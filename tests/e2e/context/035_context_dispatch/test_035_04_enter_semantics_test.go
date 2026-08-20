package e2e

import (
	"testing"
)

// This file covers the enter semantics the spec is most explicit about and that a
// composite depends on: enter fires on EVERY matching occurrence (not once), its
// stdout REPLACES the payload (a script grows a list by reading currentContext and
// returning the grown version), and a context whose `require` is unmet does not
// enter.

// countingContext enters on every PreToolUse and grows a counter in its payload by
// reading currentContext.payload.count and returning count+1 — proving enter fires
// on every occurrence AND that its stdout replaces the payload with the grown one.
const countingContext = `on:
  - event: PreToolUse
enter: ./enter.sh
exit: ./exit.sh
`

// enterGrowCounter reads the current count off currentContext.payload.count and
// writes count+1 — the "grow a list/counter by reading currentContext" shape the
// spec names for enter's replace-not-merge payload.
const enterGrowCounter = `#!/bin/sh
input="$(cat)"
cur="$(printf '%s' "$input" | sed -n 's/.*"currentContext":{"active":[^,]*,"payload":{"count":\([0-9]*\).*/\1/p' | head -1)"
if [ -z "$cur" ]; then cur=0; fi
next=$((cur + 1))
printf '{"count":%d}' "$next"
exit 0
`

// exitNever keeps the context active (non-zero = stay active).
const exitNever = `#!/bin/sh
cat >/dev/null
exit 1
`

// T035_08: enter fires on EVERY matching occurrence and its stdout REPLACES the
// payload — a counter grown across several tool calls in one cycle reaches the
// number of calls.
//
// Three tool calls in one cycle each fire PreToolUse, so enter runs three times,
// each reading the prior count and writing count+1. The final payload is 3.
func TestT035_08_EnterFiresEveryOccurrenceAndReplacesPayload(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Context(proj, "counter", countingContext, map[string]string{
		"enter.sh": enterGrowCounter,
		"exit.sh":  exitNever,
	})

	sess := "s-035-08"
	// Three tool calls in one cycle — three PreToolUse events, three enters.
	e.Run(proj, sess, "do three things", Turns("done",
		Write("w1", "a.md", "one"),
		Write("w2", "b.md", "two"),
		Write("w3", "c.md", "three"),
	))

	active, payload := e.ContextState(proj, sess, "counter")
	if !active {
		t.Fatalf("the counting context did not enter")
	}
	// Each PreToolUse fired enter once, growing the counter. Three writes → count 3.
	// (A float in JSON: the harness decodes numbers as float64.)
	if got, ok := payload["count"].(float64); !ok || got < 3 {
		t.Errorf("enter did not fire on every occurrence and grow the payload: count=%v (want >= 3)", payload["count"])
	}
}

// requireGatedContext enters only if the document-topic skill was loaded first —
// its `require` is checked before enter (the same Prerequisite semantics a gate
// uses).
const requireGatedContext = `on:
  - event: PreToolUse
require:
  - skill: document-topic
enter: ./enter.sh
exit: ./exit.sh
`

// enterMark writes a fixed payload so a test can see it entered.
const enterMark = `#!/bin/sh
cat >/dev/null
printf '{"entered":"yes"}'
exit 0
`

// T035_09: a context whose `require` is unmet does NOT enter.
//
// The context requires the document-topic skill; the cycle loads no skill, so the
// require fails and enter never runs — the context stays inactive. Proves a
// context's require gates its enter the same way a gate's require gates its checks.
func TestT035_09_RequireUnmetDoesNotEnter(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Context(proj, "gated", requireGatedContext, map[string]string{
		"enter.sh": enterMark,
		"exit.sh":  exitNever,
	})

	sess := "s-035-09"
	// A tool call with NO skill loaded first — the require is unmet.
	e.Run(proj, sess, "act without loading the skill", Turns("done",
		Write("w1", "a.md", "one"),
	))

	if active, _ := e.ContextState(proj, sess, "gated"); active {
		t.Errorf("a context whose require was unmet entered anyway")
	}
}

// T035_10: the SAME context DOES enter once the required skill was loaded.
//
// The control for T035_09: a Skill turn precedes the tool call, so the record
// holds a real Skill tool_use and the require passes — enter runs and the context
// activates.
func TestT035_10_RequireMetEnters(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Context(proj, "gated", requireGatedContext, map[string]string{
		"enter.sh": enterMark,
		"exit.sh":  exitNever,
	})

	sess := "s-035-10"
	e.Run(proj, sess, "load the skill then act", Turns("done",
		Skill("s1", "document-topic"),
		Write("w1", "a.md", "one"),
	))

	active, payload := e.ContextState(proj, sess, "gated")
	if !active {
		t.Fatalf("a context whose require was met did not enter")
	}
	if payload["entered"] != "yes" {
		t.Errorf("the context entered but its payload was not enter's: %v", payload)
	}
}
