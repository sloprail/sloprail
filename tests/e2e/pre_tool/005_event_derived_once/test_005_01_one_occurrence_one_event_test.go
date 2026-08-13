package e2e

import (
	"encoding/json"
	"testing"
)

// event_derived_once: the event a matcher is evaluated against and the event
// its hook is given are the same one, derived once for the occurrence.
//
// Three guardrails, all bound to the same kind, each recording the event it was
// handed. The observable consequence of deriving per binding rather than per
// occurrence is that the derivations can disagree — and the cost, which the
// spec names outright: a cycle that parses every command line once per
// guardrail multiplies that work by the number of rules declared.

// Each guardrail records the event it received and permits. Recording rather
// than refusing, because a refusal stops at the first one and this test needs
// every binding to have been reached.
const recordAndPermit = `---
hooks:
  PreFileCreate:
    - hooks:
        - type: command
          command: ./record.sh
---

# Records what it was shown

Permits unconditionally: the question here is which event arrived, not what
anyone decided about it.
`

// recordScript appends the whole payload as one line, so a test can compare
// what different bindings were handed byte for byte.
const recordScript = `#!/bin/sh
cat >> "$PWD/seen"
echo >> "$PWD/seen"
exit 0
`

// payloadEvent is the event out of one recorded hook payload.
type payloadEvent struct {
	Event struct {
		Kind   string         `json:"kind"`
		Fields map[string]any `json:"fields"`
	} `json:"event"`
}

// T005_01: every binding that sees one write is handed the same event.
//
// Not merely an equal one. Three guardrails bound to the same kind must all
// receive the same kind and the same subject, because there is one occurrence
// to describe. Two derivations that disagreed would show up here as two
// different subjects for a single write — a matcher admitting an occurrence its
// hook then judges on different facts.
func TestT005_01_EveryBindingSeesTheSameEvent(t *testing.T) {
	e := New(t)
	proj := e.Project()
	for _, name := range []string{"first", "second", "third"} {
		e.Guardrail(proj, name, recordAndPermit, map[string]string{"record.sh": recordScript})
	}

	e.Run(proj, "s-005-01", "write a note", Turns("done",
		Write("w1", "some/notes.md", "hello"),
	))

	var subjects []string
	for _, name := range []string{"first", "second", "third"} {
		lines := e.Ledger(proj, name, "seen")
		if len(lines) != 1 {
			t.Fatalf("guardrail %q ran %d times for one write, want exactly 1: %v", name, len(lines), lines)
		}
		var got payloadEvent
		if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
			t.Fatalf("guardrail %q was handed something that is not an event payload: %v\n%s", name, err, lines[0])
		}
		if got.Event.Kind != "PreFileCreate" {
			t.Errorf("guardrail %q got kind %q, want PreFileCreate", name, got.Event.Kind)
		}
		path, _ := got.Event.Fields["path"].(string)
		if path == "" {
			t.Fatalf("guardrail %q got an event naming no file:\n%s", name, lines[0])
		}
		subjects = append(subjects, path)
	}

	for i, s := range subjects {
		if s != subjects[0] {
			t.Fatalf("one write produced disagreeing events: binding 0 saw %q, binding %d saw %q", subjects[0], i, s)
		}
	}
}

// T005_02: one command line is parsed once, however many rules bind to it.
//
// The cost half of the invariant, and the one with teeth. Establishing what a
// command line runs means walking its whole structure; doing that once per
// binding rather than once per occurrence multiplies it by the number of
// guardrails declared. Three rules bound to one command must each be handed one
// event carrying one identical flattened invocation list — evidence the walk
// happened once and its result was shared, not repeated per rule.
func TestT005_02_OneCommandLineIsWalkedOnce(t *testing.T) {
	e := New(t)
	proj := e.Project()

	const bindCommand = `---
hooks:
  PreCommandInvoke:
    - hooks:
        - type: command
          command: ./record.sh
---

# Records the command it was shown
`
	for _, name := range []string{"first", "second", "third"} {
		e.Guardrail(proj, name, bindCommand, map[string]string{"record.sh": recordScript})
	}

	e.Run(proj, "s-005-02", "run something", Turns("done",
		Bash("b1", "npm publish --access public && echo done"),
	))

	var rendered []string
	for _, name := range []string{"first", "second", "third"} {
		lines := e.Ledger(proj, name, "seen")
		if len(lines) != 1 {
			t.Fatalf("guardrail %q ran %d times for one command, want exactly 1: %v", name, len(lines), lines)
		}
		var got payloadEvent
		if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
			t.Fatalf("guardrail %q was handed something that is not an event payload: %v\n%s", name, err, lines[0])
		}
		// The flattened invocation list is the expensive derivation. Compared
		// as canonical JSON, so a difference in what any rule was told about
		// what is about to run fails here rather than passing as "both got a
		// list".
		canon, err := json.Marshal(got.Event.Fields["invocations"])
		if err != nil {
			t.Fatalf("guardrail %q: re-encode invocations: %v", name, err)
		}
		if string(canon) == "null" {
			t.Fatalf("guardrail %q got a command event carrying no invocations:\n%s", name, lines[0])
		}
		rendered = append(rendered, string(canon))
	}

	for i, r := range rendered {
		if r != rendered[0] {
			t.Fatalf("one command line was resolved differently per binding:\n binding 0: %s\n binding %d: %s", rendered[0], i, r)
		}
	}
}
