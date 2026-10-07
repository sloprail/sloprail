package e2e

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The agent is a10n-claude-mock with this repo's plugin enabled, so what fires
// during a test is the wiring a user would get.
var (
	New   = harness.New
	Turns = harness.Turns
	Write = harness.Write
	Bash  = harness.Bash
)

// TestMain removes the binary build dir when this package's tests finish.
// Without it every e2e package leaks 15M for the life of the machine.
func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

// seeEveryCreate is a GATE that records what each pending write was reported as,
// and permits.
//
// The spelling a rule is handed is the spelling its match narrows on, and this
// suite reads that spelling off the payload. The old observer bound PreFileCreate
// unnarrowed and recorded `.event.fields.path`; the new observer is a gate
// triggering on the pre-write events (PreFileCreate + PreFileUpdate) with NO
// match, so it fires once per write regardless of path — including an
// extensionless one like id_rsa and one whose target escapes the repository — and
// its check records `.event.path` FLAT.
//
// A gate rather than a file-guard, deliberately. A file-guard is bound to a FILE'S
// STATE and its match is a FileMatchScope over the tree; the file-guard dispatch
// does not fire for a path OUTSIDE the workspace (an absolute path in a temp dir,
// a write through a repository-escaping symlink) — which is exactly the case this
// suite must observe the reported ABSOLUTE spelling of. A gate triggers on the
// pre-action event the module emits for the write itself, so it sees every write
// the harness announces and is handed the same `reportable` path the old hook was.
// A gate is also one-shot per event, so one write records exactly one line — the
// `require.Len(..., 1)` this suite rests on.
const seeEveryCreate = `on:
  - event: PreFileCreate
  - event: PreFileUpdate
checks:
  - script: ./h.sh
`

// recordPayload writes the raw FLAT payload, one JSON document per line, into the
// gate's own folder ($SR_GUARDRAIL_DIR).
const recordPayload = `#!/bin/sh
p="$(cat)"
printf '%s\n' "$p" >> "$SR_GUARDRAIL_DIR/log"
exit 0
`

// pathsSeen reads the `path` field of every event a check recorded, in order.
//
// The event's own fields are spread FLAT under `event` (`.event.path`), NOT nested
// under an `event.fields` envelope the way the old format wrote them.
func pathsSeen(t *testing.T, e *harness.Env, proj, gateName string) []string {
	t.Helper()
	var out []string
	for _, line := range e.GateLedgerLines(proj, gateName, "log") {
		var got struct {
			Event struct {
				Path string `json:"path"`
			} `json:"event"`
		}
		require.NoError(t, json.Unmarshal([]byte(line), &got),
			"the check was handed something that is not a valid event payload: %s", line)
		out = append(out, got.Event.Path)
	}
	return out
}
