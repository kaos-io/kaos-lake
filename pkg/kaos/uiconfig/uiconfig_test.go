// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-04): new file, Apache-2.0.

package uiconfig_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/treeverse/lakefs/pkg/kaos/uiconfig"
)

func TestHandler(t *testing.T) {
	for url, want := range map[string]string{
		"":                           `{"console_url":"","mlflow_url":"","legacy_repository":"data"}`,
		"https://console.kaos.eu/ui": `{"console_url":"https://console.kaos.eu/ui","mlflow_url":"","legacy_repository":"data"}`,
		"javascript:alert(1)":        `{"console_url":"","mlflow_url":"","legacy_repository":"data"}`,
		"console.kaos.eu":            `{"console_url":"","mlflow_url":"","legacy_repository":"data"}`,
	} {
		rec := httptest.NewRecorder()
		uiconfig.Handler{ConsoleURL: url, LegacyRepository: "data"}.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/kaos/v1/ui", nil))
		require.Equal(t, http.StatusOK, rec.Code)
		require.JSONEq(t, want, rec.Body.String(), url)
	}
}

func TestHandlerMLflowURL(t *testing.T) {
	for url, want := range map[string]string{
		"":                         "",
		"https://mlflow.kaos.eu":   "https://mlflow.kaos.eu",
		"http://mlflow.local:5000": "http://mlflow.local:5000",
		"javascript:alert(1)":      "",
		"mlflow.kaos.eu":           "",
		"ftp://mlflow.kaos.eu":     "",
	} {
		rec := httptest.NewRecorder()
		uiconfig.Handler{ConsoleURL: "https://console.kaos.eu", MLflowURL: url}.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/kaos/v1/ui", nil))
		require.Equal(t, http.StatusOK, rec.Code)
		require.JSONEq(t, `{"console_url":"https://console.kaos.eu","mlflow_url":"`+want+`","legacy_repository":""}`, rec.Body.String(), url)
	}
}
