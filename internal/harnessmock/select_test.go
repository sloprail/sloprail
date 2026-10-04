package harnessmock

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveTable(t *testing.T) {
	tree := func(dirs ...string) string {
		d := t.TempDir()
		for _, x := range dirs {
			require.NoError(t, os.MkdirAll(filepath.Join(d, x), 0o755))
		}
		return d
	}
	cases := []struct {
		name, override string
		dirs           []string
		harness        Harness
		err            string
	}{
		{"claude plugin", "", []string{".claude-plugin"}, Claude, ""},
		{"no markers defaults to claude", "", nil, Claude, ""},
		{"claude wins when several", "", []string{".cursor", ".claude-plugin"}, Claude, ""},
		{"codex detected", "", []string{".codex-plugin"}, "", "codex-mock not released yet"},
		{"cursor detected", "", []string{".cursor"}, "", "cursor-mock not released yet"},
		{"override claude beats codex marker", "claude", []string{".codex"}, Claude, ""},
		{"override codex", "codex", []string{".claude-plugin"}, "", "codex-mock not released yet"},
		{"override cursor", "cursor", nil, "", "cursor-mock not released yet"},
		{"unknown override", "vim", nil, "", `unknown harness "vim"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, err := Resolve(c.override, tree(c.dirs...))
			if c.err != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), c.err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, c.harness, m.Harness)
			assert.Equal(t, "a10n-claude-mock", m.Binary)
			assert.Equal(t, Version, m.Version)
		})
	}
}

func TestLocalPluginMarketplace(t *testing.T) {
	p := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(p, ".claude-plugin"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(p, ".claude-plugin", "plugin.json"), []byte(`{"name":"mine"}`), 0o644))
	dir := t.TempDir()
	names, err := LocalPluginMarketplace("m", dir, []string{p})
	require.NoError(t, err)
	assert.Equal(t, []string{"mine"}, names)
	_, err = os.Stat(filepath.Join(dir, "plugins", "mine", ".claude-plugin", "plugin.json"))
	assert.NoError(t, err)
}
