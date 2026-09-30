package e2e

import (
	"os"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// pure_require: a file-guard whose WHOLE enforcement is a `require:` precondition,
// carrying NO `checks:` at all, both VALIDATES and ENFORCES.
//
// A file-guard evaluates its `require` BEFORE any check and refuses the write when
// it is unmet, so a guard that is nothing but a `require: [{skill}]` is a complete
// rule on its own — the pass-through check it used to be forced to carry (a `.sh`
// that only `exit 0`s, present solely to satisfy the validator's old "checks
// required" rule) was pure boilerplate. This suite pins both halves of the change:
//
//   - it VALIDATES: `sr-file declarations` accepts the checks-less guard and reports
//     it loaded (no ErrMissingField), the CLI-level reconciliation the loader change
//     is about;
//   - it ENFORCES: driven through the mock as a real session, a pure-require gate
//     with only a skill `require` REFUSES a matching write when the skill was not
//     loaded and PERMITS it once a real Skill tool_use for it is in the record —
//     the native skill precondition, with no check anywhere in the guard.
//
// The enforcement half mirrors the gate's pure-require pair (032_01's
// T032_03/T032_04) one nature over: same skill, same loaded-vs-not distinction,
// against a file-guard instead of a gate.
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
