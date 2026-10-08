package srtest

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/sloprail/sloprail/internal/harness"
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
	put(t, root, ".sloprail/gate/g/tests/dup/test.sh", "test -d .sloprail && test ! -e .sloprail/gate/g/tests && test -f \"$SR_TEST_CASE_DIR/test.sh\" && test -z \"$SR_TEST_PLUGIN_DIR\"")
	put(t, root, "marketplace/plugins/p/.claude-plugin/plugin.json", `{"name":"p"}`)
	put(t, root, "marketplace/plugins/p/.sloprail/gate/g/tests/dup/test.sh", "test ! -e .sloprail && test -n \"$SR_TEST_PLUGIN_DIR\" && test -f \"$SR_TEST_CASE_DIR/test.sh\"")
	put(t, root, "marketplace/plugins/p/.sloprail/file-guard/f/tests/only/test.sh", "exit 0")
	put(t, root, "marketplace/plugins/p/.sloprail/gate/g/tests/no-script/readme", "x")
	put(t, root, "tools/x/.sloprail/context/c/tests/t/test.sh", "test -d .sloprail")
	put(t, root, "node_modules/q/.sloprail/gate/g/tests/n/test.sh", "exit 1")
	put(t, root, ".claude/worktrees/w/.sloprail/gate/g/tests/w/test.sh", "exit 1")
	put(t, root, "sr-test-123/.sloprail/gate/g/tests/tmp/test.sh", "exit 1")
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
	assert.Equal(t, []string{"gate/g:dup", "marketplace/plugins/p:file-guard/f:only", "marketplace/plugins/p:gate/g:dup", "tools/x:context/c:t"}, subjects)
	assert.Empty(t, cs[0].Plugins)
	plug := filepath.Join(root, "marketplace", "plugins", "p")
	assert.Equal(t, []string{plug, core}, cs[1].Plugins)
	assert.Equal(t, plug, cs[1].Target)
	assert.Equal(t, "file-guard/f", cs[1].Owner())
	assert.Empty(t, cs[3].Plugins, "a non-plugin nested folder is a project case")
}

func TestDiscoverPluginUnderTestIsCoreInstallsOnce(t *testing.T) {
	root := t.TempDir()
	put(t, root, "pl/.claude-plugin/plugin.json", `{"name":"sloprail"}`)
	put(t, root, "pl/.sloprail/file-guard/structure.tests/a/test.sh", "exit 0")
	cs, err := Discover(root, filepath.Join(root, "pl"))
	require.NoError(t, err)
	require.Len(t, cs, 1)
	assert.Equal(t, []string{filepath.Join(root, "pl")}, cs[0].Plugins)
	assert.Equal(t, "structure", cs[0].Nature)
	assert.Equal(t, "file-guard/structure", cs[0].Owner())
	assert.Equal(t, "pl:file-guard/structure:a", cs[0].Subject)
}

// A plugin under test that is another checkout's copy of the core plugin (a git worktree, while
// sr-test was built in the main checkout) is installed once: the copy under test, not both.
func TestDiscoverPluginUnderTestShadowsAnotherCheckoutsCore(t *testing.T) {
	root := t.TempDir()
	putJSON(t, root, "pl/.claude-plugin/plugin.json", `{"name":"sloprail"}`)
	put(t, root, "pl/.sloprail/file-guard/structure.tests/a/test.sh", "exit 0")
	core := t.TempDir() // the main checkout's marketplace/plugins/sloprail
	putJSON(t, core, ".claude-plugin/plugin.json", `{"name":"sloprail"}`)
	cs, err := Discover(root, core)
	require.NoError(t, err)
	require.Len(t, cs, 1)
	assert.Equal(t, []string{filepath.Join(root, "pl")}, cs[0].Plugins)
}

func TestRunNestedContexts(t *testing.T) {
	root := nestedTree(t)
	var (
		seenMu sync.Mutex
		seen   []Context
	)
	rs, err := Run(root, Options{
		CorePluginDir: filepath.Join(root, "core"),
		Rules: func(c Context, _ io.Writer) []string {
			seenMu.Lock()
			seen = append(seen, c)
			seenMu.Unlock()
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
	assert.Contains(t, byS, "gate/g:dup")
	assert.Contains(t, byS, "marketplace/plugins/p:gate/g:dup")
	assert.Equal(t, "gate/g", byS["gate/g:dup"].Owner)
	withPlugins := 0
	for _, c := range seen {
		if len(c.Plugins) > 0 {
			withPlugins++
		}
	}
	assert.Equal(t, 2, withPlugins)
	assert.NotEmpty(t, strings.Join(byS["gate/g:dup"].Metadata.Rules, ""))
}

func TestDiscoverLayout(t *testing.T) {
	root := t.TempDir()
	put(t, root, ".sloprail/gate/g/tests/a/test.sh", "")
	put(t, root, ".sloprail/file-guard/f/tests/b/test.sh", "")
	put(t, root, ".sloprail/context/c/tests/c/test.sh", "")
	put(t, root, ".sloprail/file-guard/structure.tests/s/test.sh", "")
	// not cases: the retired top-level tests/, a test.sh not under tests/<case>/, a case without test.sh,
	// a tests/ at the wrong depth, and an unknown nature.
	put(t, root, ".sloprail/tests/old/test.sh", "")
	put(t, root, ".sloprail/gate/g/test.sh", "")
	put(t, root, ".sloprail/gate/g/tests/empty/readme", "")
	put(t, root, ".sloprail/gate/tests/x/test.sh", "")
	put(t, root, ".sloprail/other/o/tests/z/test.sh", "")
	put(t, root, ".sloprail/file-guard/structure.tests/s/deeper/test.sh", "")
	cs, err := Discover(root, "")
	require.NoError(t, err)
	var got []string
	for _, c := range cs {
		got = append(got, c.Nature+"|"+c.Rule+"|"+c.Subject+"|"+c.Owner())
	}
	assert.Equal(t, []string{
		"context|c|context/c:c|context/c",
		"file-guard|f|file-guard/f:b|file-guard/f",
		"structure||file-guard/structure:s|file-guard/structure",
		"gate|g|gate/g:a|gate/g",
	}, got)
}

func putJSON(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
}

// Every harness's manifest folder makes a plugin folder, and names it.
func TestPluginManifestOfAnyHarness(t *testing.T) {
	root := t.TempDir()
	for _, d := range harness.PluginManifestDirs {
		putJSON(t, root, "p"+d+"/"+d+"/plugin.json", `{"name":"n`+d+`"}`)
		assert.True(t, IsPlugin(filepath.Join(root, "p"+d)), d)
		assert.Equal(t, "n"+d, PluginName(filepath.Join(root, "p"+d)), d)
	}
}
