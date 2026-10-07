package e2e

import (
	"testing"
)

// This file covers a gate bound to Stop — the end-of-cycle checkpoint — and that a
// gate's verdict lands in the gates[] map a context will read next slice. A Stop
// gate blocks the TURN (not a single tool call) on a refusal, which is a different
// channel from a pre-tool deny; the harness reads it from the record.

// stopGateFail is a Stop gate whose check always refuses — a "don't stop until X"
// rule that is never satisfied here.
const stopGateFail = `on:
  - event: Stop
checks:
  - script: ./verify.sh
`

const verifyFail = `#!/bin/sh
cat >/dev/null
echo "the cycle did not meet the completion requirement" >&2
exit 1
`

const verifyPass = `#!/bin/sh
cat >/dev/null
exit 0
`

// T032_06: a Stop gate whose check refuses BLOCKS the turn.
//
// The gate wakes on Stop (the moment the cycle ends), its check fails, and the turn
// is blocked — the mechanism a "don't stop until done" rule uses to send the agent
// round again. The refusal arrives as a blocking error on the record, which is
// where a Stop refusal's text lands (not the result stream).
// sr:proves gates/stop-refusal-continues-the-turn
func TestT032_06_StopGateBlocksTheTurn(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj) // a Stop dispatch establishes a baseline; give it a repo
	e.Gate(proj, "must-finish", stopGateFail, map[string]string{"verify.sh": verifyFail})

	e.Run(proj, "s-032-06", "do some work then stop", Turns("done",
		Write("w1", "note.txt", "some work"),
	))

	blocks := e.BlockingErrorsFrom(proj, "s-032-06", "Stop")
	if len(blocks) == 0 {
		t.Fatalf("a Stop gate whose check failed did not block the turn")
	}
	joined := ""
	for _, b := range blocks {
		joined += b + "\n"
	}
	if !containsStr(joined, "completion requirement") {
		t.Errorf("the Stop gate's refusal did not carry its reason:\n%s", joined)
	}
	if !containsStr(joined, "must-finish") {
		t.Errorf("the Stop gate's refusal did not name the gate:\n%s", joined)
	}
}

// T032_07: a gate's verdict LANDS in the gates[] map — pass when it admits, fail
// when it blocks.
//
// This is what a context reads next slice: `gates[<name>].status`. A Stop gate that
// passes records `pass`; one that fails records `fail`. Read straight from the
// session store, so a broken persistence path cannot pass this.
func TestT032_07_GateVerdictLandsInGatesMap(t *testing.T) {
	// A passing Stop gate records pass.
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "checkpoint", stopGateFail, map[string]string{"verify.sh": verifyPass})

	e.Run(proj, "s-032-07a", "work then stop", Turns("done",
		Write("w1", "note.txt", "work"),
	))
	if status := e.GateState(proj, "s-032-07a", "checkpoint"); status != "pass" {
		t.Errorf("a passing gate's verdict was %q, want \"pass\"", status)
	}

	// A failing Stop gate records fail.
	e2 := New(t)
	proj2 := e2.Project()
	e2.GitInit(proj2)
	e2.Gate(proj2, "checkpoint", stopGateFail, map[string]string{"verify.sh": verifyFail})

	e2.Run(proj2, "s-032-07b", "work then stop", Turns("done",
		Write("w1", "note.txt", "work"),
	))
	if status := e2.GateState(proj2, "s-032-07b", "checkpoint"); status != "fail" {
		t.Errorf("a failing gate's verdict was %q, want \"fail\"", status)
	}
}

// containsStr is a tiny substring helper kept local so the test file needs no
// import beyond testing.
func containsStr(haystack, needle string) bool {
	return len(needle) == 0 || indexOf(haystack, needle) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
