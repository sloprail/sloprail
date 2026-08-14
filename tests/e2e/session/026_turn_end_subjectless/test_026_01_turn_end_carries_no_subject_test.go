// Package e2e covers turn_end_subjectless, the invariant with no end-to-end
// test of its own before this suite.
//
// The predicate has two halves and they fail independently:
//
//   - an event whose kind is TurnEnd has an EMPTY subject;
//   - one of every OTHER kind names the file or command line it concerns.
//
// A suite asserting only the first passes against an engine that gives nothing
// a subject, which is the more damaging failure — a matcher narrowing on a path
// would then admit nothing and every narrowed rule would silently stop firing.
// So both halves are asserted here, against the same running engine.
//
// # Why the shape of "empty" is the thing under test
//
// The engine marshals a subjectless event as `"fields":{}` rather than as
// `null`, and internal/event says why in the code: a hook doing the obvious
// thing with null — `.fields.path` in jq, `["fields"].get("path")` in Python —
// gets an ERROR where it gets a clean miss on `{}`. The hook then exits
// non-zero, and a non-zero exit is a refusal. An event carrying nothing would
// refuse the work it was reporting on.
//
// That makes the distinction between `{}` and `null` a difference between a
// working guardrail and one that blocks every cycle, so it is asserted as the
// literal wire shape rather than as "no path was set". A test checking only
// that `path` was absent passes on `null`, which is the broken case.
//
// # Where TurnEnd comes from
//
// It is dispatched at the end of a cycle by the Stop hook, appended
// UNCONDITIONALLY after whatever the tree difference produced — see
// runPostDispatch. Nothing a scenario does makes it fire and nothing makes it
// stop firing, which is exactly what T026_02 pins.
package e2e

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// boundToTurnEnd records the whole event it was handed, once per cycle.
//
// No matcher. TurnEnd's kind declaration carries no fields at all, so there is
// nothing a matcher could narrow on — which is itself part of the invariant and
// is pinned separately by T026_04.
const boundToTurnEnd = `---
hooks:
  TurnEnd:
    - hooks:
        - type: command
          command: ./record.sh
---

# Records the cycle-end event it is given
`

// recordEvent writes the hook's entire stdin payload to the ledger as one line,
// so the test can read the event off the wire exactly as the hook received it.
//
// The whole payload rather than a field extracted with sed: the shape is what
// is under test, and a script that reached in for `.fields.path` would report
// "absent" for `null` and for `{}` alike — the two cases this suite exists to
// tell apart.
//
// tr squeezes the JSON onto one line. It arrives on one line already; the guard
// costs nothing and keeps a pretty-printing change from splitting one event
// across several ledger entries and being read as several cycles.
const recordEvent = `#!/bin/sh
tr -d '\n' < /dev/stdin >> "$PWD/events"
echo "" >> "$PWD/events"
exit 0
`

// payload is the JSON a hook was handed on its standard input.
type payload struct {
	Event struct {
		Kind   string          `json:"kind"`
		Fields json.RawMessage `json:"fields"`
	} `json:"event"`
}

// eventsSeen parses every event a guardrail's hook recorded, in the order it
// recorded them.
func eventsSeen(t *testing.T, e *harness.Env, proj, guardrail string) []payload {
	t.Helper()
	var out []payload
	for _, line := range e.Ledger(proj, guardrail, "events") {
		var p payload
		if err := json.Unmarshal([]byte(line), &p); err != nil {
			t.Fatalf("a hook recorded something that is not a payload: %v\n%s", err, line)
		}
		out = append(out, p)
	}
	return out
}

// project is a repository whose guardrails are committed before the session, so
// the rules' own folders are part of the baseline and the cycle's difference
// does not report them as newly created files.
func project(t *testing.T) (*harness.Env, string) {
	t.Helper()
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	return e, proj
}

func commitGuardrails(e *harness.Env, proj string) {
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "the project before the session")
}

// T026_01: the TurnEnd event a hook is given carries an empty subject, on the
// wire, as an object.
//
// The positive half of the invariant, and the one that had no test. Three
// separate claims:
//
//   - the hook ran at all — without this the rest is a test about an empty
//     ledger, which passes against an engine that dispatches no TurnEnd;
//   - the kind really is TurnEnd, so the payload being examined is the cycle's
//     and not some file event that happened to arrive;
//   - `fields` is the empty OBJECT. Not null, which is the shape that makes an
//     ordinary hook crash and turns a reporting event into a refusal.
func TestT026_01_TurnEndCarriesAnEmptySubject(t *testing.T) {
	e, proj := project(t)
	e.Guardrail(proj, "cycle-watch", boundToTurnEnd, map[string]string{"record.sh": recordEvent})
	commitGuardrails(e, proj)

	e.Run(proj, "s-026-01", "do a little work", Turns("done",
		Write("w1", "notes.md", "some work\n"),
	))

	seen := eventsSeen(t, e, proj, "cycle-watch")
	if len(seen) == 0 {
		t.Fatalf("no TurnEnd reached the hook, so nothing here is a claim about its subject")
	}

	last := seen[len(seen)-1]
	if last.Event.Kind != "TurnEnd" {
		t.Fatalf("a rule bound to TurnEnd was handed a %q", last.Event.Kind)
	}

	// The literal wire shape. `{}` is the contract; `null` is the shape that
	// makes `.fields.path` an error rather than a miss, and an event carrying
	// nothing would then refuse the work it was reporting on.
	got := strings.TrimSpace(string(last.Event.Fields))
	if got != "{}" {
		t.Fatalf("TurnEnd must carry an empty subject as an empty object, got %q — "+
			"null makes an ordinary hook error, and a hook that errors refuses the cycle it "+
			"was only reporting on", got)
	}
}

// T026_02: TurnEnd fires for a cycle that changed nothing at all.
//
// The unconditional half. A cycle that touched no file still ended, and the
// rules that fire on completeness — a required artifact never produced, a
// checklist not filled in — are exactly the ones whose violation looks like
// nothing having happened. An engine deriving TurnEnd from the tree difference
// would go silent in precisely the case those rules exist for.
//
// The scenario runs a command that touches nothing, so the difference is empty
// and the only event that can reach the hook is the cycle's own.
func TestT026_02_TurnEndFiresWhenNothingChanged(t *testing.T) {
	e, proj := project(t)
	e.Guardrail(proj, "cycle-watch", boundToTurnEnd, map[string]string{"record.sh": recordEvent})
	commitGuardrails(e, proj)

	e.Run(proj, "s-026-02", "look around and change nothing", Turns("done",
		Bash("b1", "true"),
	))

	seen := eventsSeen(t, e, proj, "cycle-watch")
	if len(seen) == 0 {
		t.Fatalf("a cycle that changed nothing dispatched no TurnEnd — the completeness rules " +
			"whose violation looks like nothing having happened would never fire")
	}
	if k := seen[len(seen)-1].Event.Kind; k != "TurnEnd" {
		t.Fatalf("want TurnEnd for a cycle that changed nothing, got %q", k)
	}
	if got := strings.TrimSpace(string(seen[len(seen)-1].Event.Fields)); got != "{}" {
		t.Fatalf("TurnEnd carried %q rather than an empty object", got)
	}
}

// T026_03: every kind that is NOT TurnEnd names what it concerns.
//
// The other half of the predicate, and the half a subject-less engine would
// pass if it were left out. Two producers are exercised in one session so the
// claim is about the rule rather than about one module: a file event must name
// its path, and a command event must name its command line.
//
// Asserted as "the subject is non-empty AND is the thing the scenario did",
// because an engine setting every subject to a constant would satisfy mere
// non-emptiness while making every narrowed matcher useless.
func TestT026_03_EveryOtherKindNamesItsSubject(t *testing.T) {
	e, proj := project(t)
	e.Guardrail(proj, "subjects", `---
hooks:
  PreFileCreate:
    - hooks:
        - type: command
          command: ./record.sh
  PreCommandInvoke:
    - hooks:
        - type: command
          command: ./record.sh
---

# Records the subject of every pending event it is shown
`, map[string]string{"record.sh": recordEvent})
	commitGuardrails(e, proj)

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
		fields := string(p.Event.Fields)
		switch p.Event.Kind {
		case "PreFileCreate":
			// The path the scenario actually wrote, not merely some non-empty
			// string — a constant subject would pass a non-emptiness check and
			// break every narrowed matcher.
			if !strings.Contains(fields, "docs/guide.md") {
				t.Errorf("a PreFileCreate did not name the file it concerns: %s", fields)
			}
			sawPath = true
		case "PreCommandInvoke":
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

// T026_04: a matcher naming a field on TurnEnd does not load, and the rule it
// belongs to therefore never runs.
//
// The consequence the invariant's own rationale states: a matcher narrowing on
// a subject "has nothing to narrow on here". TurnEnd's kind declaration carries
// no fields, so `path` is not a field it could ever have, and the load check
// that catches a misspelled field on any other kind catches every field here.
//
// This is what keeps the invariant from being merely descriptive. Without it an
// author could write `matcher: path endsWith ".md"` on TurnEnd, the expression
// would compile against an open environment, evaluate against nothing, and the
// rule would silently never fire — the exact failure the expression language was
// chosen to prevent.
//
// What this asserts is the LOADER's half: the hook never runs. That the failure
// is also REPORTED — rather than being a rule that quietly went away — is a
// separate claim and a separate mechanism, and it is T026_06's. Splitting them
// matters because they were not both true: the loader rejected this declaration
// correctly all along, while nothing at the cycle's own hook point ever said so.
//
// The hook records into the ledger, so "never ran" is observable as the absence
// of a file rather than inferred from silence on a stream.
func TestT026_04_AMatcherOnTurnEndDoesNotLoad(t *testing.T) {
	e, proj := project(t)
	e.Guardrail(proj, "narrowed-cycle", `---
hooks:
  TurnEnd:
    - matcher: path endsWith ".md"
      hooks:
        - type: command
          command: ./record.sh
---

# Tries to narrow the end of a cycle to a path, which a cycle does not have
`, map[string]string{"record.sh": recordEvent})
	commitGuardrails(e, proj)

	e.Run(proj, "s-026-04", "write a note", Turns("done",
		Write("w1", "notes.md", "hello\n"),
	))

	if ran := e.Ledger(proj, "narrowed-cycle", "events"); len(ran) != 0 {
		t.Fatalf("a matcher naming a field TurnEnd does not carry was accepted and its hook ran "+
			"%d time(s) — the expression evaluates against nothing, so the rule would appear to "+
			"be satisfied whatever the cycle did: %v", len(ran), ran)
	}
}

// T026_04b: the same rule with no matcher DOES run.
//
// The control for T026_04, and it is not optional. An empty ledger is what a
// rule that never loaded leaves, and it is also what a TurnEnd that never
// dispatched leaves, and what a hook the engine could not execute leaves. Only
// running the same guardrail — same kind, same hook, same script — with the one
// offending line removed tells those apart.
func TestT026_04b_TheSameRuleWithoutTheMatcherRuns(t *testing.T) {
	e, proj := project(t)
	e.Guardrail(proj, "narrowed-cycle", boundToTurnEnd, map[string]string{"record.sh": recordEvent})
	commitGuardrails(e, proj)

	e.Run(proj, "s-026-04b", "write a note", Turns("done",
		Write("w1", "notes.md", "hello\n"),
	))

	if ran := e.Ledger(proj, "narrowed-cycle", "events"); len(ran) == 0 {
		t.Fatalf("the same guardrail without the offending matcher did not run either, so " +
			"T026_04 proves nothing about the matcher")
	}
}

// T026_05: a TurnEnd hook that refuses blocks the cycle, and names its rule.
//
// The invariant says a rule bound to TurnEnd "runs for the cycle as a whole".
// That is only enforcement if its refusal governs, so this is the half that
// makes the rest matter: a completeness rule that cannot stop a turn is a rule
// that complains once and is ignored.
//
// The refusal is read from the blocking attachments rather than the stream. A
// Stop hook blocks by exiting 0 with {"decision":"block"} on stdout, so its
// stderr reaches no agent at all — a test scanning the stream would assert on a
// channel the refusal never travels.
func TestT026_05_ARefusalAtTurnEndBlocksTheCycle(t *testing.T) {
	e, proj := project(t)
	e.Guardrail(proj, "needs-changelog", boundToTurnEnd, map[string]string{
		"record.sh": "#!/bin/sh\ncat >/dev/null\necho 'the cycle produced no CHANGELOG entry' >&2\nexit 1\n",
	})
	commitGuardrails(e, proj)

	got := e.Run(proj, "s-026-05", "do some work", Turns("done",
		Write("w1", "notes.md", "work\n"),
	))

	// A blocked stop sends the agent round again, so the mock emits its final
	// result more than once. One result means the turn simply ended.
	if strings.Count(got.Output, `"subtype":"success"`) < 2 {
		t.Fatalf("a TurnEnd refusal did not stop the turn — a rule about the cycle as a whole "+
			"that cannot block it is advisory, which this product does not have:\n%s", got.Output)
	}

	told := strings.Join(e.BlockingErrors(proj, "s-026-05"), "\n")
	if !strings.Contains(told, "the cycle produced no CHANGELOG entry") {
		t.Errorf("the hook's own words did not reach the agent:\n%s", told)
	}
	if !strings.Contains(told, "needs-changelog") {
		t.Errorf("the refusal did not name the guardrail that produced it:\n%s", told)
	}
}
