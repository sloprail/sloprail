package e2e

import "github.com/sloprail/sloprail/tests/e2e/harness"

// `init` and `guardrail help` are typed rather than invoked by a harness, so
// these drive the binary directly. Everything a session triggers is tested
// through the mock instead — see tests/e2e/pre_tool.
var (
	New   = harness.New
	Turns = harness.Turns
	Write = harness.Write
)
