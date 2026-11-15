// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-04): new file, Apache-2.0.

package uiconfig_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/treeverse/lakefs/pkg/kaos/uiconfig"
)

func TestHandler(t *testing.T) {
	for url, want := range map[string]string{
		"":                           `{"console_url":"","mlflow_url":"","legacy_repository":"data","meluxina":false,"project":""}`,
		"https://console.kaos.eu/ui": `{"console_url":"https://console.kaos.eu/ui","mlflow_url":"","legacy_repository":"data","meluxina":false,"project":""}`,
		"javascript:alert(1)":        `{"console_url":"","mlflow_url":"","legacy_repository":"data","meluxina":false,"project":""}`,
		"console.kaos.eu":            `{"console_url":"","mlflow_url":"","legacy_repository":"data","meluxina":false,"project":""}`,
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
		require.JSONEq(t, `{"console_url":"https://console.kaos.eu","mlflow_url":"`+want+`","legacy_repository":"","meluxina":false,"project":""}`, rec.Body.String(), url)
	}
}

func TestHandlerMeluxina(t *testing.T) {
	for _, on := range []bool{false, true} {
		rec := httptest.NewRecorder()
		uiconfig.Handler{Meluxina: on}.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/kaos/v1/ui", nil))
		var got map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
		require.Equal(t, on, got["meluxina"])
	}
}

func TestHandlerProject(t *testing.T) {
	rec := httptest.NewRecorder()
	uiconfig.Handler{Project: "greenland"}.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/kaos/v1/ui", nil))
	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, "greenland", got["project"])
}

// The kaos-lib release record is served unchanged, new fields included (PRD novelcore/kaos#2017
// F-55); without one the field is absent.
func TestHandlerKaosLibSupport(t *testing.T) {
	serve := func(h uiconfig.Handler) string {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/kaos/v1/ui", nil))
		require.Equal(t, http.StatusOK, rec.Code)
		return rec.Body.String()
	}
	var support map[string]any
	require.NoError(t, json.Unmarshal([]byte(`{"version":"0.4.0","supported":">=0.3","breaking":true,
		"deprecated":[{"range":"<0.3","unsupported_after":"2026-12-31"}],
		"addresses":{"releases_base":"https://r","index_url":"https://i","docs_base":"https://d","docs_current":"https://dc"},
		"recommended":"0.4.0","a_future_field":{"x":[1,2]}}`), &support))
	body := serve(uiconfig.Handler{KaosLibSupport: support})
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &got))
	require.Equal(t, support, got["kaos_lib_support"])
	require.Equal(t, "", got["console_url"], "the other fields are unchanged")

	for _, h := range []uiconfig.Handler{{}, {KaosLibSupport: map[string]any{}}} {
		require.NotContains(t, serve(h), "kaos_lib_support")
	}
}
