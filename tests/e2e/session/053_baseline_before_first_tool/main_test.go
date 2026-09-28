package e2e

import (
	"os"
	"testing"

	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// Where a FRESH session's baseline is taken, under the order real Claude Code
// writes its transcript in: after SessionStart. The session's first tool call is
// where the point is recorded, before anything the agent does can move HEAD —
// so an agent that commits its work in its first turn still has that work
// inside the difference its file-guards judge at Stop.
//
// The mock writes a fresh session's record in that order itself: nothing on
// disk while SessionStart runs (a10n-claude-mock, e2e T017_01). Before it did,
// SessionStart always found a record, and the defect these tests exist for
// could not happen under the mock.
var (
	New   = harness.New
	Turns = harness.Turns
	Write = harness.Write
	Bash  = harness.Bash
)

// Env is the harness environment, aliased so helpers here can take one.
type Env = harness.Env

// The engine's own per-session key, taken from the store rather than spelled
// again.
const metaBaselineCommit = sessionstate.MetaBaselineCommit

// TestMain removes the binary build dir when this package's tests finish.
func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}
