package guardrail

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// The core sloprail plugin's path-shaped rules must judge a nested `.sloprail/`
// folder (marketplace/plugins/<p>/.sloprail/...) exactly as they judge the root
// one. These read the SHIPPED declarations, so a rule edited back to a root-only
// matcher fails here, not silently in the field.

const coreDotDir = "../../marketplace/plugins/sloprail/.sloprail"

type coreDecl struct {
	Match string `yaml:"match"`
	On    []struct {
		Event string `yaml:"event"`
		Match string `yaml:"match"`
	} `yaml:"on"`
}

func readCore(t *testing.T, nature, rule string) coreDecl {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(coreDotDir, nature, rule, nature+".yaml"))
	require.NoError(t, err)
	var d coreDecl
	require.NoError(t, yaml.Unmarshal(b, &d))
	return d
}

func coreFileMatches(t *testing.T, rule, path string) bool {
	t.Helper()
	m, err := CompileFileMatch(readCore(t, "file-guard", rule).Match)
	require.NoError(t, err)
	ok, err := m.Match(fileScopeEvent(path))
	require.NoError(t, err)
	return ok
}

func coreGateMatches(t *testing.T, rule, path string) bool {
	t.Helper()
	d := readCore(t, "gate", rule)
	require.NotEmpty(t, d.On)
	for _, on := range d.On {
		if on.Event != "PreFileWrite" {
			continue
		}
		m, err := CompileGateMatch(on.Match, preFileCreateKind)
		require.NoError(t, err)
		ok, err := m.Match(eventScopeEvent("PreFileWrite", map[string]any{"path": path}, nil))
		require.NoError(t, err)
		return ok
	}
	t.Fatalf("gate %s has no PreFileWrite trigger", rule)
	return false
}

const nest = "marketplace/plugins/x/.sloprail/"

func TestCoreRules_GateDocReadsCoverNestedFolders(t *testing.T) {
	cases := []struct{ rule, rel string }{
		{"read-gate-doc", "gate/demo/gate.yaml"},
		{"read-gate-doc", "gate/demo/gate.yml"},
		{"read-file-guard-doc", "file-guard/demo/file-guard.yaml"},
		{"read-context-doc", "context/demo/context.yaml"},
		{"read-structure-gate-doc", "file-guard/structure.yaml"},
	}
	for _, c := range cases {
		assert.True(t, coreGateMatches(t, c.rule, ".sloprail/"+c.rel), "%s root %s", c.rule, c.rel)
		assert.True(t, coreGateMatches(t, c.rule, nest+c.rel), "%s nested %s", c.rule, c.rel)
		assert.True(t, coreGateMatches(t, c.rule, "a/"+nest+c.rel), "%s deeper %s", c.rule, c.rel)
	}
}

func TestCoreRules_GateDocReadsStayNarrow(t *testing.T) {
	// near-boundary: a folder that merely ends in .sloprail, a data file, a deeper rule file
	for _, p := range []string{
		"marketplace/plugins/x/not.sloprail/gate/demo/gate.yaml",
		"marketplace/plugins/x/.sloprailx/gate/demo/gate.yaml",
		nest + "gate/demo/data.yaml",
		nest + "gate/demo/extra/gate.yaml",
		"gate/demo/gate.yaml",
	} {
		assert.False(t, coreGateMatches(t, "read-gate-doc", p), p)
	}
	assert.False(t, coreGateMatches(t, "read-structure-gate-doc", nest+"file-guard/x/structure.yaml"))
}

func TestCoreRules_MisplacedDeclarationCoversNestedFolders(t *testing.T) {
	for _, p := range []string{".sloprail/structure.yaml", nest + "structure.yaml", nest + "gate/x/gate.yml"} {
		assert.True(t, coreGateMatches(t, "misplaced-declaration", p), "gate %s", p)
		assert.True(t, coreFileMatches(t, "misplaced-declaration", p), "file-guard %s", p)
	}
	for _, p := range []string{"marketplace/plugins/x/structure.yaml", "marketplace/plugins/x/not.sloprail/a.yaml", nest + "gate/x/run.sh"} {
		assert.False(t, coreGateMatches(t, "misplaced-declaration", p), "gate %s", p)
		assert.False(t, coreFileMatches(t, "misplaced-declaration", p), "file-guard %s", p)
	}
}

func TestCoreRules_GroundedRuleChangesCoversNestedFolders(t *testing.T) {
	for _, p := range []string{".sloprail/gate/a/gate.yaml", nest + "gate/a/gate.yaml", nest + "config.yaml"} {
		assert.True(t, coreFileMatches(t, "grounded-rule-changes", p), p)
	}
	for _, p := range []string{"marketplace/plugins/x/README.md", "marketplace/plugins/x/not.sloprail/a", "marketplace/plugins/x/.sloprailx/a"} {
		assert.False(t, coreFileMatches(t, "grounded-rule-changes", p), p)
	}
	assert.True(t, coreGateMatches(t, "grounded-rule-changes", ".sloprail/config.yaml"))
	assert.True(t, coreGateMatches(t, "grounded-rule-changes", nest+"config.yaml"))
	assert.False(t, coreGateMatches(t, "grounded-rule-changes", nest+"gate/a/config.yaml"))
	assert.False(t, coreGateMatches(t, "grounded-rule-changes", "marketplace/plugins/x/config.yaml"))
}

func TestCoreRules_AuthoringSlopCoversNestedFolders(t *testing.T) {
	for _, p := range []string{".sloprail/gate/a/x.sh", nest + "gate/a/x.sh", nest + "file-guard/a/j.md.j2", nest + "context/a/e.sh"} {
		assert.True(t, coreFileMatches(t, "authoring-slop", p), p)
	}
	assert.True(t, coreGateMatches(t, "authoring-slop", nest+"gate/a/x.sh"))
	assert.True(t, coreGateMatches(t, "read-script-checks-doc", nest+"gate/a/x.sh"))
	assert.True(t, coreGateMatches(t, "read-judge-checks-doc", nest+"gate/a/j.md.j2"))
}

// misplacedVerdict runs the shipped check library on one path: permitted, or the
// refusal reason.
func misplacedVerdict(t *testing.T, path string) (bool, string) {
	t.Helper()
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not on PATH")
	}
	lib, err := filepath.Abs(filepath.Join(coreDotDir, "file-guard", "misplaced-declaration", "check-path-lib.sh"))
	require.NoError(t, err)
	out, err := exec.Command("sh", "-c", `. "$1"; lib_setup; path="$2"; lib_check`, "sh", lib, path).Output()
	if err == nil {
		return true, ""
	}
	var ee *exec.ExitError
	require.True(t, errors.As(err, &ee), "unexpected: %v", err)
	return false, string(out)
}

func TestCoreRules_MisplacedDeclarationScriptResolvesItsOwnRoot(t *testing.T) {
	ok, why := misplacedVerdict(t, nest+"structure.yaml")
	assert.False(t, ok)
	assert.Contains(t, why, "belongs at "+nest+"file-guard/structure.yaml", "a nested folder names its own root, not the repository's")

	ok, why = misplacedVerdict(t, ".sloprail/structure.yaml")
	assert.False(t, ok)
	assert.Contains(t, why, "belongs at .sloprail/file-guard/structure.yaml")

	ok, why = misplacedVerdict(t, nest+"foo.yaml")
	assert.False(t, ok)
	assert.Contains(t, why, nest+"<file-guard|gate|context>/<rule-name>/")

	// a fixture folder under tests/ is a root of its own
	ok, why = misplacedVerdict(t, ".sloprail/tests/c/.sloprail/gate.yaml")
	assert.False(t, ok)
	assert.Contains(t, why, ".sloprail/tests/c/.sloprail/gate/<rule-name>/gate.yaml")

	for _, p := range []string{
		nest + "config.yaml", nest + "file-guard/structure.yaml", nest + "gate/a/gate.yaml",
		nest + "file-guard/a/file-guard.yaml", nest + "context/a/context.yaml",
		nest + "gate/a/data.yaml", "marketplace/plugins/x/structure.yaml", ".sloprail/tests/c/.sloprail/file-guard/structure.yaml",
	} {
		ok, why = misplacedVerdict(t, p)
		assert.True(t, ok, "%s: %s", p, why)
	}
}
