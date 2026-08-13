package e2e

import "github.com/sloprail/sloprail/tests/e2e/harness"

// The agent is a10n-claude-mock with this repo's plugin enabled, so what fires
// during a test is the wiring a user would get.
var (
	New   = harness.New
	Turns = harness.Turns
	Write = harness.Write
)
