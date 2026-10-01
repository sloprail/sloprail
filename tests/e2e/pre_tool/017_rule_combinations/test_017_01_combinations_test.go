// Package e2e drives COMBINATIONS of guardrails rather than single facts.
//
// The existing suites each isolate one property with one rule, which is what
// makes them readable and what makes them incomplete. A project does not
// declare one rule. It declares several, bound to overlapping kinds, narrowed
// by matches that agree about some events and disagree about others — and the
// interactions are where an engine that passes every isolated test still gets
// it wrong.
//
// The invariants these bear on:
//
//   - hook_within_binding: several guardrails on one kind, one guardrail on
//     several kinds, and a match admitting some occurrences of one kind and
//     not others.
//   - order_within_binding: "between Guardrails, no order is promised" — so
//     these tests assert what is true regardless of the order rules are walked
//     in, and deliberately never assert the order itself.
//
// That last point governs several assertions below. It would be easy to write a
// stronger-looking test that pinned which of two rules ran first; it would also
// be wrong, because the spec explicitly declines to promise it and the test
// would fail the day the walk changed for a good reason.
//
// Every rule here acts at the PRE-ACTION moment (a pending write or command
// blocked, or a permitting rule recording what it was asked), so the vehicle is
// a GATE. Several gates on one event stand in for several guardrails on one kind;
// one gate with several `on` triggers stands in for one guardrail on several
// kinds — including PreCommandInvoke, which only a gate (not a file-guard) can
// trigger on. The checks read the FLAT payload (`.event.path`, `.event.kind`) and
// their ledgers are read with e.GateLedgerLines from `.sloprail/gate/<name>/`.
// The pre-tool dispatch runs gates in name order and stops at the first refusal,
// exactly the "first refusal ends the matter" the old path took — which is what
// T017_07 pins.
package e2e

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// logPath records the path each event named, one line per question asked, into
// the gate's own folder. Reads `.event.path` FLAT.
const logPath = `#!/bin/sh
p="$(cat)"
printf '%s\n' "$p" | sed 's/.*"path":"\([^"]*\)".*/\1/' >> "$SR_GUARDRAIL_DIR/log"
exit 0
`

// logKind records the whole FLAT payload each event was, which is what a rule
// bound to several kinds is asked to distinguish.
const logKind = `#!/bin/sh
p="$(cat)"
printf '%s\n' "$p" >> "$SR_GUARDRAIL_DIR/log"
exit 0
`

// kindsSeen reads the event kinds a check was handed, off its own ledger. The
// event's own fields are spread FLAT under `event` (`.event.kind`).
func kindsSeen(t *testing.T, lines []string) []string {
	t.Helper()
	var kinds []string
	for _, line := range lines {
		var got struct {
			Event struct {
				Kind string `json:"kind"`
			} `json:"event"`
		}
		require.NoError(t, json.Unmarshal([]byte(line), &got),
			"a check was handed something that is not an event payload: %s", line)
		kinds = append(kinds, got.Event.Kind)
	}
	return kinds
}

// T017_01: several guardrails bound to ONE kind are all asked about it.
//
// Three gates, all triggering on PreFileCreate, all permitting. Each must see the
// event. The failure this catches is a dispatch that stops after the first rule
// that has an opinion — which would look correct in every single-rule suite and
// would silently disarm every rule but one in a real project.
//
// All three permit deliberately. A refusal ends the dispatch for that event
// (the first refusal ends the matter), so a refusing rule among them would make
// the others' silence ambiguous — unasked and asked-then-cut-short are
// different, and only an all-permitting set tells them apart.
func TestT017_01_SeveralGuardrailsOnOneKindAreAllAsked(t *testing.T) {
	e := New(t)
	proj := e.Project()
	for _, name := range []string{"alpha", "beta", "gamma"} {
		e.Gate(proj, name, `on:
  - event: PreFileCreate
checks:
  - script: ./h.sh
`, map[string]string{"h.sh": logPath})
	}

	e.Run(proj, "s-017-01", "write a note", Turns("done",
		Write("w1", "notes.md", "hello"),
	))

	for _, name := range []string{"alpha", "beta", "gamma"} {
		assert.Equal(t, []string{"notes.md"}, e.GateLedgerLines(proj, name, "log"),
			"every gate triggering on the kind must be asked about the event: %s", name)
	}
}

// T017_02: one guardrail bound to SEVERAL kinds sees each of them, and is told
// which is which.
//
// The mirror of T017_01, and the case the spec calls out directly: "A guardrail
// may bind to several events, which is what lets one rule guard an action
// before it happens and the result after it settles."
//
// Both halves are asserted. That the rule was asked three times is the weaker
// claim; that the three questions were DIFFERENT KINDS is the one that matters,
// because a rule bound to three kinds and handed the same kind three times
// cannot tell a create from a command and its script would be unwritable. A gate
// with three `on` triggers is the vehicle — and a command kind (PreCommandInvoke)
// is one only a gate can name.
func TestT017_02_OneGuardrailOnSeveralKindsIsToldWhichIsWhich(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Gate(proj, "everything", `on:
  - event: PreFileCreate
  - event: PreFileUpdate
  - event: PreCommandInvoke
checks:
  - script: ./h.sh
`, map[string]string{"h.sh": logKind})

	// The file the update turn changes has to exist first, or that turn is a
	// second creation and the kind under test never fires.
	e.WriteFile(proj, "exists.md", "original")

	e.Run(proj, "s-017-02", "create, change, run", Turns("done",
		Write("t1", "fresh.md", "new"),
		Write("t2", "exists.md", "changed"),
		Bash("t3", "true --flag"),
	))

	assert.Equal(t,
		[]string{"PreFileCreate", "PreFileUpdate", "PreCommandInvoke"},
		kindsSeen(t, e.GateLedgerLines(proj, "everything", "log")),
		"one rule bound to three kinds must be handed each of them, distinguishable")
}

// T017_03: a match admits some occurrences of a kind and not others, within
// one session.
//
// hook_within_binding at the granularity that actually bites. 001 proves a
// match admits one path and rejects another across two separate runs; this
// puts admitted and rejected events in the SAME session, interleaved, which is
// how a real match is exercised and where a match evaluated once and cached
// would show up.
//
// The interleaving is the point: admitted, rejected, admitted. A match
// evaluated once and reused would produce three identical answers, and either
// order of two events could hide that.
func TestT017_03_AMatcherSplitsOccurrencesOfOneKindInOneSession(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Gate(proj, "docs-only", `on:
  - event: PreFileCreate
    match: event.path endsWith ".md"
checks:
  - script: ./h.sh
`, map[string]string{"h.sh": logPath})

	e.Run(proj, "s-017-03", "md, txt, md", Turns("done",
		Write("t1", "one.md", "a"),
		Write("t2", "two.txt", "b"),
		Write("t3", "three.md", "c"),
	))

	assert.Equal(t, []string{"one.md", "three.md"}, e.GateLedgerLines(proj, "docs-only", "log"),
		"the match must admit and reject occurrences of the same kind independently")

	// Everything landed: the narrowed rule permits what it admits, and never
	// touched what it did not. Without this the ledger above is satisfied by a
	// rule that refused the .txt write and was simply never logged.
	for _, f := range []string{"one.md", "two.txt", "three.md"} {
		assert.True(t, e.Exists(proj, f), "a permitting rule must leave the tree alone: %s", f)
	}
}

// T017_04: one rule refuses an event another permits, and the refusal governs.
//
// Two gates, same kind, same event, opposite verdicts. The work must be
// prevented — a permit is not a veto over a refusal, or any rule could disarm
// every other by declaring itself permissive.
//
// Asserted on the TREE rather than on the stream. "A refusal was reported" and
// "the work was prevented" are different claims, and only the second is what a
// guardrail is for. A rule that reported a refusal while the file landed would
// satisfy a stream-only assertion completely.
func TestT017_04_ARefusalGovernsOverAPermit(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Gate(proj, "permits", `on:
  - event: PreFileCreate
checks:
  - script: ./h.sh
`, map[string]string{"h.sh": "#!/bin/sh\ncat >/dev/null\nexit 0\n"})
	e.Gate(proj, "refuses", `on:
  - event: PreFileCreate
checks:
  - script: ./h.sh
`, map[string]string{"h.sh": "#!/bin/sh\ncat >/dev/null\necho '{\"reason\":\"this rule says no\"}'\nexit 1\n"})

	res := e.Run(proj, "s-017-04", "write a note", Turns("done",
		Write("w1", "notes.md", "hello"),
	))

	assert.True(t, res.Refused(), "a refusal must not be cancelled by another rule permitting")
	assert.True(t, res.Saw("this rule says no"), "and the refusing rule's reason must travel")
	assert.False(t, e.Exists(proj, "notes.md"),
		"the work must actually be prevented, not merely reported as refused")
}

// T017_05: two rules narrowed to different scopes each govern their own.
//
// The combination a real project has: rules that are not about the same things.
// One guards `src/`, the other guards `docs/`, and three writes exercise each
// scope plus a path neither claims.
//
// What this catches is a match whose scope leaks across guardrails — a shared
// compiled match, or a scope resolved from the wrong declaration. Every
// single-rule test would pass with that defect present, because it takes two
// narrowed rules in one project to observe it.
//
// The unclaimed path is what makes it a scope test rather than a refusal test.
// Without `other/notes.md` the pair could be refusing everything between them
// and the assertions would not know.
func TestT017_05_TwoNarrowedRulesEachGovernTheirOwnScope(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Gate(proj, "src-guard", `on:
  - event: PreFileCreate
    match: event.path startsWith "src/"
checks:
  - script: ./h.sh
`, map[string]string{"h.sh": "#!/bin/sh\ncat >/dev/null\necho '{\"reason\":\"src is closed\"}'\nexit 1\n"})
	e.Gate(proj, "docs-guard", `on:
  - event: PreFileCreate
    match: event.path startsWith "docs/"
checks:
  - script: ./h.sh
`, map[string]string{"h.sh": "#!/bin/sh\ncat >/dev/null\necho '{\"reason\":\"docs is closed\"}'\nexit 1\n"})

	res := e.Run(proj, "s-017-05", "src, docs, neither", Turns("done",
		Write("t1", "src/main.go", "a"),
		Write("t2", "docs/guide.md", "b"),
		Write("t3", "other/notes.md", "c"),
	))

	require.True(t, res.Saw("src is closed"), "the src rule must refuse its own scope")
	require.True(t, res.Saw("docs is closed"), "the docs rule must refuse its own scope")

	assert.False(t, e.Exists(proj, "src/main.go"), "src/ is refused")
	assert.False(t, e.Exists(proj, "docs/guide.md"), "docs/ is refused")
	assert.True(t, e.Exists(proj, "other/notes.md"),
		"a path neither rule claims must be left alone — scopes must not leak between guardrails")
}
