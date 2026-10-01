package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// stop_order: at Stop the order is context ENTERS -> commit-required ->
// file-guards -> gates -> context EXITS. A rule that reads context[] therefore
// sees the contexts this turn entered (and the ones exiting at this very Stop),
// never the previous turn's state.
//
// Run as `env -u CLAUDECODE -u CLAUDE_CODE_SESSION_ID go test ...`.

type Env = harness.Env

var (
	Turns = harness.Turns
	Write = harness.Write
)

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

// New is harness.New with the plugin's authoring file-guards switched off: this
// package is about other rules.
func New(t *testing.T) *Env { return harness.New(t, harness.WithoutShippedFileGuards()) }

// modeContext activates when trigger.txt is written (a PostFileWrite), in the
// same Stop. exit is the given script body's exit code: 1 keeps it active, 0
// closes it at that Stop.
func modeContext(exitCode string) (string, map[string]string) {
	return "on:\n  - event: PostFileWrite\n    match: event.path == \"trigger.txt\"\nenter: ./enter.sh\nexit: ./exit.sh\n",
		map[string]string{
			"enter.sh": "#!/bin/sh\ncat >/dev/null\nprintf '{}'\nexit 0\n",
			"exit.sh":  "#!/bin/sh\ncat >/dev/null\nexit " + exitCode + "\n",
		}
}

// scopedGuard judges src/ only while the `mode` context is active. Its check
// records a line per run in the ledger and refuses text containing FORBIDDEN.
const scopedMatch = "match: path startsWith \"src/\" and context[\"mode\"].active\nchecks:\n  - script: ./check.sh\n"

func scopedCheck(ledger string) string {
	return `#!/bin/sh
payload="$(cat)"
echo ran >> ` + ledger + `
if printf '%s' "$payload" | jq -e 'any(.changeset.files[]; .newContent | contains("FORBIDDEN"))' >/dev/null; then
  echo '{"reason":"SCOPED-GUARD refused FORBIDDEN text"}'
  exit 1
fi
exit 0
`
}

// stopGate requires the mode context and refuses until approved.txt exists.
const stopGate = "on:\n  - event: Stop\nrequire:\n  - context: mode\nchecks:\n  - script: ./check.sh\n"

const stopGateCheck = `#!/bin/sh
cat >/dev/null
if [ -f "$SR_WORKSPACE/approved.txt" ]; then exit 0; fi
echo '{"reason":"STOP-GATE-RAN: approved.txt is missing"}'
exit 1
`

// setup is a committed project with the mode context (exiting with exitCode at
// Stop) and the scoped file-guard, plus the Stop gate when withGate.
func setup(t *testing.T, exitCode string, withGate bool) (*Env, string, string) {
	t.Helper()
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "README.md", "readme\n")
	e.CommitAll(proj, "the project")
	led := filepath.Join(t.TempDir(), "ledger")
	y, files := modeContext(exitCode)
	e.Context(proj, "mode", y, files)
	e.FileGuard(proj, "scoped", scopedMatch, map[string]string{"check.sh": scopedCheck(led)})
	if withGate {
		e.Gate(proj, "stop-gate", stopGate, map[string]string{"check.sh": stopGateCheck})
	}
	e.CommitAll(proj, "the rules")
	return e, proj, led
}

func ranCount(t *testing.T, ledger string) int {
	t.Helper()
	b, err := os.ReadFile(ledger)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	return strings.Count(string(b), "ran")
}

func joined(errs []string) string { return strings.Join(errs, "\n") }
