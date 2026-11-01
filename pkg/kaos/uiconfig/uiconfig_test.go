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
		"":                           `{"console_url":""}`,
		"https://console.kaos.eu/ui": `{"console_url":"https://console.kaos.eu/ui"}`,
		"javascript:alert(1)":        `{"console_url":""}`,
		"console.kaos.eu":            `{"console_url":""}`,
	} {
		rec := httptest.NewRecorder()
		uiconfig.Handler{ConsoleURL: url}.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/kaos/v1/ui", nil))
		require.Equal(t, http.StatusOK, rec.Code)
		require.JSONEq(t, want, rec.Body.String(), url)
	}
}
