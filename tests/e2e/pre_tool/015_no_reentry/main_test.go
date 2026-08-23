package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The re-entry suite, on the NEW nature format.
//
// The property under test is the engine's re-entry guard: a rule whose CHECK
// launches sr-agent must not re-fire ON ITSELF inside the agent it launched, or
// it recurses. The mechanism is the env var SLOPRAIL_LAUNCHED_BY — the engine
// records the launching rule's name in it, the value is inherited across the exec
// into the launched agent's own hooks, and the dispatch one level down declines to
// enforce the rules named there (and only those).
//
// The VEHICLE is a PREVENTIVE FILE-GUARD. The old format expressed the same shape
// as a `hooks: PreFileCreate` guardrail whose command shelled sr-agent; the
// new-format equivalent that fires at pre-tool on a would-be write is a file-guard
// with `preventive: true` and a `script:` check that launches the agent. A
// preventive guard runs the check-runner on the PRE file event, so the check runs
// through the new dispatch (services/sr-session runFileGuardsPreventive →
// internal/dispatch Runner.Run → runScriptExec), which is the path that had to
// learn to set SLOPRAIL_LAUNCHED_BY. The check `exit 0`s (it only launches and
// permits), so the outer write lands and the launched agent's own writes fire the
// guards again — the recursion this suite bounds.
//
// Nothing here calls sloprail directly. The agent writes, the harness fires
// PreToolUse, the plugin reaches our subcommand, the check launches sr-agent, and
// sr-agent execs a `claude` the harness shimmed to the mock — the same wiring a
// user installing this would get, with only the harness binary substituted.
var (
	New   = harness.New
	Turns = harness.Turns
	Write = harness.Write
)

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

// fileGuardLedgerLines reads, as lines, the ledger a file-guard's check appended
// to inside the guard's own folder (`.sloprail/file-guard/<name>/<file>`).
//
// The check runs with its working directory set to the guard's folder — the same
// place an old-format hook ran from — so a line appended there is the one channel
// that records a run without the engine's cooperation and without a test reaching
// inside the binary. An absent file is a real answer: nothing ran. The lines-
// returning analogue of the harness's FileGuardLedger (which only counts), needed
// because the recursion assertions read the actual carried depth off each line.
func fileGuardLedgerLines(t *testing.T, projDir, name, file string) []string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(projDir, ".sloprail", "file-guard", name, file))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("harness: read file-guard ledger %s/%s: %v", name, file, err)
	}
	var lines []string
	for _, l := range strings.Split(string(body), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	return lines
}
