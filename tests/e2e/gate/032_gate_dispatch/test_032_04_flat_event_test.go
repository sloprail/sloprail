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

// A gate on the pre-write events is where a marker-scoped write is prevented. A
// create carries `newMarkers`; an update carries `oldMarkers` too, which is how a
// gate sees a write that REMOVES a marker (its `newMarkers` are empty, so a match
// on them alone never selects it). Each kind has its own fields, so each is its
// own trigger. The check echoes what it was handed and refuses.
const markerGate = `on:
  - event: PreFileCreate
    match: any(event.newMarkers, .kind == "invariant")
  - event: PreFileUpdate
    match: any(event.newMarkers, .kind == "invariant") or any(event.oldMarkers, .kind == "invariant")
checks:
  - script: ./check.sh
`

const echoesMarkers = `#!/bin/sh
payload="$(cat)"
kind="$(printf '%s' "$payload" | jq -r '.event.kind')"
known="$(printf '%s' "$payload" | jq -r '.event.resultKnown')"
newn="$(printf '%s' "$payload" | jq -r '(.event.newMarkers // []) | length')"
oldn="$(printf '%s' "$payload" | jq -r '(.event.oldMarkers // []) | length')"
printf '{"reason":"MARKERS kind=%s resultKnown=%s new=%s old=%s"}' "$kind" "$known" "$newn" "$oldn"
exit 1
`

// T032_12: a PreFileWrite gate sees `resultKnown`, `newMarkers` and `oldMarkers`.
// A create that carries a marker is selected and refused before it lands; an
// update that strips the marker is selected by `oldMarkers` and refused, the file
// unchanged; a write carrying no marker, on a file that held none, is not selected
// and lands.
func TestT032_12_PreWriteGateSeesMarkersAndResultKnown(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "src/pinned.go", "// sr:invariant refunds-capped\npackage src\n")
	e.WriteFile(proj, "src/plain.go", "package src\n")
	e.Gate(proj, "pinned", markerGate, map[string]string{"check.sh": echoesMarkers})
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "the project before the session")

	res := e.Run(proj, "s-032-12a", "add a pinned file", Turns("done",
		Write("w1", "src/new.go", "// sr:invariant new-rule\npackage src\n"),
	))
	if !res.Refused() || e.Exists(proj, "src/new.go") {
		t.Fatalf("a create carrying an invariant marker was not refused before it landed:\n%s", res.Output)
	}
	if !res.Saw("kind=PreFileCreate resultKnown=true new=1 old=0") {
		t.Errorf("the create's event did not carry resultKnown and newMarkers:\n%s", res.Output)
	}

	res = e.Run(proj, "s-032-12b", "strip the marker", Turns("done",
		Write("w2", "src/pinned.go", "package src\n"),
	))
	if !res.Refused() {
		t.Fatalf("an update that removes an invariant marker was not refused (oldMarkers must select it):\n%s", res.Output)
	}
	if !res.Saw("kind=PreFileUpdate resultKnown=true new=0 old=1") {
		t.Errorf("the update's event did not carry oldMarkers:\n%s", res.Output)
	}

	res = e.Run(proj, "s-032-12c", "edit a plain file", Turns("done",
		Write("w3", "src/plain.go", "package src // edited\n"),
	))
	if res.Refused() {
		t.Fatalf("a write with no marker on either side was refused:\n%s", res.Output)
	}
}
