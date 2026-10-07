package e2e

import "testing"

// A guardrail whose check keeps a count of the writes it has seen, and refuses the
// second one. Counting is the smallest rule that cannot be expressed without
// memory: the first write and the second are identical events, and only what the
// rule stored tells them apart. SHARED machinery — the per-guard `sr-session state`
// keyspace the new dispatch reuses.
const countingGuard = `on:
  - event: PreFileWrite
    match: event.path startsWith "counted/"
checks:
  - script: ./count.sh
`

// The check reads its own count, increments it, writes it back, and refuses once
// the count says it has been here before.
//
// `sr-session` is found on PATH, which is itself part of what this proves: a check
// given no environment cannot resolve the binary the plugin was invoked as.
const countScript = `#!/bin/sh
cat >/dev/null
seen="$(sr-session state get writes)"
if [ -n "$seen" ]; then
  echo "already saw a write in this session (count=$seen)" >&2
  exit 1
fi
sr-session state set writes 1 || { echo "state set failed" >&2; exit 1; }
exit 0
`

// T008_05: a check can reach `sr-session state` at all.
//
// The narrowest statement of the invariant. The check writes one entry and exits
// zero; if the engine handed it no environment, `state set` exits non-zero, and a
// non-zero exit is never consent — so the write is refused and the failure is
// visible from outside as a refusal that no rule intended.
func TestT008_05_HookReachesItsState(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "counter", countingGuard, map[string]string{"count.sh": countScript})

	got := e.Run(proj, "s-008-05", "write a note", Turns("done",
		Write("w1", "counted/first.md", "hello"),
	))

	if got.Saw("state set failed") {
		t.Fatalf("the check could not write its state — the engine gave it no environment:\n%s", got.Output)
	}
	if got.Saw("no guardrail in scope") {
		t.Fatalf("SR_GUARDRAIL never reached the check:\n%s", got.Output)
	}
	// The first write in a fresh session must not be refused at pre-tool as a
	// repeat. (The stream carries the gate's pre-tool deny.)
	if got.Refused() {
		t.Fatalf("the first write in a fresh session was refused — its state read wrongly, or the environment was missing:\n%s", got.Output)
	}
}

// T008_06: what one write stored, the next write reads back.
//
// producers_hold_state as a user meets it. Two writes: the first is permitted and
// remembered, the second is refused *because* of what the first stored. Nothing
// about the two events differs — same tool, same match, same directory — so a
// refusal on the second and not the first can only have come from state that
// survived.
func TestT008_06_StateSurvivesAcrossWrites(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "counter", countingGuard, map[string]string{"count.sh": countScript})

	got := e.Run(proj, "s-008-06", "write two notes", Turns("done",
		Write("w1", "counted/first.md", "hello"),
		Write("w2", "counted/second.md", "hello again"),
	))

	if got.Saw("state set failed") || got.Saw("no guardrail in scope") {
		t.Fatalf("the check could not reach its state:\n%s", got.Output)
	}
	if !got.Saw("already saw a write in this session") {
		t.Fatalf("the second write was not recognised as a repeat — nothing survived the first:\n%s", got.Output)
	}
	// The count came back as the value that was stored, not merely as "something
	// non-empty". A store that returned any garbage would satisfy the check above.
	if !got.Saw("count=1") {
		t.Fatalf("the value read back was not the value written:\n%s", got.Output)
	}
}
