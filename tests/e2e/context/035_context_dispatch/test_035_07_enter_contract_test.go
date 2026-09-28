package e2e

import (
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// This file pins what an `enter` script's exit code and stdout mean (issue #90).
// The authoring doc once said an enter that exits 0 with no output does NOT
// activate the context; the engine activates it, carrying over the payload it
// already had, and only a NON-ZERO exit declines. The engine is the contract —
// it is what the shipped examples are written against — so these tests pin it:
//
//   - exit 0, a JSON object on stdout → active; the object replaces the payload;
//   - exit 0, nothing on stdout       → active; the payload is kept (none, the
//     first time) — a silent clean exit is NOT a decline;
//   - non-zero                        → declined; the context is left as it was,
//     inactive or active with its payload.
//
// And a context's `exit` never refuses the Stop: every session below ends with
// the context's exit saying "not done" (non-zero), and no Stop is blocked.

// pathContext enters on every Write, handing enter the tool call.
const pathContext = `on:
  - event: PreToolUse
    match: event.tool == "Write"
enter: ./enter.sh
exit: ./exit.sh
`

// enterByPath decides by the file the Write targets, so one session can drive
// all three outcomes in order:
//
//	payload.md → exit 0 and print {"from":"payload.md"}
//	silent.md  → exit 0 and print nothing
//	decline.md → exit 1
const enterByPath = `#!/bin/sh
input="$(cat)"
path="$(printf '%s' "$input" | jq -r '.event.input.file_path // ""')"
case "$path" in
  *payload.md) printf '{"from":"payload.md"}'; exit 0 ;;
  *silent.md)  exit 0 ;;
  *)           exit 1 ;;
esac
`

// pathContextProject is a committed project with pathContext installed and an
// exit that never says done.
func pathContextProject(t *testing.T) (*harness.Env, string) {
	t.Helper()
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Context(proj, "by-path", pathContext, map[string]string{
		"enter.sh": enterByPath,
		"exit.sh":  exitNever,
	})
	commitGuards(t, proj)
	return e, proj
}

// T035_15: an enter that exits 0 and prints NOTHING activates an inactive
// context — the first trigger, so there is no payload to keep and the payload is
// empty. A silent clean exit is not a "no".
func TestT035_15_SilentCleanEnterActivates(t *testing.T) {
	e, proj := pathContextProject(t)

	sess := "s-035-15"
	e.Run(proj, sess, "write one file", Turns("done",
		Write("w1", "silent.md", "one"),
	))

	active, payload := e.ContextState(proj, sess, "by-path")
	if !active {
		t.Fatalf("an enter that exited 0 with no output did not activate the context")
	}
	if len(payload) != 0 {
		t.Errorf("a first activation with no output should carry no payload, got %v", payload)
	}
	if blocks := e.BlockingErrorsFrom(proj, sess, "Stop"); len(blocks) != 0 {
		t.Errorf("a context's exit saying not-done refused the Stop — exit never blocks:\n%v", blocks)
	}
}

// T035_16: a silent clean enter on an ACTIVE context keeps the payload the last
// printing enter set — it does not clear it, and does not deactivate.
func TestT035_16_SilentCleanEnterKeepsThePayload(t *testing.T) {
	e, proj := pathContextProject(t)

	sess := "s-035-16"
	e.Run(proj, sess, "write two files", Turns("done",
		Write("w1", "payload.md", "one"),
		Write("w2", "silent.md", "two"),
	))

	active, payload := e.ContextState(proj, sess, "by-path")
	if !active {
		t.Fatalf("the context is not active after a printing enter and a silent one")
	}
	if payload["from"] != "payload.md" {
		t.Errorf("a silent clean enter did not keep the previous payload: %v", payload)
	}
	if blocks := e.BlockingErrorsFrom(proj, sess, "Stop"); len(blocks) != 0 {
		t.Errorf("a context's exit saying not-done refused the Stop — exit never blocks:\n%v", blocks)
	}
}

// T035_17: a NON-ZERO enter declines — an inactive context stays inactive.
// The control for T035_15: the only difference is the exit code.
func TestT035_17_NonZeroEnterDeclines(t *testing.T) {
	e, proj := pathContextProject(t)

	sess := "s-035-17"
	e.Run(proj, sess, "write one file", Turns("done",
		Write("w1", "decline.md", "one"),
	))

	if active, payload := e.ContextState(proj, sess, "by-path"); active {
		t.Errorf("an enter that exited non-zero activated the context (payload %v)", payload)
	}
}

// T035_18: a NON-ZERO enter on an ACTIVE context leaves it exactly as it was —
// still active, payload intact. Declining is not `exit` saying done.
func TestT035_18_NonZeroEnterLeavesAnActiveContextAsItWas(t *testing.T) {
	e, proj := pathContextProject(t)

	sess := "s-035-18"
	e.Run(proj, sess, "write two files", Turns("done",
		Write("w1", "payload.md", "one"),
		Write("w2", "decline.md", "two"),
	))

	active, payload := e.ContextState(proj, sess, "by-path")
	if !active {
		t.Fatalf("a declining enter deactivated an active context")
	}
	if payload["from"] != "payload.md" {
		t.Errorf("a declining enter changed the payload: %v", payload)
	}
}
