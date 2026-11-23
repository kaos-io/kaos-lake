// KAOS (kaos-lake, PRD novelcore/kaos#1283): new file, Apache-2.0.

package config_test

import (
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"github.com/treeverse/lakefs/pkg/config"
)

// A kaos.* key this build does not know (set by a newer chart) must not stop the server from
// starting, while a mistyped key outside kaos.* is still an error as in upstream.
func TestKaosUnknownKeyDoesNotRefuseStart(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("kaos.meluxina.namespaces", []string{"vision-ml"})
	viper.Set("kaos.some_future_key", "x")
	viper.Set("kaos.future_block.nested", 3)
	baseCfg, err := config.NewConfig("", &config.ConfigImpl{})
	require.NoError(t, err)
	require.Equal(t, []string{"vision-ml"}, baseCfg.KAOS.Meluxina.Namespaces)

	viper.Reset()
	viper.Set("not_a_lakefs_key", "x")
	_, err = config.NewConfig("", &config.ConfigImpl{})
	require.Error(t, err)
}

// kaos.ui.mlflow_url is read into the KAOS block (PRD novelcore/kaos#1283: an app's Model links to MLflow).
func TestKaosMLflowURL(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("kaos.ui.mlflow_url", "https://mlflow.kaos.eu")
	baseCfg, err := config.NewConfig("", &config.ConfigImpl{})
	require.NoError(t, err)
	require.Equal(t, "https://mlflow.kaos.eu", baseCfg.KAOS.UI.MLflowURL)
}

// kaos.sharing (PRD novelcore/kaos#1283 F-12) is its own block with its own catch-all: a newer
// chart's kaos.sharing.* key must not stop the server, and the known keys still decode.
func TestKaosSharingUnknownSubKey(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("kaos.sharing.peers_path", "/etc/kaos-lake/peers/peers.json")
	viper.Set("kaos.sharing.machine_key_path", "/etc/kaos-lake/machine-key/ZITADEL_MACHINE_KEY")
	viper.Set("kaos.sharing.cache_ttl", "30s")
	viper.Set("kaos.sharing.some_future_key", map[string]any{"x": 1})
	baseCfg, err := config.NewConfig("", &config.ConfigImpl{})
	require.NoError(t, err)
	s := baseCfg.KAOS.Sharing
	require.Equal(t, "/etc/kaos-lake/peers/peers.json", s.PeersPath)
	require.Equal(t, "/etc/kaos-lake/machine-key/ZITADEL_MACHINE_KEY", s.MachineKeyPath)
	require.Equal(t, 30*time.Second, s.CacheTTL)
	require.Zero(t, s.Interval, "defaults are applied by pkg/kaos/sharing, not here")
	require.Contains(t, s.Unknown, "some_future_key")
}

// Without a kaos.sharing block the feature is off: nothing is set.
func TestKaosSharingAbsentIsOff(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	baseCfg, err := config.NewConfig("", &config.ConfigImpl{})
	require.NoError(t, err)
	require.Empty(t, baseCfg.KAOS.Sharing.PeersPath)
	require.Empty(t, baseCfg.KAOS.Sharing.MachineKeyPath)
	require.Zero(t, baseCfg.KAOS.Sharing.Interval)
	require.Zero(t, baseCfg.KAOS.Sharing.CacheTTL)
	require.Empty(t, baseCfg.KAOS.Unknown)
}

// kaos.dataset_events (PRD novelcore/kaos#1283 D-05) names the CI namespace; absent, it is off.
func TestKaosDatasetEvents(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	baseCfg, err := config.NewConfig("", &config.ConfigImpl{})
	require.NoError(t, err)
	require.Empty(t, baseCfg.KAOS.DatasetEvents.Namespace)

	viper.Set("kaos.dataset_events.namespace", "vision-ci")
	baseCfg, err = config.NewConfig("", &config.ConfigImpl{})
	require.NoError(t, err)
	require.Equal(t, "vision-ci", baseCfg.KAOS.DatasetEvents.Namespace)
}

// kaos.kaos_lib.support (PRD novelcore/kaos#2017 F-55) is read as opaque data, nested fields
// included; absent, it is empty and not collected as unknown.
func TestKaosLibSupport(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	baseCfg, err := config.NewConfig("", &config.ConfigImpl{})
	require.NoError(t, err)
	require.Empty(t, baseCfg.KAOS.KaosLib.Support)

	viper.Set("kaos.kaos_lib.support", map[string]any{
		"version":        "0.4.0",
		"addresses":      map[string]any{"index_url": "https://i"},
		"a_future_field": []any{"x"},
	})
	baseCfg, err = config.NewConfig("", &config.ConfigImpl{})
	require.NoError(t, err)
	s := baseCfg.KAOS.KaosLib.Support
	require.Equal(t, "0.4.0", s["version"])
	require.Equal(t, map[string]any{"index_url": "https://i"}, s["addresses"])
	require.Equal(t, []any{"x"}, s["a_future_field"])
	require.NotContains(t, baseCfg.KAOS.Unknown, "kaos_lib")
}

// PRD novelcore/kaos#1533: kaos.catalog.workloads entries are <namespace>/<serviceaccount>, and
// need a project; a bad entry stops the server from starting.
func TestKaosCatalogWorkloads(t *testing.T) {
	load := func(workloads []string, project, idProject string) error {
		viper.Reset()
		t.Cleanup(viper.Reset)
		viper.Set("database.type", "local")
		viper.Set("database.local.path", t.TempDir())
		viper.Set("auth.encrypt.secret_key", "k")
		viper.Set("blockstore.type", "local")
		viper.Set("kaos.catalog.workloads", workloads)
		viper.Set("kaos.catalog.project", project)
		viper.Set("kaos.token_identity.project", idProject)
		baseCfg, err := config.NewConfig("", &config.ConfigImpl{})
		if err != nil {
			return err
		}
		return baseCfg.Validate()
	}
	require.NoError(t, load([]string{"kaos-system/osr-read-api"}, "e2e-hpc-lab", ""))
	require.NoError(t, load([]string{"kaos-system/osr-read-api"}, "", "e2e-hpc-lab"), "falls back to token_identity.project")
	require.NoError(t, load(nil, "", ""))
	for _, bad := range []string{"osr-read-api", "a/b/c", "/sa", "ns/", "Kaos/sa", "ns/sa_x", "ns/-sa", ""} {
		require.ErrorIs(t, load([]string{bad}, "p", ""), config.ErrBadConfiguration, bad)
	}
	require.ErrorIs(t, load([]string{"ns/sa"}, "", ""), config.ErrBadConfiguration)
	require.ErrorIs(t, load([]string{"ns/sa"}, "Bad_Project", ""), config.ErrBadConfiguration)
}
