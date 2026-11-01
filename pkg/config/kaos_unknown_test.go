// KAOS (kaos-lake, PRD novelcore/kaos#1283): new file, Apache-2.0.

package config_test

import (
	"testing"

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
