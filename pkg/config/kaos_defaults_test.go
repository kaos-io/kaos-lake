// KAOS (kaos-lake, PRD novelcore/kaos#1217): new file, Apache-2.0.

package config_test

import (
	"testing"

	"github.com/treeverse/lakefs/pkg/config"
	"github.com/treeverse/lakefs/pkg/testutil"
)

// kaos-lake is hosted for clients and frozen at lakeFS v1.86.0, so by default it must not
// report usage, check Treeverse for advisories, or recommend upgrading to upstream releases
// (BUSL-1.1 from v1.87.0). Each stays available through configuration. Setup e-mails travel
// only through the stats sender, so stats off also keeps them local.
func TestKaosDefaults_NoPhoneHome(t *testing.T) {
	baseCfg, err := config.NewConfig("", &config.ConfigImpl{})
	testutil.Must(t, err)

	if baseCfg.Stats.Enabled {
		t.Error("stats.enabled defaults to true, want false")
	}
	if baseCfg.Security.CheckLatestVersion {
		t.Error("security.check_latest_version defaults to true, want false")
	}
	if baseCfg.Security.AuditCheckURL != "" {
		t.Errorf("security.audit_check_url defaults to %q, want empty (audit check off)", baseCfg.Security.AuditCheckURL)
	}
}
