package srtest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUncovered(t *testing.T) {
	root := t.TempDir()
	// covered
	put(t, root, ".sloprail/gate/covered/gate.yaml", "")
	put(t, root, ".sloprail/gate/covered/tests/a/test.sh", "")
	// no tests/ at all, empty tests/, a tests/ with a folder lacking test.sh
	put(t, root, ".sloprail/gate/bare/gate.yaml", "")
	put(t, root, ".sloprail/file-guard/empty/file-guard.yaml", "")
	put(t, root, ".sloprail/file-guard/empty/tests/.keep", "")
	put(t, root, ".sloprail/context/noscript/context.yaml", "")
	put(t, root, ".sloprail/context/noscript/tests/a/readme", "")
	// a folder without a declaration is not a rule
	put(t, root, ".sloprail/gate/not-a-rule/check.sh", "")
	// structure: declared, no cases; and another .sloprail/ whose structure is covered
	put(t, root, ".sloprail/file-guard/structure.yaml", "")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "marketplace/plugins/p/.claude-plugin"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "marketplace/plugins/p/.claude-plugin/plugin.json"), []byte(`{"name":"pl"}`), 0o644))
	put(t, root, "marketplace/plugins/p/.sloprail/file-guard/structure.yaml", "")
	put(t, root, "marketplace/plugins/p/.sloprail/file-guard/structure.tests/s/test.sh", "")
	put(t, root, "marketplace/plugins/p/.sloprail/gate/g/gate.yaml", "")
	put(t, root, "tools/x/.sloprail/context/c/context.yaml", "")
	// the retired top-level tests/ covers nothing
	put(t, root, ".sloprail/tests/gate-bare/test.sh", "")
	got, err := Uncovered(root)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"context:noscript",
		"context:tools/x/c",
		"file-guard:empty",
		"gate:bare",
		"gate:pl/g",
		"structure:structure",
	}, got)
}

func TestUncoveredNothingToCover(t *testing.T) {
	got, err := Uncovered(t.TempDir())
	require.NoError(t, err)
	assert.Empty(t, got)
}

// UncoveredRules locates each rule: the .sloprail's parent relative to the root and the qualified
// name, which for a plugin without a `name` in plugin.json is its folder's (as PluginName says).
func TestUncoveredRulesLocatesEachRule(t *testing.T) {
	root := t.TempDir()
	put(t, root, ".sloprail/gate/top/gate.yaml", "")
	manifest := func(dir, body string) {
		require.NoError(t, os.MkdirAll(filepath.Join(root, dir, ".claude-plugin"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, dir, ".claude-plugin", "plugin.json"), []byte(body), 0o644))
	}
	manifest("plugins/unnamed", "{}")
	put(t, root, "plugins/unnamed/.sloprail/gate/g/gate.yaml", "")
	manifest("plugins/named", `{"name":"pl"}`)
	put(t, root, "plugins/named/.sloprail/file-guard/structure.yaml", "")
	got, err := UncoveredRules(root)
	require.NoError(t, err)
	assert.Equal(t, []UncoveredRule{
		{Nature: "gate", Rule: "top", Dir: ".", Qualified: "top"},
		{Nature: "gate", Rule: "g", Dir: "plugins/unnamed", Qualified: "unnamed/g"},
		{Nature: "structure", Rule: "structure", Dir: "plugins/named", Qualified: "pl/structure"},
	}, got)
}
