package e2e

import (
	"encoding/json"
	"testing"
)

// hook_judges_event: a hook is given one event and decides about that event,
// and what reaches it is settled before it runs.
//
// Everything about which occurrences a rule sees belongs to the binding. A hook
// that had to work out whether an occurrence was its own would be
// re-implementing its matcher, and the two would drift. Observably that means:
// one invocation carries exactly one event, never a batch to filter; the
// narrowing has already happened, so nothing the matcher excluded arrives; and
// the payload carries the event and the guardrail's own folder, not the history
// a matcher is forbidden to read.

// narrowed admits only what is under guarded/. Two writes go out per test, one
// admitted and one not, so "the hook was handed only its own" is a claim with
// something to exclude rather than a description of the only write there was.
const narrowed = `---
hooks:
  PreFileCreate:
    - matcher: path startsWith "guarded/"
      hooks:
        - type: command
          command: ./record.sh
---

# Records what it is handed, and permits

The matcher is the whole of the scope. What arrives here has already been
narrowed by it.
`

const recordScript = `#!/bin/sh
cat >> "$PWD/seen"
echo >> "$PWD/seen"
exit 0
`

type payload struct {
	Event struct {
		Kind   string         `json:"kind"`
		Fields map[string]any `json:"fields"`
	} `json:"event"`
	GuardrailDir string `json:"guardrailDir"`
}

func decode(t *testing.T, line string) payload {
	t.Helper()
	var p payload
	if err := json.Unmarshal([]byte(line), &p); err != nil {
		t.Fatalf("hook was handed something that is not an event payload: %v\n%s", err, line)
	}
	return p
}

// T009_01: each invocation carries exactly one event, already narrowed.
//
// Two writes, one inside the binding and one outside. The hook must be run once,
// handed the admitted occurrence alone — not twice, not once with both to sort
// through.
func TestT009_01_HookIsHandedOneAlreadyNarrowedEvent(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "narrow", narrowed, map[string]string{"record.sh": recordScript})

	e.Run(proj, "s-009-01", "write two notes", Turns("done",
		Write("w1", "guarded/notes.md", "hello"),
		Write("w2", "elsewhere/notes.md", "hello"),
	))

	lines := e.Ledger(proj, "narrow", "seen")
	if len(lines) != 1 {
		t.Fatalf("the hook ran %d times for one admitted write, want 1: %v", len(lines), lines)
	}

	p := decode(t, lines[0])
	path, _ := p.Event.Fields["path"].(string)
	if path != "guarded/notes.md" {
		t.Fatalf("the hook was handed %q — the matcher's narrowing was not applied before it ran", path)
	}

	// One event, not a list. A payload carrying a batch would make filtering
	// the hook's job, which is the drift this invariant exists to prevent.
	var shape map[string]json.RawMessage
	if err := json.Unmarshal([]byte(lines[0]), &shape); err != nil {
		t.Fatalf("payload is not an object: %v", err)
	}
	if _, batched := shape["events"]; batched {
		t.Errorf("the hook was handed a list of events to filter itself:\n%s", lines[0])
	}
	if p.Event.Kind == "" {
		t.Errorf("the payload carries no single event:\n%s", lines[0])
	}
}

// T009_02: the payload carries the event and the guardrail's own folder, and no
// history to judge against.
//
// The spec's second reason: a matcher that could read what a rule remembered
// would be unverifiable when the guardrail loads, since the keys are the rule's
// own and unknown until it runs. A hook is handed the occurrence and where its
// own files are — what it remembered is fetched deliberately, never pushed at it
// as something to match on.
func TestT009_02_PayloadCarriesTheEventAndNothingToMatchHistoryOn(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "narrow", narrowed, map[string]string{"record.sh": recordScript})

	e.Run(proj, "s-009-02", "write a note", Turns("done",
		Write("w1", "guarded/notes.md", "hello"),
	))

	lines := e.Ledger(proj, "narrow", "seen")
	if len(lines) != 1 {
		t.Fatalf("the hook ran %d times, want 1: %v", len(lines), lines)
	}

	p := decode(t, lines[0])
	if p.GuardrailDir == "" {
		t.Errorf("the hook was not told where its own folder is — it cannot read the rubric beside it:\n%s", lines[0])
	}

	var shape map[string]json.RawMessage
	if err := json.Unmarshal([]byte(lines[0]), &shape); err != nil {
		t.Fatalf("payload is not an object: %v", err)
	}
	// The payload's whole surface. A key beyond these is a fact a matcher could
	// come to depend on, and the expression language was chosen precisely so a
	// matcher reaches nothing beyond the event it was given.
	for key := range shape {
		if key != "event" && key != "guardrailDir" {
			t.Errorf("the payload carries %q beyond the event and the guardrail's folder — a matcher could come to read it:\n%s", key, lines[0])
		}
	}
}

// T009_03: a hook bound to one kind is never handed another.
//
// What reaches a hook is settled by its binding's event kind before the hook
// runs. A rule about files asked to judge a command event would have to detect
// and ignore it — re-implementing, in script, the routing the binding already
// declared.
func TestT009_03_HookIsNeverHandedAnotherKind(t *testing.T) {
	e := New(t)
	proj := e.Project()

	const filesOnly = `---
hooks:
  PreFileCreate:
    - hooks:
        - type: command
          command: ./record.sh
---

# Bound to file creation alone
`
	e.Guardrail(proj, "files-only", filesOnly, map[string]string{"record.sh": recordScript})

	e.Run(proj, "s-009-03", "write then run", Turns("done",
		Write("w1", "some/notes.md", "hello"),
		Bash("b1", "npm publish --access public"),
	))

	lines := e.Ledger(proj, "files-only", "seen")
	if len(lines) != 1 {
		t.Fatalf("a hook bound to one kind ran %d times across two differing turns, want 1: %v", len(lines), lines)
	}
	if kind := decode(t, lines[0]).Event.Kind; kind != "PreFileCreate" {
		t.Fatalf("a hook bound to PreFileCreate was handed %q", kind)
	}
}
