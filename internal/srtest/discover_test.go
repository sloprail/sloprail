package srtest

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func put(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755))
}

// tree: a root case, a plugin case (same name as the root's), another plugin, a plain nested
// folder, and trees that must be skipped.
func nestedTree(t *testing.T) string {
	root := t.TempDir()
	put(t, root, ".sloprail/tests/dup/test.sh", "test -d .sloprail && test ! -e .sloprail/tests && test -f \"$SR_TEST_CASE_DIR/test.sh\" && test -z \"$SR_TEST_PLUGIN_DIR\"")
	put(t, root, "marketplace/plugins/p/.claude-plugin/plugin.json", `{"name":"p"}`)
	put(t, root, "marketplace/plugins/p/.sloprail/tests/dup/test.sh", "test ! -e .sloprail && test -n \"$SR_TEST_PLUGIN_DIR\" && test -f \"$SR_TEST_CASE_DIR/test.sh\"")
	put(t, root, "marketplace/plugins/p/.sloprail/tests/only/test.sh", "exit 0")
	put(t, root, "marketplace/plugins/p/.sloprail/tests/no-script/readme", "x")
	put(t, root, "tools/x/.sloprail/tests/t/test.sh", "test -d .sloprail")
	put(t, root, "node_modules/q/.sloprail/tests/n/test.sh", "exit 1")
	put(t, root, ".claude/worktrees/w/.sloprail/tests/w/test.sh", "exit 1")
	put(t, root, "sr-test-123/.sloprail/tests/tmp/test.sh", "exit 1")
	return root
}

func TestDiscoverNested(t *testing.T) {
	root := nestedTree(t)
	core := filepath.Join(root, "core")
	cs, err := Discover(root, core)
	require.NoError(t, err)
	var subjects []string
	for _, c := range cs {
		subjects = append(subjects, c.Subject)
	}
	assert.Equal(t, []string{"dup", "marketplace/plugins/p:dup", "marketplace/plugins/p:only", "tools/x:t"}, subjects)
	assert.Empty(t, cs[0].Plugins)
	plug := filepath.Join(root, "marketplace", "plugins", "p")
	assert.Equal(t, []string{plug, core}, cs[1].Plugins)
	assert.Equal(t, plug, cs[1].Target)
	assert.Empty(t, cs[3].Plugins, "a non-plugin nested folder is a project case")
}

func TestDiscoverPluginUnderTestIsCoreInstallsOnce(t *testing.T) {
	root := t.TempDir()
	put(t, root, "pl/.claude-plugin/plugin.json", `{"name":"sloprail"}`)
	put(t, root, "pl/.sloprail/tests/a/test.sh", "exit 0")
	cs, err := Discover(root, filepath.Join(root, "pl"))
	require.NoError(t, err)
	require.Len(t, cs, 1)
	assert.Equal(t, []string{filepath.Join(root, "pl")}, cs[0].Plugins)
}

func TestRunNestedContexts(t *testing.T) {
	root := nestedTree(t)
	var seen []Context
	rs, err := Run(root, Options{
		CorePluginDir: filepath.Join(root, "core"),
		Rules: func(c Context, _ io.Writer) []string {
			seen = append(seen, c)
			return []string{"gate:" + filepath.Base(c.Dir)[:2]}
		},
	})
	require.NoError(t, err)
	byS := byName(rs)
	// same-named cases do not collide
	require.Len(t, rs, 4)
	for s, r := range byS {
		assert.Equal(t, Pass, r.Status, "%s: %s", s, r.Output)
	}
	assert.Contains(t, byS, "dup")
	assert.Contains(t, byS, "marketplace/plugins/p:dup")
	withPlugins := 0
	for _, c := range seen {
		if len(c.Plugins) > 0 {
			withPlugins++
		}
	}
	assert.Equal(t, 2, withPlugins)
	assert.NotEmpty(t, strings.Join(byS["dup"].Metadata.Rules, ""))
}
