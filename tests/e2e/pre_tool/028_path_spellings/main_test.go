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

// seeEveryCreate records what each pending creation was reported as, and
// permits. Unnarrowed, so which events arrive is the engine's answer rather
// than a matcher's.
const seeEveryCreate = `---
hooks:
  PreFileCreate:
    - hooks:
        - type: command
          command: ./h.sh
---

# Records the path of every creation it is shown
`

// recordPayload writes the raw payload, one JSON document per line.
const recordPayload = `#!/bin/sh
p="$(cat)"
printf '%s\n' "$p" >> "$PWD/log"
exit 0
`

// pathsSeen reads the `path` field of every event a hook recorded, in order.
func pathsSeen(t *testing.T, e *harness.Env, proj, guardrail string) []string {
	t.Helper()
	var out []string
	for _, line := range e.Ledger(proj, guardrail, "log") {
		var got struct {
			Event struct {
				Fields struct {
					Path string `json:"path"`
				} `json:"fields"`
			} `json:"event"`
		}
		require.NoError(t, json.Unmarshal([]byte(line), &got),
			"the hook was handed something that is not a valid event payload: %s", line)
		out = append(out, got.Event.Fields.Path)
	}
	return out
}
