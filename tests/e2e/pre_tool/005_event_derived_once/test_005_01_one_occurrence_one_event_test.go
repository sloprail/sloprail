package e2e

import (
	"encoding/json"
	"testing"
)

// event_derived_once: the event a rule is matched against and the event its check
// is given are the same one, derived once for the occurrence — not re-derived per
// binding.
//
// # RE-VEHICLED onto the NEW file-guard / gate natures (was old GUARDRAIL.md hooks)
//
// This is SHARED extraction machinery the new dispatch reuses: extractPreEvents
// runs each module ONCE per pre-tool dispatch and hands the SAME event objects to
// every file-guard and gate bound to that kind (services/sr-session/
// nature_pre_tool.go). The observable consequence of deriving per occurrence rather
// than per binding is that the derivations cannot disagree — and the cost the spec
// names outright: a cycle that parses every command line once per rule multiplies
// that work by the number of rules declared.
//
// The old rules installed via `e.Guardrail` and read the NESTED payload; these
// install NEW-format rules and read the FLAT event. A file occurrence is observed
// through file-guards (T005_01); a command occurrence is a GATE trigger, not a
// file-guard match, so it is observed through gates bound to PreCommandInvoke
// (T005_02) — the faithful new-format home for "one command line, walked once".

// recordAndPermitGuard is a preventive file-guard that records the event it
// received and permits. Recording rather than refusing, because a refusal stops the
// preventive pass at the first guard and this test needs every binding to have been
// reached. It records to the guard's own folder (SR_GUARDRAIL_DIR).
const recordAndPermitGuard = `match: "**/*.md"
preventive: true
checks:
  - script: ./record.sh
`

const recordScript = `#!/bin/sh
cat >> "$SR_GUARDRAIL_DIR/seen"
echo >> "$SR_GUARDRAIL_DIR/seen"
exit 0
`

// prePayload is one recorded payload's flat event.
type prePayload struct {
	Event struct {
		Kind string `json:"kind"`
		Path string `json:"path"`
	} `json:"event"`
}

// preLines keeps only the PreFileCreate payloads a guard recorded. A permitting
// preventive guard records at pre-tool AND again at Stop's after-check (as a Post
// event); the pre-tool derivation is the one this invariant is about, so the Post
// lines are filtered out.
func preLines(t *testing.T, lines []string) []prePayload {
	t.Helper()
	var out []prePayload
	for _, line := range lines {
		var p prePayload
		if err := json.Unmarshal([]byte(line), &p); err != nil {
			t.Fatalf("a check was handed something that is not an event payload: %v\n%s", err, line)
		}
		if p.Event.Kind == "PreFileCreate" {
			out = append(out, p)
		}
	}
	return out
}

// T005_01: every binding that sees one write is handed the same event.
//
// Not merely an equal one. Three guards bound to the same write must all receive
// the same kind and the same subject, because there is one occurrence to describe.
// Two derivations that disagreed would show up here as two different subjects for a
// single write — a match admitting an occurrence its check then judges on different
// facts.
func TestT005_01_EveryBindingSeesTheSameEvent(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	for _, name := range []string{"first", "second", "third"} {
		e.FileGuard(proj, name, recordAndPermitGuard, map[string]string{"record.sh": recordScript})
	}

	e.Run(proj, "s-005-01", "write a note", Turns("done",
		Write("w1", "some/notes.md", "hello"),
	))

	var subjects []string
	for _, name := range []string{"first", "second", "third"} {
		pre := preLines(t, e.FileGuardLedgerLines(proj, name, "seen"))
		if len(pre) != 1 {
			t.Fatalf("guardrail %q saw the pre event %d times for one write, want exactly 1: %v", name, len(pre), pre)
		}
		if pre[0].Event.Path == "" {
			t.Fatalf("guardrail %q got an event naming no file", name)
		}
		subjects = append(subjects, pre[0].Event.Path)
	}

	for i, s := range subjects {
		if s != subjects[0] {
			t.Fatalf("one write produced disagreeing events: binding 0 saw %q, binding %d saw %q", subjects[0], i, s)
		}
	}
}

// recordCommandGate is a gate that records the command event it was fired on and
// permits. A gate, not a file-guard, because a command occurrence is an event
// trigger (PreCommandInvoke) rather than a file's state — this is the new format's
// home for a rule about what a command line runs. It records to the gate's own
// folder (SR_GUARDRAIL_DIR) and never blocks, so all three gates run.
const recordCommandGate = `on:
  - event: PreCommandInvoke
checks:
  - script: ./record.sh
`

// T005_02: one command line is parsed once, however many rules bind to it.
//
// The cost half of the invariant, and the one with teeth. Establishing what a
// command line runs means walking its whole structure; doing that once per binding
// rather than once per occurrence multiplies it by the number of rules declared.
// Three gates bound to one command must each be handed one event carrying one
// identical flattened invocation list — evidence the walk happened once and its
// result was shared, not repeated per rule.
func TestT005_02_OneCommandLineIsWalkedOnce(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	for _, name := range []string{"first", "second", "third"} {
		e.Gate(proj, name, recordCommandGate, map[string]string{"record.sh": recordScript})
	}

	e.Run(proj, "s-005-02", "run something", Turns("done",
		Bash("b1", "npm publish --access public && echo done"),
	))

	var rendered []string
	for _, name := range []string{"first", "second", "third"} {
		lines := e.GateLedgerLines(proj, name, "seen")
		if len(lines) != 1 {
			t.Fatalf("gate %q ran %d times for one command, want exactly 1: %v", name, len(lines), lines)
		}
		var got struct {
			Event struct {
				Invocations any `json:"invocations"`
			} `json:"event"`
		}
		if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
			t.Fatalf("gate %q was handed something that is not an event payload: %v\n%s", name, err, lines[0])
		}
		// The flattened invocation list is the expensive derivation. Compared as
		// canonical JSON, so a difference in what any rule was told about what is
		// about to run fails here rather than passing as "both got a list".
		canon, err := json.Marshal(got.Event.Invocations)
		if err != nil {
			t.Fatalf("gate %q: re-encode invocations: %v", name, err)
		}
		if string(canon) == "null" {
			t.Fatalf("gate %q got a command event carrying no invocations:\n%s", name, lines[0])
		}
		rendered = append(rendered, string(canon))
	}

	for i, r := range rendered {
		if r != rendered[0] {
			t.Fatalf("one command line was resolved differently per binding:\n binding 0: %s\n binding %d: %s", rendered[0], i, r)
		}
	}
}
