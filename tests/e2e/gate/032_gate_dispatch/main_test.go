package e2e

import (
	"os"
	"os/exec"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The gate is the new checkpoint-on-an-event nature: it wakes on a pre-action
// event (or Stop), evaluates require + checks, and blocks or admits. These tests
// drive the compiled sr-session through a10n-claude-mock against a sandboxed
// project holding real .sloprail/gate/*/gate.yaml, so what fires is the plugin's
// own dispatch — a failing script check blocks, a missing skill require blocks,
// a met require with passing checks admits, and the gate's verdict lands in the
// gates[] map a context will read.
var New = harness.New

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

var (
	Turns = harness.Turns
	Write = harness.Write
	Skill = harness.Skill
	Bash  = harness.Bash
)

// commitGuards commits the project's `.sloprail` tree so a guard written after
// GitInit's own baseline commit (e.Gate writes straight to disk, uncommitted) is
// part of the baseline the engine diffs against, not this cycle's own work —
// the same reason 037_fileguard_pure_require's own commitGuards commits: the
// sloprail plugin ships gates and file-guards that judge a guardrail's OWN files
// (authoring-slop's script/template check, and the shipped read-*-doc rules
// requiring the skill be read before writing one), and an uncommitted guard
// reads as this cycle's write to THOSE rules too, not only to the rule under
// test.
func commitGuards(t *testing.T, proj string) {
	t.Helper()
	if out, err := exec.Command("git", "-C", proj, "add", ".sloprail").CombinedOutput(); err != nil {
		t.Fatalf("commitGuards: git add .sloprail: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", proj, "commit", "-m", "baseline .sloprail").CombinedOutput(); err != nil {
		t.Fatalf("commitGuards: git commit: %v\n%s", err, out)
	}
}
