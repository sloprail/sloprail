package e2e

import (
	"encoding/json"
	"testing"
)

// hook_judges_event: a check is given one event and decides about that event, and
// what reaches it is settled before it runs.
//
// Everything about which occurrences a rule sees belongs to the binding. A check
// that had to work out whether an occurrence was its own would be re-implementing
// its matcher, and the two would drift. Observably that means: one invocation
// carries exactly one event, never a batch to filter; the narrowing has already
// happened, so nothing the match excluded arrives; and the payload carries the
// event and the session facts, not the history a match is forbidden to read.
//
// # RE-VEHICLED onto the NEW file-guard nature (was old GUARDRAIL.md hooks)
//
// It used to install an OLD-format rule (`hooks: PreFileCreate: [matcher: path
// startsWith "guarded/"]`) whose hook read the NESTED payload (`.event.fields.path`)
// and asserted the surface was `{event, guardrailDir}`. The NEW dispatch hands a
// file-guard's check the FLAT CheckPayload (internal/declaration/payload.go): the
// event's own fields spread directly under `event` (`.event.path`, `.event.kind`,
// never `.event.fields.path`), and the payload's whole surface is `{event,
// transcriptPath, context}` — there is NO `guardrailDir` field (a check finds its
// folder via $SR_GUARDRAIL_DIR instead). So this re-proves the SAME invariant
// against the new payload shape: narrowing by `match`, a single flat event, and a
// surface a matcher cannot smuggle history through.
//
// The guard is PREVENTIVE and its check RECORDS then REFUSES, so the observation
// is the PRE file event and nothing lands (a landed write would also run the Stop
// after-check, mixing a Post event into the ledger). A denied pre-write is retried
// by the mock, so the ledger holds the SAME single event repeated; every assertion
// reads a representative line, and the claim is about the SHAPE of what the check
// is handed, not how many retries happened.

// narrowed is a NEW-FORMAT preventive file-guard that admits only what is under
// guarded/. Two writes go out per test, one admitted and one not, so "the check
// was handed only its own" is a claim with something to exclude rather than a
// description of the only write there was. It records what it is handed, then
// refuses (so nothing lands).
const narrowed = `match: "guarded/**"
preventive: true
checks:
  - script: ./record.sh
`

const recordScript = `#!/bin/sh
cat >> "$SR_GUARDRAIL_DIR/seen"
echo >> "$SR_GUARDRAIL_DIR/seen"
echo '{"reason":"recorded"}'
exit 1
`

type payload struct {
	Event struct {
		Kind string `json:"kind"`
		Path string `json:"path"`
	} `json:"event"`
}

func decode(t *testing.T, line string) payload {
	t.Helper()
	var p payload
	if err := json.Unmarshal([]byte(line), &p); err != nil {
		t.Fatalf("the check was handed something that is not an event payload: %v\n%s", err, line)
	}
	return p
}

// T009_01: each invocation carries exactly one event, already narrowed.
//
// Two writes, one inside the binding and one outside. The check must be handed the
// admitted occurrence alone — the FLAT event for guarded/notes.md — and never the
// excluded one.
func TestT009_01_CheckIsHandedOneAlreadyNarrowedEvent(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "narrow", narrowed, map[string]string{"record.sh": recordScript})

	e.Run(proj, "s-009-01", "write two notes", Turns("done",
		Write("w1", "guarded/notes.md", "hello"),
		Write("w2", "elsewhere/notes.md", "hello"),
	))

	lines := e.FileGuardLedgerLines(proj, "narrow", "seen")
	if len(lines) == 0 {
		t.Fatalf("the check never ran on the admitted write")
	}

	// The narrowing was applied BEFORE the check: every line it recorded is the
	// admitted path, and the excluded one never appears.
	for _, line := range lines {
		p := decode(t, line)
		if p.Event.Path != "guarded/notes.md" {
			t.Fatalf("the check was handed %q — the match's narrowing was not applied before it ran:\n%s", p.Event.Path, line)
		}
		// A pre file event, read FLAT: `.event.path` and `.event.kind` directly, not
		// through a nested `event.fields` envelope.
		if p.Event.Kind != "PreFileCreate" {
			t.Errorf("the check was handed kind %q, want PreFileCreate:\n%s", p.Event.Kind, line)
		}
	}

	// One event, not a list. A payload carrying a batch would make filtering the
	// check's job, which is the drift this invariant exists to prevent.
	var shape map[string]json.RawMessage
	if err := json.Unmarshal([]byte(lines[0]), &shape); err != nil {
		t.Fatalf("payload is not an object: %v", err)
	}
	if _, batched := shape["events"]; batched {
		t.Errorf("the check was handed a list of events to filter itself:\n%s", lines[0])
	}
	if _, single := shape["event"]; !single {
		t.Errorf("the payload carries no single event:\n%s", lines[0])
	}
}

// T009_02: the payload carries the event and the session facts, and no history to
// judge against.
//
// The spec's second reason: a matcher that could read what a rule remembered would
// be unverifiable when the guard loads, since the keys are the rule's own and
// unknown until it runs. So the NEW CheckPayload's whole surface is `{event,
// transcriptPath, context}` — the occurrence, the record it can query
// deliberately, and the declared contexts. What a rule remembered is fetched by
// the check on purpose (via sr-session state / $SR_GUARDRAIL_DIR), never pushed at
// it as something to match on. A key beyond these three is a fact a matcher could
// come to depend on.
func TestT009_02_PayloadCarriesTheEventAndNothingToMatchHistoryOn(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "narrow", narrowed, map[string]string{"record.sh": recordScript})

	e.Run(proj, "s-009-02", "write a note", Turns("done",
		Write("w1", "guarded/notes.md", "hello"),
	))

	lines := e.FileGuardLedgerLines(proj, "narrow", "seen")
	if len(lines) == 0 {
		t.Fatalf("the check never ran")
	}

	var shape map[string]json.RawMessage
	if err := json.Unmarshal([]byte(lines[0]), &shape); err != nil {
		t.Fatalf("payload is not an object: %v", err)
	}
	// The payload's whole surface. `event` carries the occurrence; `transcriptPath`
	// is the record a check queries deliberately; `context` is the declared contexts
	// (empty here). None of these is history a match reaches implicitly.
	for key := range shape {
		if key != "event" && key != "transcriptPath" && key != "context" {
			t.Errorf("the payload carries %q beyond the event, the transcript path and the context — a matcher could come to read it:\n%s", key, lines[0])
		}
	}
	// The event itself is present.
	if _, ok := shape["event"]; !ok {
		t.Errorf("the payload carries no event:\n%s", lines[0])
	}
}

// T009_03: a check bound to files is never handed a command event.
//
// What reaches a check is settled by its binding before the check runs. A
// file-guard matches a FILE's state, and the dispatch only ever hands it file
// events (nature_fileguard.go's isPreFileEvent) — a rule about files asked to judge
// a command event would have to detect and ignore it, re-implementing the routing
// the binding already declared. A write and a bash go out; only the write reaches
// the guard.
func TestT009_03_CheckIsNeverHandedACommandEvent(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	// Bound to markdown writes alone.
	const filesOnly = `match: "**/*.md"
preventive: true
checks:
  - script: ./record.sh
`
	e.FileGuard(proj, "files-only", filesOnly, map[string]string{"record.sh": recordScript})

	e.Run(proj, "s-009-03", "write then run", Turns("done",
		Write("w1", "some/notes.md", "hello"),
		Bash("b1", "npm publish --access public"),
	))

	lines := e.FileGuardLedgerLines(proj, "files-only", "seen")
	if len(lines) == 0 {
		t.Fatalf("the check never ran on the file write")
	}
	for _, line := range lines {
		if kind := decode(t, line).Event.Kind; kind != "PreFileCreate" {
			t.Fatalf("a check bound to files was handed %q — a command event reached a file rule:\n%s", kind, line)
		}
	}
}
