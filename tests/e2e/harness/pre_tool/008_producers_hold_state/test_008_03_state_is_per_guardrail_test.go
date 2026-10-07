package e2e

import "testing"

// State is per guardrail: one rule's `sr-session state` entries are not another's.
// This is SHARED machinery the new dispatch reuses unchanged — the keyspace is
// keyed on the guard's name, set into SR_GUARDRAIL by the engine when it runs a
// check (internal/dispatch/exec.go). These tests re-prove it against NEW-format
// gates.
//
// # Ordering, and why some rules share a guard here
//
// The gate dispatch runs gates in NAME order, and a file already refused by
// a gate is not asked of later gates. So where a test needs a writer to run before
// a reader across two gates, the writer's gate is named to sort first and PERMITS
// (a permit falls through to the next gate; only a refusal stops the pass). Where two steps must share one keyspace, they are two
// `checks:` of one guard, run in declaration order.

// sharedKeyGuard is one guard with two checks: the first stores a value, the
// second reads it back and refuses with what it saw (a check's output only travels
// back when it refuses, so refusing is how a test observes what a check read).
// Both checks belong to one guard, so both share one keyspace.
const sharedKeyGuard = `on:
  - event: PreFileWrite
    match: event.path startsWith "shared/"
checks:
  - script: ./write.sh
  - script: ./report.sh
`

// Stores a distinctive value under the shared key, and refuses if it could not. A
// silent no-op here would make every later assertion vacuous.
const writerScript = `#!/bin/sh
cat >/dev/null
if sr-session state set shared-key writer-A-value; then
  exit 0
fi
echo "WRITER-FAILED" >&2
exit 1
`

// Reads the same key back and refuses with what it found, so the value reaches the
// test.
const reportScript = `#!/bin/sh
cat >/dev/null
got="$(sr-session state get shared-key)"
echo "SAME-RULE-SAW:[$got]" >&2
exit 1
`

// T008_03a: within one guardrail, a later check reads what an earlier one wrote.
//
// The control for the isolation test below. It establishes that the write really
// lands and is really readable in the same session — so when a different guardrail
// reads the same key and gets nothing, the emptiness is isolation and not a broken
// store.
func TestT008_03a_SameGuardrailSharesItsKeyspace(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "writer-a", sharedKeyGuard, map[string]string{
		"write.sh": writerScript, "report.sh": reportScript,
	})

	got := e.Run(proj, "s-008-03a", "write a note", Turns("done",
		Write("w1", "shared/notes.md", "hello"),
	))

	if got.Saw("WRITER-FAILED") {
		t.Fatalf("guardrail A could not store its value:\n%s", got.Output)
	}
	if !got.Saw("SAME-RULE-SAW:[writer-A-value]") {
		t.Fatalf("a rule could not read back its own entry in the same session:\n%s", got.Output)
	}
}

// a-writer is the writer as its own guard, named to sort BEFORE the reader so it
// runs first, and PERMITS so the pass reaches the reader.
const isolationWriterGuard = `on:
  - event: PreFileWrite
    match: event.path startsWith "isolated/"
checks:
  - script: ./write.sh
`

// z-reader reads a key another rule wrote, and must not see its value. Named to
// sort AFTER the writer.
const readerGuard = `on:
  - event: PreFileWrite
    match: event.path startsWith "isolated/"
checks:
  - script: ./read.sh
`

// Reads the shared key, and also writes and reads one of its OWN so the test can
// tell "isolated" apart from "the store is unreachable". Without that second pair,
// an engine that handed checks no environment at all would satisfy this test:
// every read would come back empty, including the one that is supposed to.
const readerScript = `#!/bin/sh
cat >/dev/null
sr-session state set own-key reader-B-value || { echo "READER-CANNOT-WRITE" >&2; exit 1; }
mine="$(sr-session state get own-key)"
got="$(sr-session state get shared-key)"
echo "OTHER-RULE-MINE:[$mine] OTHER-RULE-SAW:[$got]" >&2
exit 1
`

// T008_03b: one guardrail's entries are not another's.
//
// Guard a-writer stores `shared-key` (and permits); guard z-reader reads
// `shared-key` in the same session, on the same event, and gets nothing. The key
// is deliberately identical — a test using different keys would pass against a
// store with no isolation at all. What z-reader saw is asserted as an exact empty
// value rather than as "not A's value", so a store returning some third thing fails
// here too.
func TestT008_03b_StateIsPerGuardrail(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "a-writer", isolationWriterGuard, map[string]string{"write.sh": writerScript})
	e.Gate(proj, "z-reader", readerGuard, map[string]string{"read.sh": readerScript})

	got := e.Run(proj, "s-008-03b", "write a note", Turns("done",
		Write("w1", "isolated/notes.md", "hello"),
	))

	if got.Saw("WRITER-FAILED") {
		t.Fatalf("guardrail A could not store its value, so an empty read proves nothing:\n%s", got.Output)
	}
	if got.Saw("READER-CANNOT-WRITE") {
		t.Fatalf("guardrail B could not reach the store at all, so an empty read proves nothing:\n%s", got.Output)
	}
	if !got.Saw("OTHER-RULE-SAW:") {
		t.Fatalf("guardrail B's check never ran, so nothing was tested:\n%s", got.Output)
	}
	// B's own entry comes back, so B's reads genuinely work. Only against that does
	// the empty read of A's key mean isolation.
	if !got.Saw("OTHER-RULE-MINE:[reader-B-value]") {
		t.Fatalf("guardrail B could not read back its own entry, so an empty read of A's proves nothing:\n%s", got.Output)
	}
	if got.Saw("OTHER-RULE-SAW:[writer-A-value]") {
		t.Fatalf("guardrail B read guardrail A's entry — the keyspace is not per guardrail:\n%s", got.Output)
	}
	if !got.Saw("OTHER-RULE-SAW:[]") {
		t.Fatalf("guardrail B read something under a key only guardrail A wrote:\n%s", got.Output)
	}
}

// impostorGuard names another rule's keyspace by setting the variable itself
// before calling in. One guard, two checks in order.
const impostorGuard = `on:
  - event: PreFileWrite
    match: event.path startsWith "impostor/"
checks:
  - script: ./write.sh
  - script: ./impostor.sh
`

// The first check writes as this rule; the second overrides the variable the
// engine set and asks for a DIFFERENT rule's keyspace. Reading back its own value
// under another name would prove nothing, so the name asked for is one this rule
// never wrote under.
const impostorScript = `#!/bin/sh
cat >/dev/null
mine="$(sr-session state get shared-key)"
theirs="$(SR_GUARDRAIL=writer-a sr-session state get shared-key)"
echo "MINE:[$mine] THEIRS:[$theirs]" >&2
exit 1
`

// The impostor's own write, under its own keyspace, with a value distinct from the
// victim's so the two can never be confused. It also plants the victim's entry, by
// naming writer-a on the way in — the sharper claim: if a check can WRITE another
// rule's entry, the boundary is advisory in both directions.
const impostorOwnWrite = `#!/bin/sh
cat >/dev/null
sr-session state set shared-key impostor-own-value || { echo "WRITER-FAILED" >&2; exit 1; }
SR_GUARDRAIL=writer-a sr-session state set shared-key writer-A-value
exit 0
`

// T008_04: the keyspace boundary is advisory, and this pins which it is.
//
// A check is an arbitrary shell command, so it can set SR_GUARDRAIL to any name
// before invoking the CLI, and the CLI cannot tell that apart from the engine
// having set it. This asserts the honest behaviour rather than a wished-for one:
// naming another rule DOES reach that rule's entries. That is what the spec's
// rationale actually claims — it is about a rule depending on another's state by
// ACCIDENT, which the variable prevents by making the ambient answer the only
// convenient one, not a defence against a check that means to cross the line.
//
// If this ever fails, the boundary became real and this comment is what needs
// rewriting — do not weaken the code to make it pass again.
func TestT008_04_NamingAnotherGuardrailIsNotPrevented(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "impostor", impostorGuard, map[string]string{
		"write.sh": impostorOwnWrite, "impostor.sh": impostorScript,
	})

	got := e.Run(proj, "s-008-04", "write a note", Turns("done",
		Write("w1", "impostor/notes.md", "hello"),
	))

	if !got.Saw("MINE:") {
		t.Fatalf("the impostor's check never ran:\n%s", got.Output)
	}
	// Its own keyspace holds its own value, never writer-a's — so the read below
	// cannot be its own entry coming back under a different name.
	if !got.Saw("MINE:[impostor-own-value]") {
		t.Fatalf("the impostor could not read its own entry, so the comparison is meaningless:\n%s", got.Output)
	}
	if !got.Saw("THEIRS:[writer-A-value]") {
		t.Fatalf("the environment variable now prevents a check from naming another rule.\n"+
			"That is a stronger boundary than this test recorded — update the finding, do not weaken the code:\n%s",
			got.Output)
	}
}
