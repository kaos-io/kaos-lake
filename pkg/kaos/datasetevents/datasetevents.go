// KAOS (kaos-lake, PRD novelcore/kaos#1283 D-05): new file, Apache-2.0.

// Package datasetevents tells the project's ML apps that a dataset changed, so their pipelines
// offer the new version in the `dataset` dropdown within about a minute instead of at the next
// commit or hourly poll. The dropdown is rendered by CI from the catalog (pkg/kaos/datacatalog);
// nothing re-renders it when a version is released. A change (a released or deleted `vN` tag,
// a repository labelled a dataset, a dataset deleted) is coalesced for a few seconds, then ONE
// Workflow is created in the project's CI namespace; it asks each ML app to re-read the catalog.
//
// The transport is the Kubernetes API, not a webhook: kaos-lake has no listener to protect and no
// shared secret to keep, and its service account may only create Workflows in the one CI namespace
// (a Role the platform renders). A lost event (this server down, the API refusing) is caught by
// the hourly reconcile, which compares the dataset list each pipeline was rendered with against
// the catalog.
package datasetevents

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/treeverse/lakefs/pkg/logging"
)

const (
	// Template is the ClusterWorkflowTemplate (kubecore-ci-workflows) that fans the event out to
	// the project's ML apps.
	Template = "ml-ci-dataset-release"
	// The Workflow sets no serviceAccountName: Strict templateReferencing refuses one beside the
	// ref. The template runs as dataset-release-sa (rendered by the platform with the right to
	// read the apps' reconcilers and start their runs).

	// debounce is how long a change waits for others: a burst of releases is one Workflow.
	debounce = 10 * time.Second
	// retryEvery and attempts bound how long a refused submission is retried.
	retryEvery = 5 * time.Second
	attempts   = 3

	serviceAccountDir = "/var/run/secrets/kubernetes.io/serviceaccount"
)

// Submitter creates the Workflow that carries one event.
type Submitter interface {
	Submit(ctx context.Context) error
}

// Notifier coalesces changes into submissions.
type Notifier struct {
	submit   Submitter
	debounce time.Duration
	retry    time.Duration
	log      logging.Logger

	mu      sync.Mutex
	pending bool
	// wg tracks the submissions waiting or in flight.
	wg sync.WaitGroup
}

// NewNotifier returns a Notifier that submits through s.
func NewNotifier(s Submitter, log logging.Logger) *Notifier {
	if log == nil {
		log = logging.Dummy()
	}
	return &Notifier{submit: s, debounce: debounce, retry: retryEvery, log: log.WithField("service", "kaos_dataset_events")}
}

// WithTiming sets how long a change waits for others and how long a refused submission waits to be
// retried; for tests.
func (n *Notifier) WithTiming(debounce, retry time.Duration) *Notifier {
	n.debounce, n.retry = debounce, retry
	return n
}

// Wait returns when the changes noted so far have been submitted or given up; for tests.
func (n *Notifier) Wait() { n.wg.Wait() }

// Notify records that a dataset changed. The first change starts a window; changes inside it are
// absorbed, and every one of them is already in the catalog when the Workflow runs. It never blocks.
func (n *Notifier) Notify(reason string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.pending {
		return
	}
	n.pending = true
	n.log.WithField("reason", reason).Info("Dataset changed; re-rendering the project's pipelines shortly")
	n.wg.Add(1)
	time.AfterFunc(n.debounce, func() {
		defer n.wg.Done()
		n.mu.Lock()
		n.pending = false
		n.mu.Unlock()
		n.send()
	})
}

func (n *Notifier) send() {
	var err error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			time.Sleep(n.retry)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		err = n.submit.Submit(ctx)
		cancel()
		if err == nil {
			return
		}
	}
	// Not fatal: the hourly reconcile finds the drift.
	n.log.WithError(err).Error("Dataset event not delivered; the hourly reconcile will catch up")
}

var (
	activeMu sync.RWMutex
	active   *Notifier
)

// Configure makes the Notifier for the project's CI namespace the one Notify reaches (called once,
// by lakefs run), and notes a change at once: whatever changed while this server was down, or was
// pending when it stopped, is caught up now. It returns nil when namespace is empty (off).
func Configure(namespace string, log logging.Logger) (*Notifier, error) {
	if namespace == "" {
		return nil, nil
	}
	s, err := NewKubeSubmitter(namespace)
	if err != nil {
		return nil, err
	}
	n := NewNotifier(s, log)
	Activate(n)
	n.Notify("started")
	return n, nil
}

// Activate makes n the Notifier that Notify reaches; nil turns events off.
func Activate(n *Notifier) {
	activeMu.Lock()
	active = n
	activeMu.Unlock()
}

// Notify tells the configured Notifier that a dataset changed; without one it does nothing.
func Notify(reason string) {
	activeMu.RLock()
	n := active
	activeMu.RUnlock()
	if n != nil {
		n.Notify(reason)
	}
}

// KubeSubmitter creates the Workflow through the Kubernetes API with the pod's own service-account
// token. That account may create Workflows in Namespace and do nothing else.
type KubeSubmitter struct {
	Namespace string
	// Server is the API server's base URL, https://host:port.
	Server string
	Client *http.Client
	// Token returns the service-account token (it rotates, so it is read on every call).
	Token func() (string, error)
}

// NewKubeSubmitter returns the in-cluster KubeSubmitter for namespace.
func NewKubeSubmitter(namespace string) (*KubeSubmitter, error) {
	host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return nil, errors.New("kaos.dataset_events needs to run in a cluster (KUBERNETES_SERVICE_HOST is not set)")
	}
	ca, err := os.ReadFile(serviceAccountDir + "/ca.crt")
	if err != nil {
		return nil, fmt.Errorf("kaos.dataset_events: cluster CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return nil, errors.New("kaos.dataset_events: cluster CA is not PEM")
	}
	return &KubeSubmitter{
		Namespace: namespace,
		Server:    "https://" + net.JoinHostPort(host, port),
		Client: &http.Client{Timeout: 15 * time.Second,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}},
		Token: func() (string, error) {
			b, err := os.ReadFile(serviceAccountDir + "/token")
			return strings.TrimSpace(string(b)), err
		},
	}, nil
}

// Workflow is the object created for one event.
func (s *KubeSubmitter) Workflow() map[string]any {
	return map[string]any{
		"apiVersion": "argoproj.io/v1alpha1",
		"kind":       "Workflow",
		"metadata": map[string]any{
			"generateName": "dataset-release-",
			"namespace":    s.Namespace,
			"labels":       map[string]string{"platform.kubecore.io/component": "dataset-release"},
		},
		"spec": map[string]any{
			"workflowTemplateRef": map[string]any{"name": Template, "clusterScope": true},
			"ttlStrategy":         map[string]any{"secondsAfterCompletion": 3600},
		},
	}
}

// Submit creates the Workflow. The token is never logged or put in an error.
func (s *KubeSubmitter) Submit(ctx context.Context) error {
	token, err := s.Token()
	if err != nil {
		return fmt.Errorf("service-account token: %w", err)
	}
	body, err := json.Marshal(s.Workflow())
	if err != nil {
		return err
	}
	url := fmt.Sprintf("%s/apis/argoproj.io/v1alpha1/namespaces/%s/workflows", s.Server, s.Namespace)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.Client.Do(req)
	if err != nil {
		return fmt.Errorf("create Workflow in %s: %w", s.Namespace, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("create Workflow in %s: %s: %s", s.Namespace, resp.Status, strings.TrimSpace(string(msg)))
	}
	return nil
}
