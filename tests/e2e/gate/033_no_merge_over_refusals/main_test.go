package e2e

import (
	"os"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The plugin's sloprail/gate/no-merge-over-refusals refuses `gh pr merge` while the
// branch being merged has open refusals recorded this session, judged at its tip.
// It ships on by default, so these tests install nothing: the plugin's own gate fires.
var (
	Turns = harness.Turns
	Bash  = harness.Bash
)

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}
