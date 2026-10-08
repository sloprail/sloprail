package codex

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/harness"
)

// installed lays out a Codex home with plugin acme@mk enabled from a local
// marketplace AND installed in the cache (what `codex plugin add` leaves), and
// returns the marketplace source dir, the cache version dir and the home.
func installed(t *testing.T) (source, cached, home string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("CODEX_HOME", "")
	market := t.TempDir()
	source = filepath.Join(market, "plugins", "acme")
	require.NoError(t, os.MkdirAll(source, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(market, ".agents", "plugins"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(market, ".agents", "plugins", "marketplace.json"),
		[]byte(`{"plugins":[{"name":"acme","source":{"source":"local","path":"./plugins/acme"}}]}`), 0o644))
	cached = filepath.Join(home, ".codex", "plugins", "cache", "mk", "acme", "1.0.0")
	require.NoError(t, os.MkdirAll(cached, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".codex", "config.toml"), []byte(
		"[marketplaces.mk]\nsource_type = \"local\"\nsource = \""+market+"\"\n\n[plugins.\"acme@mk\"]\nenabled = true\n"), 0o644))
	return source, cached, home
}

// TestResolve_PrefersTheMarketplaceSourceOverTheCache documents why a skill read
// from the cache copy was missed: Resolve names one root, the source.
func TestResolve_PrefersTheMarketplaceSourceOverTheCache(t *testing.T) {
	source, _, home := installed(t)
	res, err := Resolve(t.TempDir(), home)
	require.NoError(t, err)
	require.Len(t, res.Roots, 1)
	assert.Equal(t, source, res.Roots[0].Dir)
}

// TestPluginDirs_IncludeTheInstalledCacheCopy: the agent loads the plugin's skills
// from the cache, so that copy must be among the plugin's directories.
func TestPluginDirs_IncludeTheInstalledCacheCopy(t *testing.T) {
	source, cached, home := installed(t)
	dirs, err := harness.PluginDirs(New(), t.TempDir(), home)
	require.NoError(t, err)
	assert.Equal(t, []string{source, cached}, dirs)
}
