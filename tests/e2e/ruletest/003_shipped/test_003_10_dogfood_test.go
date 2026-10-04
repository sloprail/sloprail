package e2e

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T003_10: sloprail tests its own shipped rules with its own tests. The plugin's rules that have
// cases pass them (no model, no harness), and the ones that do not are listed as untested: a
// rule that gets cases must keep them passing, which is what this run enforces in CI.
func TestT003_10_TheShippedRulesPassTheirOwnCases(t *testing.T) {
	p := harness.NewRuleProject(t)
	dot := filepath.Join(harness.RepoRoot(t), "marketplace", "plugins", "sloprail", ".sloprail")

	res := p.Sr("", "sr-checks", "doctor", "--allow-untested", "--plugin", "sloprail", "--rules-dir", dot)
	require.Equal(t, 0, res.Code, res.Output)
	for _, rule := range []string{
		"sloprail/file-guard/rule-tests",
		"sloprail/file-guard/misplaced-declaration",
		"sloprail/gate/misplaced-declaration",
		"sloprail/gate/no-lifecycle-commands",
		"sloprail/gate/no-self-matching-pgrep",
		"sloprail/gate/checks-ref-sr-only",
		"sloprail/gate/read-gate-doc",
	} {
		require.Contains(t, res.Output, "ok   "+rule, "%s should be tested and passing:\n%s", rule, res.Output)
	}
}
