package e2e

import (
	"testing"
)

// This file covers a context's core lifecycle: it ENTERS on its trigger (its
// state shows active with the payload its enter extracted), and its EXIT runs at
// Stop and flips active WITHOUT blocking the turn.

// refactorContext enters when a file under src/ is written, recording the touched
// path into its payload. Its exit says done (non-zero) only once a DONE marker
// file exists — otherwise it stays active. Neither enter nor exit blocks anything.
const refactorContext = `on:
  - event: PostFileWrite
    match: event.path startsWith "src/"
enter: ./enter.sh
exit: ./exit.sh
`

// enterRecordScope reads the ContextEnterPayload and writes a payload naming the
// file that was touched — a flat JSON object that REPLACES the context's payload.
const enterRecordScope = `#!/bin/sh
payload="$(cat)"
path="$(printf '%s' "$payload" | sed -n 's/.*"path":"\([^"]*\)".*/\1/p' | head -1)"
printf '{"scope":"src/","last":"%s"}' "$path"
exit 0
`

// exitDoneWhenMarker exits ZERO (done — deactivate) once a DONE file exists at the
// project root, else exits non-zero (stay active). This is the examples'
// convention: exit 0 = "yes, done". exit only flips active/inactive; it never
// blocks the Stop. The project root is $SR_GUARDRAIL_DIR/../../.. (the context
// folder is <proj>/.sloprail/context/<name>).
const exitDoneWhenMarker = `#!/bin/sh
cat >/dev/null
if [ -f "$SR_GUARDRAIL_DIR/../../../DONE" ]; then
  exit 0
fi
exit 1
`

// T035_01: a context ENTERS on its trigger — its state shows active with the
// payload enter extracted — and a later cycle can read it.
//
// The write under src/ is a Post event, so enter runs at Stop (after the write
// settles). The context's state then shows active, carrying the scope its enter
// wrote. Nothing is blocked — a context does not block.
func TestT035_01_ContextEntersOnTrigger(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Context(proj, "refactor", refactorContext, map[string]string{
		"enter.sh": enterRecordScope,
		"exit.sh":  exitDoneWhenMarker,
	})
	e.CommitAll(proj, "the guards")

	sess := "s-035-01"
	res := e.Run(proj, sess, "touch a source file", Turns("done",
		Write("w1", "src/model.go", "package model"),
	))

	// A context never blocks the turn.
	if res.Refused() {
		t.Errorf("a context entering blocked the turn — a context must not block:\n%s", res.Output)
	}
	if len(e.BlockingErrorsFrom(proj, sess, "Stop")) != 0 {
		t.Errorf("a context's enter/exit blocked the Stop — only a gate may")
	}

	// The context entered: active, with the payload its enter wrote.
	active, payload := e.ContextState(proj, sess, "refactor")
	if !active {
		t.Fatalf("the context did not enter on its trigger (state not active)")
	}
	if payload["scope"] != "src/" {
		t.Errorf("the context's payload was not the one enter wrote: %v", payload)
	}
	if payload["last"] != "src/model.go" {
		t.Errorf("enter did not record the touched path into the payload: %v", payload)
	}
}

// T035_02: a context's EXIT runs at Stop and does NOT block it, and it flips
// active to inactive when it says done — the reversal (a context's exit is pure
// lifecycle; a gate blocks, not a context).
//
// Cycle 1 enters the context (no DONE marker, so exit keeps it active). Cycle 2
// creates the DONE marker; exit then says done and the context goes inactive —
// the turn still ends cleanly.
// sr:proves contexts/exit-only-deactivates
func TestT035_02_ContextExitDoesNotBlockAndFlipsActive(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Context(proj, "refactor", refactorContext, map[string]string{
		"enter.sh": enterRecordScope,
		"exit.sh":  exitDoneWhenMarker,
	})
	e.CommitAll(proj, "the guards")

	sess := "s-035-02"

	// Cycle 1: enter, exit keeps it active (no DONE yet).
	e.Run(proj, sess, "start the refactor", Turns("done",
		Write("w1", "src/a.go", "package a"),
	))
	active, _ := e.ContextState(proj, sess, "refactor")
	if !active {
		t.Fatalf("the context should be active after entering with no done marker")
	}

	// Cycle 2: create the DONE marker. exit says done — the context goes inactive,
	// and the Stop is NOT blocked.
	res := e.Run(proj, sess, "finish the refactor", Turns("done",
		Write("w2", "DONE", "finished"),
	))
	if res.Refused() {
		t.Errorf("the context's exit blocked the turn — exit is pure lifecycle:\n%s", res.Output)
	}
	if len(e.BlockingErrorsFrom(proj, sess, "Stop")) != 0 {
		t.Errorf("the context's exit contributed a Stop block — the reversal says it must not")
	}
	active, _ = e.ContextState(proj, sess, "refactor")
	if active {
		t.Errorf("the context's exit said done but the context is still active")
	}
}
