package e2e

import (
	"testing"
)

// This file covers the whole point of a context: its state is READ by other
// rules. A context's active flag and payload are consulted by a gate's
// require:[{context}], and its active flag narrows a file-guard's match. This is
// what makes a context a scope other rules reason about rather than a thing unto
// itself.

// declareRefactorContext enters when a PreCommandInvoke names `refactor-start`,
// recording the declared scope into its payload. It enters on a PRE event, so it
// is active from that cycle onward — before the writes it governs.
const declareRefactorContext = `on:
  - event: PreCommandInvoke
    match: any(event.invocations, .bin == "refactor-start")
enter: ./enter.sh
exit: ./exit.sh
`

// enterDeclareScope writes a fixed scope payload — the context is declared active
// with a known scope a downstream rule reads.
const enterDeclareScope = `#!/bin/sh
cat >/dev/null
printf '{"scope":"src/","mode":"deterministic"}'
exit 0
`

// exitStayActive never says done — the context stays active for the rest of the
// session (a legitimate shape per the spec: "Some contexts never say done"). A
// context stays active by exiting NON-ZERO (the examples' convention: exit 0 =
// done/deactivate, non-zero = stay active).
const exitStayActive = `#!/bin/sh
cat >/dev/null
exit 1
`

// deterministicGate is a Stop gate that requires the refactor context to be
// active AND passes its check only if the context declared deterministic mode. It
// reads the context two ways: require:[{context}] (must be active) and a check
// reading context.<name>.payload.mode off the GateCheckPayload.
const deterministicGate = `on:
  - event: Stop
require:
  - context: refactor
checks:
  - script: ./check.sh
`

// checkReadsContextPayload passes only when the refactor context's payload says
// mode=deterministic — proving a gate's check reads a context's PAYLOAD, not just
// its active flag. It reads context.refactor.payload.mode off the GateCheckPayload
// on stdin.
const checkReadsContextPayload = `#!/bin/sh
payload="$(cat)"
mode="$(printf '%s' "$payload" | sed -n 's/.*"refactor":{"active":[^,]*,"payload":{[^}]*"mode":"\([^"]*\)".*/\1/p' | head -1)"
if [ "$mode" = "deterministic" ]; then
  exit 0
fi
echo '{"reason":"the refactor context did not declare deterministic mode"}'
exit 1
`

// T035_03: a gate's require:[{context}] reads a context's active flag, and its
// check reads the context's PAYLOAD.
//
// Cycle 1: a `refactor-start` command enters the context (a PreCommandInvoke, so
// it enters this cycle). The Stop gate requires the context active — it is — and
// its check reads the payload's mode=deterministic and passes. The turn ends
// clean, and the gate's verdict is pass.
func TestT035_03_GateReadsContextActiveAndPayload(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Context(proj, "refactor", declareRefactorContext, map[string]string{
		"enter.sh": enterDeclareScope,
		"exit.sh":  exitStayActive,
	})
	e.Gate(proj, "deterministic-only", deterministicGate, map[string]string{"check.sh": checkReadsContextPayload})
	e.CommitAll(proj, "the guards")

	sess := "s-035-03"
	res := e.Run(proj, sess, "declare a refactor", Turns("done",
		Bash("b1", "refactor-start src/"),
	))

	// The gate saw the context active and its payload, so it passed — no block.
	if len(e.BlockingErrorsFrom(proj, sess, "Stop")) != 0 {
		t.Errorf("a gate whose context require was met and whose check read the payload still blocked:\n%s", res.Output)
	}
	if status := e.GateState(proj, sess, "deterministic-only"); status != "pass" {
		t.Errorf("the gate recorded %q, want pass — it should have read the active context and its payload", status)
	}
	// The context is active (entered on the command).
	if active, payload := e.ContextState(proj, sess, "refactor"); !active || payload["mode"] != "deterministic" {
		t.Errorf("the context did not enter with the expected payload: active=%v payload=%v", active, payload)
	}
}

// T035_04: the SAME gate BLOCKS when the context never entered — its
// require:[{context}] is unmet.
//
// The control for T035_03: a cycle with no `refactor-start` command leaves the
// context inactive, so the gate's require fails and the turn is blocked. This
// proves the pass in T035_03 was the context being read, not the gate always
// passing.
func TestT035_04_GateBlocksWhenContextInactive(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Context(proj, "refactor", declareRefactorContext, map[string]string{
		"enter.sh": enterDeclareScope,
		"exit.sh":  exitStayActive,
	})
	e.Gate(proj, "deterministic-only", deterministicGate, map[string]string{"check.sh": checkReadsContextPayload})

	sess := "s-035-04"
	// No refactor-start command — the context never enters.
	e.Run(proj, sess, "just write something", Turns("done",
		Write("w1", "src/x.go", "package x"),
	))

	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) == 0 {
		t.Fatalf("the gate did not block though its required context never entered")
	}
	if active, _ := e.ContextState(proj, sess, "refactor"); active {
		t.Errorf("the context is active though nothing triggered its enter")
	}
}

// contextGatedFileGuard applies to src/ files ONLY while the refactor context is
// active — reading context["refactor"].active off its FileMatchScope. Outside the
// context, the guard does not select the file at all.
const contextGatedFileGuard = `match: path startsWith "src/" and context["refactor"].active
checks:
  - script: ./check.sh
`

// checkNoDebugPrint refuses a src/ file (already selected, so the context is
// active) that holds a debug print.
const checkNoDebugPrint = `#!/bin/sh
payload="$(cat)"
if printf '%s' "$payload" | grep -q 'DEBUG_PRINT'; then
  echo '{"reason":"no debug prints while refactoring"}'
  exit 1
fi
exit 0
`

// T035_05: a file-guard's match cannot read a context. A file-guard judges committed bytes
// at Stop, in CI too, where there is no session to hold a context's state, so a match that
// reads `context[...]` does not load, and the load error says where the condition belongs:
// in a gate.
//
// The rule loads in a project where the same session enters the context and commits a
// src/ file with a debug print; the guard is reported as not loaded, naming the gate as the
// remedy, rather than silently never applying.
func TestT035_05_FileGuardMatchReadsContext(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Context(proj, "refactor", declareRefactorContext, map[string]string{
		"enter.sh": enterDeclareScope,
		"exit.sh":  exitStayActive,
	})
	e.FileGuard(proj, "no-debug-in-refactor", contextGatedFileGuard, map[string]string{"check.sh": checkNoDebugPrint})
	e.CommitAll(proj, "the guards")

	sess := "s-035-05"
	e.Run(proj, sess, "refactor then leave a debug print", Turns("done",
		Bash("b1", "refactor-start src/"),
		Write("w1", "src/x.go", "DEBUG_PRINT here\npackage x"),
	).ThenCommit("leave a debug print"))

	r := e.CheckRunRaw(proj, sess, e.RunBase(sess), "HEAD")
	if !containsStr(r.Output, "not loaded") || !containsStr(r.Output, "cannot read `context`") || !containsStr(r.Output, "to a gate") {
		t.Fatalf("a file-guard whose match reads a context did not fail to load with the advice to move it to a gate:\n%s", r.Output)
	}
}

// T035_06: the SAME file-guard does NOT fire when the context is inactive — its
// match reads context["refactor"].active as false, so it does not select the file.
//
// The control for T035_05: the identical src/ debug-print write, but with no
// refactor-start command, leaves the context inactive, so the guard's match
// excludes the file and the turn is not blocked. Proves the guard's applicability
// is gated on the context.
func TestT035_06_FileGuardDoesNotFireOutsideContext(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Context(proj, "refactor", declareRefactorContext, map[string]string{
		"enter.sh": enterDeclareScope,
		"exit.sh":  exitStayActive,
	})
	e.FileGuard(proj, "no-debug-in-refactor", contextGatedFileGuard, map[string]string{"check.sh": checkNoDebugPrint})
	e.CommitAll(proj, "the guards")

	sess := "s-035-06"
	// No refactor-start — the context is inactive, so the guard's match excludes
	// the file even though it holds a debug print.
	e.Run(proj, sess, "leave a debug print outside any refactor", Turns("done",
		Write("w1", "src/x.go", "DEBUG_PRINT here\npackage x"),
	).ThenCommit("leave a debug print"))

	if len(e.BlockingErrorsFrom(proj, sess, "Stop")) != 0 {
		t.Errorf("a context-gated guard fired though its context was inactive — its match should exclude the file")
	}
}
