package e2e

import (
	"fmt"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T060_01: a wait loop that pgreps its own command line never ends, so it is refused with the
// way forward; a wait on the command's own PID and a pgrep that cannot match itself are not.
func TestT060_01_SelfMatchingPgrepIsRefusedAndOthersAreNot(t *testing.T) {
	e := harness.New(t, harness.WithoutShippedFileGuards())
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "seed.md", "seed\n")
	e.CommitAll(proj, "the project")

	refused := []string{
		`until ! pgrep -f "sr-checks run" >/dev/null; do sleep 5; done; echo finished`,
		`while pgrep -f "sr-checks run" >/dev/null; do sleep 5; done`,
		`while true; do pkill -f "sr-checks run"; sleep 1; done`,
		`pgrep -fl "wait-for-me" && echo wait-for-me`,
		`pgrep --full "wait-for-me"; echo wait-for-me`,
	}
	for i, cmd := range refused {
		res := e.Run(proj, "s-060-01", "wait", Turns("done", Bash(fmt.Sprintf("r%d", i), cmd)))
		if !res.Refused() || !res.Saw("no-self-matching-pgrep") || !res.Saw("wait $pid") {
			t.Errorf("%q was not refused with the way forward:\n%s", cmd, res.Output)
		}
	}

	allowed := []string{
		`sleep 1 & wait $!`,
		`pgrep -f nginx`,
		`pgrep -x nginx`,
		`pgrep nginx`,
		`pgrep -f nginx; echo done`,
	}
	for i, cmd := range allowed {
		res := e.Run(proj, "s-060-01", "check", Turns("done", Bash(fmt.Sprintf("a%d", i), cmd)))
		if res.Saw("no-self-matching-pgrep") {
			t.Errorf("%q cannot match itself but was refused:\n%s", cmd, res.Output)
		}
	}
}
