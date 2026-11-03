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
