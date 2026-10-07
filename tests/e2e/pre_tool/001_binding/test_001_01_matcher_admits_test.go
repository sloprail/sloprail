package e2e

import "testing"

// binding: which events reach a bound guardrail — a write in-scope fires it, a
// write out-of-scope does not.
//
// # Vehicle: a PreFileWrite gate
//
// This directory tests a PRE-TOOL DISPATCH INVARIANT: a rule's binding decides
// which occurrences reach it. The vehicle is a gate on PreFileWrite — prevention
// is a gate's job, a file-guard judges only the settled result at Stop. The gate's
// binding is its trigger `match`: `event.path startsWith "guarded/"` selects
// `guarded/notes.md` and not `elsewhere/notes.md`, which is exactly the in-scope /
// out-of-scope split this directory measures. A refusal denies at pre-tool, and
// travels back on the stream, read with res.Saw / res.Refused.

// refuseUnderGuarded is a gate bound to writes under guarded/. Its check refuses whatever it is shown — so what these tests prove is
// which writes reach it, not what it decides once they do.
//
// Unconditional on purpose: a test about binding should fail when the wrong event
// arrives, not when the right one is judged differently. The refusal contract is
// the new one — a `{"reason": …}` on stdout and a non-zero exit — replacing the
// old `{"decision":"block"}` + exit 1.
const refuseUnderGuarded = `on:
  - event: PreFileWrite
    match: event.path startsWith "guarded/"
checks:
  - script: ./refuse.sh
`

const refuseScript = `#!/bin/sh
cat >/dev/null
echo '{"reason":"this path is guarded"}'
exit 1
`

// T001_01: the agent tries to write where a rule is bound, and is refused.
//
// The positive half of binding. Nothing here calls sloprail: the agent writes,
// the harness fires its PreToolUse hook, the plugin reaches the new nature
// dispatch, the gate matches, and the refusal travels back through
// the tool result.
// sr:proves matching/scope-reads-its-own-facts
func TestT001_01_MatcherAdmitsWrite(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "guarded-dir", refuseUnderGuarded, map[string]string{"refuse.sh": refuseScript})

	got := e.Run(proj, "s-001-01", "write a note", Turns("done",
		Write("w1", "guarded/notes.md", "hello"),
	))

	if !got.Saw("this path is guarded") {
		t.Fatalf("want the refusal to reach the agent, got:\n%s", got.Output)
	}
	if !got.Refused() {
		t.Fatalf("the in-scope write was not refused:\n%s", got.Output)
	}
}

// T001_02: the same agent writing outside the binding is left alone.
//
// The negative half, and the one that catches a match that admits everything — a
// guardrail refusing every write looks identical to a correct one until something
// outside its scope is tried. `elsewhere/notes.md` is not under `guarded/**`, so
// the guard must not match it.
// sr:proves matching/scope-reads-its-own-facts
func TestT001_02_MatcherRejectsOtherPath(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "guarded-dir", refuseUnderGuarded, map[string]string{"refuse.sh": refuseScript})

	got := e.Run(proj, "s-001-02", "write a note", Turns("done",
		Write("w1", "elsewhere/notes.md", "hello"),
	))

	if got.Saw("this path is guarded") {
		t.Fatalf("a path outside the binding was refused:\n%s", got.Output)
	}
	if got.Refused() {
		t.Fatalf("a path outside the binding was refused:\n%s", got.Output)
	}
}

// T001_03: a project that declares nothing sees no difference.
//
// The engine has no opinion of its own. Every refusal traces to something a
// project declared, and one that declared nothing should be indistinguishable
// from not having installed this.
func TestT001_03_NoDeclarationsPermits(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	got := e.Run(proj, "s-001-03", "write a note", Turns("done",
		Write("w1", "guarded/notes.md", "hello"),
	))

	if got.Refused() {
		t.Fatalf("a project with no guardrails saw a refusal:\n%s", got.Output)
	}
}
