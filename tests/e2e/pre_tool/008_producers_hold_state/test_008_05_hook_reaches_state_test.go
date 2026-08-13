package e2e

import "testing"

// A guardrail whose hook keeps a count of the writes it has seen, and refuses
// the second one by name. Counting is the smallest rule that cannot be
// expressed without memory: the first write and the second are identical
// events, and only what the rule stored tells them apart.
const countingGuardrail = `---
hooks:
  PreFileCreate:
    - matcher: path startsWith "counted/"
      hooks:
        - type: command
          command: ./count.sh
---

# Refuses the second write it sees

The verdict depends on what the previous cycle stored, so a hook that cannot
reach its state cannot reach the refusal at all.
`

// The hook reads its own count, increments it, writes it back, and refuses once
// the count says it has been here before.
//
// `sloprail` is found on PATH, which is itself part of what this proves: a hook
// given no environment cannot resolve the binary the plugin was invoked as.
const countScript = `#!/bin/sh
cat >/dev/null
seen="$(sloprail session state get writes)"
if [ -n "$seen" ]; then
  echo "already saw a write in this session (count=$seen)" >&2
  exit 1
fi
sloprail session state set writes 1 || { echo "state set failed" >&2; exit 1; }
exit 0
`

// T008_05: a hook can reach `sloprail session state` at all.
//
// The narrowest statement of the bug this closes. The hook writes one entry and
// exits zero; if the engine handed it no environment, `session state set` exits
// non-zero, and a non-zero exit is never consent — so the write is refused and
// the failure is visible from outside as a refusal that no rule intended.
//
// Numbered after the ids this package already held. T008_01 and T008_02 are the
// dispatcher and scope tests this branch met on the base; the brief's premise
// that T008_02 did not exist was true when it was written and stopped being true
// at the rebase.
func TestT008_05_HookReachesItsState(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "counter", countingGuardrail, map[string]string{"count.sh": countScript})

	got := e.Run(proj, "s-008-05", "write a note", Turns("done",
		Write("w1", "counted/first.md", "hello"),
	))

	if got.Saw("state set failed") {
		t.Fatalf("the hook could not write its state — the engine gave it no environment:\n%s", got.Output)
	}
	if got.Saw("no guardrail in scope") {
		t.Fatalf("SR_GUARDRAIL never reached the hook:\n%s", got.Output)
	}
	if got.Saw("already saw a write") {
		t.Fatalf("the first write in a fresh session was treated as a repeat:\n%s", got.Output)
	}
}

// T008_06: what one cycle stored, the next cycle reads back.
//
// This is producers_hold_state as a user meets it. Two writes, one session: the
// first is permitted and remembered, the second is refused *because* of what the
// first stored. Nothing about the two events differs — same tool, same matcher,
// same directory — so a refusal on the second and not the first can only have
// come from state that survived the cycle boundary.
func TestT008_06_StateSurvivesTheCycle(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "counter", countingGuardrail, map[string]string{"count.sh": countScript})

	got := e.Run(proj, "s-008-06", "write two notes", Turns("done",
		Write("w1", "counted/first.md", "hello"),
		Write("w2", "counted/second.md", "hello again"),
	))

	if got.Saw("state set failed") || got.Saw("no guardrail in scope") {
		t.Fatalf("the hook could not reach its state:\n%s", got.Output)
	}
	if !got.Saw("already saw a write in this session") {
		t.Fatalf("the second write was not recognised as a repeat — nothing survived the first cycle:\n%s", got.Output)
	}
	// The count came back as the value that was stored, not merely as "something
	// non-empty". A store that returned any garbage would satisfy the check above.
	if !got.Saw("count=1") {
		t.Fatalf("the value read back was not the value written:\n%s", got.Output)
	}
}
