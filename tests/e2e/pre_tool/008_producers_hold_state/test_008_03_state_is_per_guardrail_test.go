package e2e

import "testing"

// Two guardrails bound to the same event, writing and reading the SAME key.
//
// Both fire on the one write, so the reader genuinely runs in a session where
// the writer has already stored something — which is what makes an empty read
// mean isolation rather than merely "nothing had happened yet".
//
// The reader refuses, always, and says what it saw in the reason. A hook's
// output only travels back when it refuses, so refusing is how a test observes
// what a hook read. Ordering is what makes the writer's entry already present:
// hooks within a binding run in declaration order, and between guardrails
// nothing is promised — so the two live in one guardrail, in order, rather than
// in two that happen to be loaded alphabetically.
const sharedKeyGuardrail = `---
hooks:
  PreFileCreate:
    - matcher: path startsWith "shared/"
      hooks:
        - type: command
          command: ./write.sh
        - type: command
          command: ./report.sh
---

# Writes a key, then reports what a second hook of the same rule sees

Both hooks belong to one guardrail, so both share one keyspace. What the second
reads is what the first wrote.
`

// Stores a distinctive value under the shared key, and refuses if it could not.
// A silent no-op here would make every later assertion vacuous.
const writerScript = `#!/bin/sh
cat >/dev/null
if sr-session state set shared-key writer-A-value; then
  exit 0
fi
echo "WRITER-FAILED" >&2
exit 1
`

// Reads the same key back and refuses with what it found, so the value reaches
// the test.
const reportScript = `#!/bin/sh
cat >/dev/null
got="$(sr-session state get shared-key)"
echo "SAME-RULE-SAW:[$got]" >&2
exit 1
`

// T008_03a: within one guardrail, a later hook reads what an earlier one wrote.
//
// The control for the isolation test below. It establishes that the write
// really lands and is really readable in the same session — so when a different
// guardrail reads the same key and gets nothing, the emptiness is isolation and
// not a broken store.
func TestT008_03a_SameGuardrailSharesItsKeyspace(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "writer-a", sharedKeyGuardrail, map[string]string{
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

// The second guardrail: same event, same key, different rule. It only reads.
const readerGuardrail = `---
hooks:
  PreFileCreate:
    - matcher: path startsWith "isolated/"
      hooks:
        - type: command
          command: ./read.sh
---

# Reads a key another rule wrote, and must not see its value

A rule that could read another's entries would depend on when that rule ran.
`

// The writer as its own rule, bound to the same path as the reader so both fire
// on one event.
const isolationWriterGuardrail = `---
hooks:
  PreFileCreate:
    - matcher: path startsWith "isolated/"
      hooks:
        - type: command
          command: ./write.sh
---

# Stores a value under a key another guardrail also uses

Its entry is its own: the keyspace is per guardrail, so the name it chose says
nothing about anyone else's.
`

// Reads the shared key, and also writes and reads one of its OWN so the test
// can tell "isolated" apart from "the store is unreachable".
//
// Without that second pair, an engine that handed hooks no environment at all
// would satisfy this test: every read would come back empty, including the one
// that is supposed to.
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
// Guardrail A stores `shared-key`; guardrail B reads `shared-key` in the same
// session, on the same event, and gets nothing. The key is deliberately
// identical — a test using different keys would pass against a store with no
// isolation at all.
//
// Both hooks really run: A's write is proven by T008_03a using the same script,
// and B's read is proven by its reason arriving. What B saw is asserted as an
// exact empty value rather than as "not A's value", so a store returning some
// third thing fails here too.
func TestT008_03b_StateIsPerGuardrail(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "writer-a", isolationWriterGuardrail, map[string]string{"write.sh": writerScript})
	e.Guardrail(proj, "reader-b", readerGuardrail, map[string]string{"read.sh": readerScript})

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
		t.Fatalf("guardrail B's hook never ran, so nothing was tested:\n%s", got.Output)
	}
	// B's own entry comes back, so B's reads genuinely work. Only against that
	// does the empty read of A's key mean isolation.
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

// A guardrail that names another rule's keyspace by setting the variable itself
// before calling in.
const impostorGuardrail = `---
hooks:
  PreFileCreate:
    - matcher: path startsWith "impostor/"
      hooks:
        - type: command
          command: ./write.sh
        - type: command
          command: ./impostor.sh
---

# Attempts to read another rule's entries by naming it

Records what the attempt yields, so the boundary's actual strength is written
down rather than assumed.
`

// The first hook writes as this rule; the second overrides the variable the
// engine set and asks for a DIFFERENT rule's keyspace. Reading back its own
// value under another name would prove nothing, so the name asked for is one
// this rule never wrote under.
const impostorScript = `#!/bin/sh
cat >/dev/null
mine="$(sr-session state get shared-key)"
theirs="$(SR_GUARDRAIL=writer-a sr-session state get shared-key)"
echo "MINE:[$mine] THEIRS:[$theirs]" >&2
exit 1
`

// T008_04: the boundary is advisory, and this pins which it is.
//
// A hook is an arbitrary shell command, so it can set SR_GUARDRAIL to any name
// before invoking the CLI, and the CLI cannot tell that apart from the engine
// having set it. This asserts the honest behaviour rather than a wished-for
// one: naming another rule DOES reach that rule's entries.
//
// That is what the spec's rationale actually claims. It is about a rule
// depending on another's state by ACCIDENT, which the variable prevents by
// making the ambient answer the only convenient one — a hook that never names a
// guardrail cannot reach a keyspace it was not told about. It is not a defence
// against a hook that means to cross the line, and nothing at this layer could
// be: the hook already runs as the user, with the same filesystem access as the
// engine, and could open the store directly.
//
// If this ever fails, the boundary became real and this comment is what needs
// rewriting — do not weaken the code to make it pass again.
func TestT008_04_NamingAnotherGuardrailIsNotPrevented(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "impostor", impostorGuardrail, map[string]string{
		"write.sh": impostorOwnWrite, "impostor.sh": impostorScript,
	})

	got := e.Run(proj, "s-008-04", "write a note", Turns("done",
		Write("w1", "impostor/notes.md", "hello"),
	))

	if !got.Saw("MINE:") {
		t.Fatalf("the impostor's hook never ran:\n%s", got.Output)
	}
	// Writing into another rule's keyspace was not prevented either: the value
	// read back below was put there by THIS rule naming writer-a, and writer-a
	// has no hook in this project at all. So the read proves both directions.
	// Its own keyspace holds its own value, never writer-a's — so the read
	// below cannot be its own entry coming back under a different name.
	if !got.Saw("MINE:[impostor-own-value]") {
		t.Fatalf("the impostor could not read its own entry, so the comparison is meaningless:\n%s", got.Output)
	}
	if !got.Saw("THEIRS:[writer-A-value]") {
		t.Fatalf("the environment variable now prevents a hook from naming another rule.\n"+
			"That is a stronger boundary than this test recorded — update the finding, do not weaken the code:\n%s",
			got.Output)
	}
}

// The impostor's own write, under its own keyspace, with a value distinct from
// the victim's so the two can never be confused.
//
// It also plants the victim's entry, by naming writer-a on the way in. Between
// guardrails no order is promised, and the impostor refuses — which stops the
// binding before any later guardrail's hook runs — so waiting for writer-a to
// write on its own would leave the keyspace empty for a reason that has nothing
// to do with isolation. Planting it here is also the sharper claim: if a hook
// can WRITE another rule's entry, the boundary is advisory in both directions.
const impostorOwnWrite = `#!/bin/sh
cat >/dev/null
sr-session state set shared-key impostor-own-value || { echo "WRITER-FAILED" >&2; exit 1; }
SR_GUARDRAIL=writer-a sr-session state set shared-key writer-A-value
exit 0
`
