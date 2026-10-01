// Package e2e covers stop_subjectless, the invariant with no end-to-end
// test of its own before this suite.
//
// The predicate has two halves and they fail independently:
//
//   - an event whose kind is Stop has an EMPTY subject;
//   - one of every OTHER kind names the file or command line it concerns.
//
// A suite asserting only the first passes against an engine that gives nothing
// a subject, which is the more damaging failure — a matcher narrowing on a path
// would then admit nothing and every narrowed rule would silently stop firing.
// So both halves are asserted here, against the same running engine.
//
// # Why the shape of "empty" is the thing under test
//
// A subjectless event must reach a check as an OBJECT carrying only its kind,
// never as a null. internal/declaration's FlatEvent says why in its own code: a
// nil Fields marshals to `{"kind":"Stop"}` — "an object, never a null, so a script
// indexing `.event.<anything>` gets a clean miss rather than an error". A hook
// doing the obvious thing with null — `.event.path` in jq — errors, exits
// non-zero, and a non-zero exit is a refusal; an event carrying nothing would then
// refuse the work it was only reporting on. That makes the distinction a
// difference between a working guardrail and one that blocks every cycle, so it is
// asserted as the literal wire shape: the flat `event` object carries `kind` and
// NO subject key, rather than being null.
//
// Stop is a GateEventKind, so a rule bound to Stop is now a GATE that wakes on it,
// and the gate's check is what the Stop event is handed to. Every OTHER kind here
// (PreFileCreate, PreCommandInvoke) is a GateEventKind too, so the subject-naming
// half is a gate on those pre-action kinds. The check receives the FLAT event (`.event.kind`, `.event.invocations`) and the
// subjectless-Stop assertion is read against that shape — `{"kind":"Stop"}`, an
// object with no subject.
//
// # Where Stop comes from
//
// It is dispatched at the end of a cycle by the Stop dispatch, appended
// UNCONDITIONALLY after whatever the tree difference produced. Nothing a scenario
// does makes it fire and nothing makes it stop firing, which is what T026_02 pins.
package e2e

import (
	"encoding/json"
	"strings"
	"testing"
)

// boundToStop is a NEW-FORMAT gate that wakes on Stop and records the event it was
// handed, once per cycle. No `match`: Stop's kind declaration carries no fields at
// all, so there is nothing a match could narrow on — which is itself part of the
// invariant and is pinned separately by T026_04. Its check permits, so nothing
// here blocks except where a test's own check refuses.
const boundToStop = `on:
  - event: Stop
checks:
  - script: ./record.sh
`

// recordEvent writes the check's entire stdin payload to the ledger as one line,
// so the test can read the event off the wire exactly as the check received it.
//
// The whole payload rather than a field extracted with jq: the shape is what is
// under test, and a script that reached in for `.event.path` would report the same
// "absent" for a null event and for a `{"kind":"Stop"}` one alike — the two cases
// this suite exists to tell apart. The ledger is $SR_GUARDRAIL_DIR/events, the
// folder the engine sets for the check (`.sloprail/gate/<name>/`).
const recordEvent = `#!/bin/sh
tr -d '\n' < /dev/stdin >> "$SR_GUARDRAIL_DIR/events"
echo "" >> "$SR_GUARDRAIL_DIR/events"
exit 0
`

// payload is one event a check recorded, decoded from the FLAT wire form: the
// event's own fields spread directly under `event` with `kind` beside them, so
// Kind is the discriminator and Subject is every OTHER key it carried (a file's
// path, a command's invocations, …). rawEvent keeps the `event` object verbatim
// for the shape assertions. There is no `event.fields` envelope in the new wire
// form, so eventsSeen populates these by hand rather than by struct tags.
type payload struct {
	Event struct {
		Kind    string
		Subject map[string]json.RawMessage
	}
	rawEvent json.RawMessage
}

// eventsSeen parses every event a gate's check recorded, in the order it recorded
// them, reading them back off the gate's own ledger.
func eventsSeen(t *testing.T, e *Env, proj, gateName string) []payload {
	t.Helper()
	var out []payload
	for _, line := range e.GateLedgerLines(proj, gateName, "events") {
		var envelope struct {
			Event json.RawMessage `json:"event"`
		}
		if err := json.Unmarshal([]byte(line), &envelope); err != nil {
			t.Fatalf("a check recorded something that is not a payload: %v\n%s", err, line)
		}
		// The flat event: a map of every key it carries. `kind` is the
		// discriminator; every other key is a subject field. A null event (the
		// shape this suite forbids for Stop) decodes to a nil map, which is what the
		// subject assertions read against.
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(envelope.Event, &fields); err != nil {
			t.Fatalf("the recorded event is not an object: %v\n%s", err, string(envelope.Event))
		}
		var p payload
		p.rawEvent = envelope.Event
		if kindRaw, ok := fields["kind"]; ok {
			_ = json.Unmarshal(kindRaw, &p.Event.Kind)
		}
		subject := make(map[string]json.RawMessage, len(fields))
		for k, v := range fields {
			if k == "kind" {
				continue
			}
			subject[k] = v
		}
		p.Event.Subject = subject
		out = append(out, p)
	}
	return out
}

// project is a repository whose rules are committed before the session, so their
// own folders are part of the baseline and the cycle's difference does not report
// them as newly created files.
func project(t *testing.T) (*Env, string) {
	t.Helper()
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	return e, proj
}

// T026_01: the Stop event a check is given carries an empty subject, on the wire,
// as an object.
//
// The positive half of the invariant, and the one that had no test. Three separate
// claims:
//
//   - the check ran at all — without this the rest is a test about an empty
//     ledger, which passes against an engine that dispatches no Stop;
//   - the kind really is Stop, so the payload being examined is the cycle's and
//     not some other event that happened to arrive;
//   - the flat `event` is an OBJECT carrying only `kind` and NO subject key. Not
//     null, which is the shape that makes an ordinary check crash and turns a
//     reporting event into a refusal. This is the flat-form version of the old
//     `"fields":{}` assertion — an empty subject, present as an object.
func TestT026_01_StopCarriesAnEmptySubject(t *testing.T) {
	e, proj := project(t)
	e.Gate(proj, "cycle-watch", boundToStop, map[string]string{"record.sh": recordEvent})
	e.CommitAll(proj, "the project before the session")

	e.Run(proj, "s-026-01", "do a little work", Turns("done",
		Write("w1", "notes.md", "some work\n"),
	))

	seen := eventsSeen(t, e, proj, "cycle-watch")
	if len(seen) == 0 {
		t.Fatalf("no Stop reached the check, so nothing here is a claim about its subject")
	}

	last := seen[len(seen)-1]
	if last.Event.Kind != "Stop" {
		t.Fatalf("a gate bound to Stop was handed a %q", last.Event.Kind)
	}

	// The literal wire shape. The event decoded to an object (not null — a null
	// would have failed eventsSeen's object decode), it carries the kind, and it
	// carries NO subject field: `.event.path` on it is a clean miss, not the error
	// a null produces, and an event carrying nothing would then refuse the work it
	// was only reporting on.
	if last.rawEvent == nil || string(last.rawEvent) == "null" {
		t.Fatalf("Stop's event arrived as null rather than an object — `.event.path` on it is an "+
			"error, and a check that errors refuses the cycle it was only reporting on:\n%s", string(last.rawEvent))
	}
	if len(last.Event.Subject) != 0 {
		keys := make([]string, 0, len(last.Event.Subject))
		for k := range last.Event.Subject {
			keys = append(keys, k)
		}
		t.Fatalf("Stop must carry an empty subject — the flat event carried subject field(s) %v "+
			"besides kind: %s", keys, string(last.rawEvent))
	}
}

// T026_02: Stop fires for a cycle that changed nothing at all.
//
// The unconditional half. A cycle that touched no file still ended, and the rules
// that fire on completeness — a required artifact never produced, a checklist not
// filled in — are exactly the ones whose violation looks like nothing having
// happened. An engine deriving Stop from the tree difference would go silent in
// precisely the case those rules exist for.
//
// The scenario runs a command that touches nothing, so the difference is empty and
// the only event that can reach the check is the cycle's own Stop.
func TestT026_02_StopFiresWhenNothingChanged(t *testing.T) {
	e, proj := project(t)
	e.Gate(proj, "cycle-watch", boundToStop, map[string]string{"record.sh": recordEvent})
	e.CommitAll(proj, "the project before the session")

	e.Run(proj, "s-026-02", "look around and change nothing", Turns("done",
		Bash("b1", "true"),
	))

	seen := eventsSeen(t, e, proj, "cycle-watch")
	if len(seen) == 0 {
		t.Fatalf("a cycle that changed nothing dispatched no Stop — the completeness rules " +
			"whose violation looks like nothing having happened would never fire")
	}
	last := seen[len(seen)-1]
	if last.Event.Kind != "Stop" {
		t.Fatalf("want Stop for a cycle that changed nothing, got %q", last.Event.Kind)
	}
	if len(last.Event.Subject) != 0 {
		t.Fatalf("Stop carried a subject rather than an empty object: %s", string(last.rawEvent))
	}
}

// T026_03: every kind that is NOT Stop names what it concerns.
//
// The other half of the predicate, and the half a subject-less engine would pass
// if it were left out. Two producers are exercised in one session so the claim is
// about the engine rather than about one module: a file event must name its path,
// and a command event must name its command line.
//
// Asserted as "the subject is the thing the scenario did", because an engine
// setting every subject to a constant would satisfy mere presence while making
// every narrowed matcher useless. One gate bound to both pre-action kinds fires
// once per matching tool call (a pre-tool dispatch per Write and per Bash), so it
// records both events.
func TestT026_03_EveryOtherKindNamesItsSubject(t *testing.T) {
	e, proj := project(t)
	e.Gate(proj, "subjects", `on:
  - event: PreFileCreate
  - event: PreCommandInvoke
checks:
  - script: ./record.sh
`, map[string]string{"record.sh": recordEvent})
	e.CommitAll(proj, "the project before the session")

	e.Run(proj, "s-026-03", "write a file and run a command", Turns("done",
		Write("w1", "docs/guide.md", "hello\n"),
		Bash("b1", "grep -r needle src"),
	))

	seen := eventsSeen(t, e, proj, "subjects")
	if len(seen) < 2 {
		t.Fatalf("want a file event and a command event, got %d: %+v", len(seen), seen)
	}

	var sawPath, sawCommand bool
	for _, p := range seen {
		fields := string(p.rawEvent)
		switch p.Event.Kind {
		case "PreFileCreate":
			// The path the scenario actually wrote, on the flat event under
			// `.event.path` — not merely some non-empty string, which a constant
			// subject would satisfy while breaking every narrowed matcher.
			if !strings.Contains(fields, "docs/guide.md") {
				t.Errorf("a PreFileCreate did not name the file it concerns: %s", fields)
			}
			sawPath = true
		case "PreCommandInvoke":
			// The command line, carried on the flat event under `.event.invocations`.
			if !strings.Contains(fields, "needle") {
				t.Errorf("a PreCommandInvoke did not name the command line it concerns: %s", fields)
			}
			sawCommand = true
		}
	}

	if !sawPath {
		t.Errorf("no PreFileCreate reached the rule, so the file half proves nothing")
	}
	if !sawCommand {
		t.Errorf("no PreCommandInvoke reached the rule, so the command half proves nothing")
	}
}

// T026_04: a match naming a field on Stop does not load, and the gate it belongs
// to therefore never runs.
//
// The consequence the invariant's own rationale states: a match narrowing on a
// subject "has nothing to narrow on here". Stop's kind declaration carries no
// fields, so `event.path` is not a field it could ever have, and the check the
// gate's trigger compiles against Stop's fieldless scope fails to compile — so the
// gate is skipped and its check never runs.
//
// This is what keeps the invariant from being merely descriptive. Without it an
// author could write a trigger `match: event.path endsWith ".md"` on Stop, the
// gate would run against nothing, and it would appear satisfied whatever the cycle
// did — the exact failure the expression language was chosen to prevent.
//
// The check records into the ledger, so "never ran" is observable as the absence
// of a file rather than inferred from silence on a stream.
func TestT026_04_AMatchOnStopDoesNotLoad(t *testing.T) {
	e, proj := project(t)
	e.Gate(proj, "narrowed-cycle", `on:
  - event: Stop
    match: event.path endsWith ".md"
checks:
  - script: ./record.sh
`, map[string]string{"record.sh": recordEvent})
	e.CommitAll(proj, "the project before the session")

	e.Run(proj, "s-026-04", "write a note", Turns("done",
		Write("w1", "notes.md", "hello\n"),
	))

	if ran := e.GateLedgerLines(proj, "narrowed-cycle", "events"); len(ran) != 0 {
		t.Fatalf("a match naming a field Stop does not carry was accepted and its check ran "+
			"%d time(s) — the expression evaluates against nothing, so the rule would appear to "+
			"be satisfied whatever the cycle did: %v", len(ran), ran)
	}
}

// T026_04b: the same gate with no match DOES run.
//
// The control for T026_04, and it is not optional. An empty ledger is what a gate
// that never loaded leaves, and it is also what a Stop that never dispatched
// leaves, and what a check the engine could not execute leaves. Only running the
// same gate — same kind, same check, same script — with the one offending line
// removed tells those apart.
func TestT026_04b_TheSameRuleWithoutTheMatchRuns(t *testing.T) {
	e, proj := project(t)
	e.Gate(proj, "narrowed-cycle", boundToStop, map[string]string{"record.sh": recordEvent})
	e.CommitAll(proj, "the project before the session")

	e.Run(proj, "s-026-04b", "write a note", Turns("done",
		Write("w1", "notes.md", "hello\n"),
	))

	if ran := e.GateLedgerLines(proj, "narrowed-cycle", "events"); len(ran) == 0 {
		t.Fatalf("the same gate without the offending match did not run either, so " +
			"T026_04 proves nothing about the match")
	}
}

// T026_05: a Stop gate that refuses blocks the cycle, and names its rule.
//
// The invariant says a rule bound to Stop "runs for the cycle as a whole". That is
// only enforcement if its refusal governs, so this is the half that makes the rest
// matter: a completeness rule that cannot stop a turn is a rule that complains once
// and is ignored.
//
// The refusal is read from the blocking attachments rather than the stream. A Stop
// gate blocks the TURN, and its reason travels as a blocking error on the record,
// not as a line on the result stream — BlockingErrorsFrom(…, "Stop") reads it.
func TestT026_05_ARefusalAtStopBlocksTheCycle(t *testing.T) {
	e, proj := project(t)
	e.Gate(proj, "needs-changelog", boundToStop, map[string]string{
		"record.sh": `#!/bin/sh
cat >/dev/null
echo '{"reason":"the cycle produced no CHANGELOG entry"}'
exit 1
`,
	})
	e.CommitAll(proj, "the project before the session")

	e.Run(proj, "s-026-05", "do some work", Turns("done",
		Write("w1", "notes.md", "work\n"),
	))

	told := strings.Join(e.BlockingErrorsFrom(proj, "s-026-05", "Stop"), "\n")
	if told == "" {
		t.Fatalf("a Stop refusal did not stop the turn — a rule about the cycle as a whole that " +
			"cannot block it is advisory, which this product does not have")
	}
	if !strings.Contains(told, "the cycle produced no CHANGELOG entry") {
		t.Errorf("the check's own words did not reach the agent:\n%s", told)
	}
	if !strings.Contains(told, "needs-changelog") {
		t.Errorf("the refusal did not name the gate that produced it:\n%s", told)
	}
}
