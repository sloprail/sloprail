package e2e

import (
	"testing"
)

// This file proves the RUNTIME payload a check-script receives on stdin carries the
// event FLAT — `.event.path`, `.event.newContent`, `.event.kind` directly, NOT under
// `.event.fields`. The unit tests pin the assembler's shape; this drives a real
// script through the mock so the whole path (dispatch -> check-runner -> stdin ->
// jq) is shown to deliver the flat event the next slice's file-guard and context
// scripts all read.

// readsFlatEvent is a check that reads the event FLAT off stdin and refuses with the
// values it found — so the test can confirm, off the wire, that `.event.path` and
// `.event.newContent` resolve (and that `.event.fields` does NOT hold the path).
const readsFlatEvent = `#!/bin/sh
payload="$(cat)"
path="$(printf '%s' "$payload" | jq -r '.event.path // "MISSING"')"
content="$(printf '%s' "$payload" | jq -r '.event.newContent // "MISSING"')"
kind="$(printf '%s' "$payload" | jq -r '.event.kind // "MISSING"')"
# The old nested envelope would put the path here; it must be absent.
nested="$(printf '%s' "$payload" | jq -r '.event.fields.path // "ABSENT"')"
printf '{"reason":"FLAT path=%s content=%s kind=%s nested=%s"}' "$path" "$content" "$kind" "$nested"
exit 1
`

const gateReadsFlat = `on:
  - event: PreFileWrite
    match: event.path startsWith "memories/"
checks:
  - script: ./check.sh
`

// T032_10: a check-script reads the event FLAT off its stdin.
//
// The gate fires on a write under memories/, its check reads `.event.path`,
// `.event.newContent`, `.event.kind` directly, and refuses echoing them back. The
// refusal reaching the agent carries the values — proving the runtime payload's
// `event` is flat, and that `.event.fields.path` is ABSENT (the old nested shape is
// gone). This is the exact shape every file-guard/context script in the next slice
// depends on.
func TestT032_10_CheckScriptReadsEventFlat(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Gate(proj, "reads-flat", gateReadsFlat, map[string]string{"check.sh": readsFlatEvent})

	res := e.Run(proj, "s-032-10", "write a memory", Turns("done",
		Write("w1", "memories/note.md", "the body text"),
	))

	if !res.Refused() {
		t.Fatalf("the gate did not fire / block:\n%s", res.Output)
	}
	// The flat fields resolved on the wire.
	if !res.Saw("path=memories/note.md") {
		t.Errorf(".event.path did not resolve FLAT on the check's stdin:\n%s", res.Output)
	}
	if !res.Saw("content=the body text") {
		t.Errorf(".event.newContent did not resolve FLAT on the check's stdin:\n%s", res.Output)
	}
	if !res.Saw("kind=PreFileCreate") {
		t.Errorf(".event.kind did not resolve FLAT on the check's stdin:\n%s", res.Output)
	}
	// And the nested envelope is gone: .event.fields.path must be absent.
	if !res.Saw("nested=ABSENT") {
		t.Errorf(".event.fields.path was present — the runtime event is still nested:\n%s", res.Output)
	}
}
