package e2e

import (
	"os"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

var (
	New     = harness.New
	Turns   = harness.Turns
	Write   = harness.Write
	Skill   = harness.Skill
	ToolUse = harness.ToolUse
)

// readSkillFirst prepends the turns that satisfy every shipped read-*-doc
// guard a scenario in this package could trip — loading authoring-guardrails
// and reading script-checks.md, file-guard.md and gate.md — so a scenario
// that writes a hook script or a plugin declaration doesn't ALSO hit that
// precondition while proving something else entirely (plugin precedence,
// disabling, the OLD authoring-slop content check). Reads all three
// unconditionally: cheap, and it keeps every call site identical regardless
// of which declaration kind the scenario writes.
func readSkillFirst(t *testing.T, turns ...harness.Turn) []harness.Turn {
	t.Helper()
	return append([]harness.Turn{
		Skill("skill-setup", "authoring-guardrails"),
		ToolUse("read-setup-1", "Read", map[string]string{"file_path": harness.ShippedSkillFile(t, "script-checks.md")}),
		ToolUse("read-setup-1b", "Read", map[string]string{"file_path": harness.ShippedSkillFile(t, "check-template.sh")}),
		ToolUse("read-setup-2", "Read", map[string]string{"file_path": harness.ShippedSkillFile(t, "file-guard.md")}),
		ToolUse("read-setup-3", "Read", map[string]string{"file_path": harness.ShippedSkillFile(t, "gate.md")}),
	}, turns...)
}

// TestMain removes the binary build dir when this package's tests finish.
// Without it every e2e package leaks 15M for the life of the machine.
func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}
