package e2e

import (
	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The agent is a10n-claude-mock with this repo's plugin enabled, so what fires
// during a test is the wiring a user would get. Nothing here invokes a
// subcommand: the session begins, the harness fires SessionStart, the plugin
// reaches `sr-session start`, and what lands in the session's own record
// is what a real session would have left.
var (
	New   = harness.New
	Turns = harness.Turns
	Bash  = harness.Bash
	Write = harness.Write
)

// The engine's own per-session keys, taken from the store rather than spelled
// again — a second copy would keep agreeing with itself while the real one
// moved.
const (
	metaBaselineCommit = sessionstate.MetaBaselineCommit
	metaBaselineBranch = sessionstate.MetaBaselineBranch
	metaTranscriptRead = sessionstate.MetaTranscriptRead
)
