package e2e

import (
	"encoding/json"
	"strings"
	"testing"
)

// producers_hold_state: whatever must be remembered to report an event honestly
// is remembered by whatever produces that event, not by the hook point that
// dispatches it.
//
// The observable form: the dispatcher carries nothing kind-specific. What it
// hands a hook is the event and the guardrail's folder, and adding a kind costs
// it nothing — a hook point holding state for one kind would hold it for every
// kind, and would grow by the size of each new kind added.
//
// What CANNOT be shown from outside the binary is the positive half — that a
// producer holds the state it needs. The one producer that would need to
// remember anything (the file module's after-the-fact diff, which must know
// where the session started) is an unimplemented stub, so there is no
// remembering to observe yet. See the note on T008_02.

const recordPayload = `---
hooks:
  PreFileCreate:
    - hooks:
        - type: command
          command: ./record.sh
  PreFileUpdate:
    - hooks:
        - type: command
          command: ./record.sh
---

# Records the whole payload it is handed
`

const recordScript = `#!/bin/sh
cat >> "$PWD/seen"
echo >> "$PWD/seen"
exit 0
`

// T008_01: the dispatcher hands over the event and nothing kind-specific.
//
// Two different kinds through the same hook point, and the payload's shape must
// not vary with the kind. A dispatcher that had learned what a file event is —
// carrying a baseline, a prior verdict, a per-kind slot — would show that here
// as a key present for one kind and absent for another, or as a key describing
// something other than the occurrence.
func TestT008_01_DispatcherCarriesNothingKindSpecific(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "recorder", recordPayload, map[string]string{"record.sh": recordScript})

	// The first write creates; the second updates the file the first left
	// behind. Two kinds, one hook point.
	e.Run(proj, "s-008-01", "write twice", Turns("done",
		Write("w1", "notes.md", "hello"),
		Write("w2", "notes.md", "hello again"),
	))

	lines := e.Ledger(proj, "recorder", "seen")
	if len(lines) != 2 {
		t.Fatalf("want one dispatch per write, got %d: %v", len(lines), lines)
	}

	var kinds []string
	for i, line := range lines {
		var shape map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &shape); err != nil {
			t.Fatalf("dispatch %d is not an object: %v\n%s", i, err, line)
		}
		// The dispatcher's whole surface, identical for every kind. Anything
		// else is the hook point holding something on a kind's behalf.
		for key := range shape {
			if key != "event" && key != "guardrailDir" {
				t.Errorf("the hook point carries %q alongside the event — state a producer should hold:\n%s", key, line)
			}
		}
		var p struct {
			Event struct {
				Kind string `json:"kind"`
			} `json:"event"`
		}
		if err := json.Unmarshal([]byte(line), &p); err != nil {
			t.Fatalf("dispatch %d carries no event: %v", i, err)
		}
		kinds = append(kinds, p.Event.Kind)
	}

	// The two dispatches must genuinely differ in kind, or the check above
	// compared one kind against itself and proved nothing.
	if kinds[0] == kinds[1] {
		t.Fatalf("both dispatches were %q — the payload shape was never compared across kinds", kinds[0])
	}
	for _, k := range kinds {
		if !strings.HasPrefix(k, "PreFile") {
			t.Errorf("unexpected kind %q", k)
		}
	}
}

// T008_02: a hook is told which guardrail and which session it is running as,
// so what a rule remembers is reachable without the dispatcher holding it.
//
// This is the invariant's mechanism. A rule spanning more than one cycle keeps
// what it knows through `sr-session state`, and that command deliberately
// takes neither the guardrail nor the session as an argument — a hook able to
// name either could read a rule it was never told about. They come from the
// environment the engine sets when it runs the hook, which is precisely how the
// dispatcher avoids holding the memory itself: it names the scope and the
// producer keeps the contents.
//
// This was written skipped, against a branch whose runHooks set no environment
// on the hook process at all — the test stated the invariant honestly and waited
// for the gap to close. The gap is closed here: runHooks now assigns c.Env, and
// the variables are SR_GUARDRAIL / SR_SESSION_ID / SR_WORKSPACE. The skip is
// lifted, and this passes.
func TestT008_02_HookIsToldItsOwnScope(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "remembers", recordPayload, map[string]string{
		// Store something under the rule's own key, then read it back. If the
		// scope is set, this round-trips; if it is not, the command errors.
		"record.sh": `#!/bin/sh
cat >/dev/null
sr-session state set seen yes >>"$PWD/seen" 2>&1
echo "got=$(sr-session state get seen 2>&1)" >> "$PWD/seen"
exit 0
`,
	})

	e.Run(proj, "s-008-02", "write a note", Turns("done",
		Write("w1", "notes.md", "hello"),
	))

	lines := e.Ledger(proj, "remembers", "seen")
	var joined = strings.Join(lines, "\n")
	if !strings.Contains(joined, "got=yes") {
		t.Fatalf("a hook could not remember anything under its own scope:\n%s", joined)
	}
}
