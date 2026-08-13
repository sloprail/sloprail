package transcript

import (
	"encoding/json"
	"testing"
)

func entriesFor(t *testing.T, lines ...string) []Entry {
	t.Helper()
	p := newProject(t)
	entries, err := Read(p.write("a-session", lines...))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	return entries
}

// TestFilterExcludesSidechainsByDefault: a rule asking what the agent did
// usually means the main line of work, and delegated work would otherwise
// answer for it.
func TestFilterExcludesSidechainsByDefault(t *testing.T) {
	entries := entriesFor(t,
		root("main-1"),
		`{"type":"assistant","uuid":"sub-1","parentUuid":"main-1","isSidechain":true,"timestamp":"t"}`,
		record("main-2", "main-1"),
	)

	got, err := Filter(entries, Query{})
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}
	for _, e := range got {
		if e.IsSidechain {
			t.Fatalf("a sub-agent's entry %q answered for the main line", e.UUID)
		}
	}
	if len(got) != 2 {
		t.Fatalf("Filter returned %d entries, want the 2 on the main line", len(got))
	}

	withSubs, err := Filter(entries, Query{IncludeSidechains: true})
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}
	if len(withSubs) != 3 {
		t.Fatalf("Filter with sidechains returned %d, want 3 — a rule asking whether work was delegated means precisely those", len(withSubs))
	}
}

// TestFilterWhereReadsTheEntry: the expression sees the canonical names, so a
// rule author learns one syntax rather than two.
func TestFilterWhereReadsTheEntry(t *testing.T) {
	entries := entriesFor(t, root("u1"), record("u2", "u1"), record("u3", "u2"))

	got, err := Filter(entries, Query{Where: `type == "assistant"`})
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Filter returned %d entries, want the 2 assistant ones", len(got))
	}

	byUUID, err := Filter(entries, Query{Where: `uuid == "u1"`})
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}
	if len(byUUID) != 1 || byUUID[0].UUID != "u1" {
		t.Fatalf("Filter by uuid returned %+v", byUUID)
	}
}

// TestFilterWhereReachesIntoAMessage: an expression asking what a tool returned
// wants to reach inside it, so the message and the tool result are decoded
// rather than handed over as text the expression would have to parse itself.
func TestFilterWhereReachesIntoAMessage(t *testing.T) {
	entries := entriesFor(t,
		root("u1"),
		`{"type":"assistant","uuid":"u2","parentUuid":"u1","isSidechain":false,"timestamp":"t",`+
			`"message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"rm -rf /"}}]}}`,
		`{"type":"user","uuid":"u3","parentUuid":"u2","isSidechain":false,"timestamp":"t",`+
			`"toolUseResult":{"stdout":"deleted everything"}}`,
	)

	got, err := Filter(entries, Query{Where: `message?.content[0]?.name == "Bash"`})
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}
	if len(got) != 1 || got[0].UUID != "u2" {
		t.Fatalf("Filter over a message's contents returned %+v, want just the tool call", got)
	}

	result, err := Filter(entries, Query{Where: `toolUseResult?.stdout contains "deleted"`})
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}
	if len(result) != 1 || result[0].UUID != "u3" {
		t.Fatalf("Filter over a tool result returned %+v, want just the result entry", result)
	}
}

// TestFilterEntriesOfAnotherShapeAreNotMatches pins a decision the fixtures
// forced. Entries are genuinely not all the same shape: a user's turn carries
// its content as a string, an assistant's as a list of blocks. So the most
// ordinary question anyone would ask — which tool did it call — indexes a
// string on every user turn and reaches for a field of a byte.
//
// Such an entry is not a match, and not an error. Erroring would fail the
// command on every real session and leave the rule author writing the
// shape-guarding themselves in every expression, which is exactly the per-rule
// reimplementation of the traversal this command exists to absorb.
func TestFilterEntriesOfAnotherShapeAreNotMatches(t *testing.T) {
	entries := entriesFor(t,
		// A user turn: content is a plain string.
		`{"type":"user","uuid":"u1","parentUuid":null,"isSidechain":false,"timestamp":"t",`+
			`"message":{"role":"user","content":"just some words"}}`,
		// An assistant turn: content is a list of blocks.
		`{"type":"assistant","uuid":"u2","parentUuid":"u1","isSidechain":false,"timestamp":"t",`+
			`"message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash"}]}}`,
	)

	got, err := Filter(entries, Query{Where: `message?.content[0]?.name == "Bash"`})
	if err != nil {
		t.Fatalf("Filter: %v — an entry of another shape must not fail the whole answer", err)
	}
	if len(got) != 1 || got[0].UUID != "u2" {
		t.Fatalf("Filter returned %+v, want just the tool call", got)
	}
}

// TestFilterAbsenceIsAnAnswer: asking about something that never happened is an
// empty answer rather than an error. Absence is a legitimate finding, and often
// the one a rule is looking for.
func TestFilterAbsenceIsAnAnswer(t *testing.T) {
	entries := entriesFor(t, root("u1"), record("u2", "u1"))

	got, err := Filter(entries, Query{Where: `type == "nothing-like-this"`})
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Filter returned %d entries, want none", len(got))
	}
	// Empty rather than null, so a hook piping this into a JSON tool gets a
	// list to iterate over instead of something it has to special-case.
	blob, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(blob) != "[]" {
		t.Fatalf("an empty answer serialises to %s, want []", blob)
	}
}

// TestFilterRefusesAnExpressionThatWillNotCompile: a rule that silently never
// fires is worse than one that will not load, because the first looks like a
// rule being satisfied.
func TestFilterRefusesAnExpressionThatWillNotCompile(t *testing.T) {
	entries := entriesFor(t, root("u1"))
	if _, err := Filter(entries, Query{Where: `type ==`}); err == nil {
		t.Fatal("an expression that will not compile must be an error, not a filter admitting nothing")
	}
	if _, err := Filter(entries, Query{Where: `uuid`}); err == nil {
		t.Fatal("an expression that is not a boolean must be an error")
	}
	// A name no entry has is caught here rather than silently matching nothing
	// later — the same reason a guardrail's matcher is refused at load.
	if _, err := Filter(entries, Query{Where: `toolName == "Bash"`}); err == nil {
		t.Fatal("an expression naming a field that does not exist must be an error")
	}
}

// TestFilterAsksAboutTypeByName pins that `type` is readable at all. It is also
// one of the expression language's own builtins, so an environment that did not
// declare it would let the builtin win and refuse the most obvious question
// anyone would ask of an entry.
func TestFilterAsksAboutTypeByName(t *testing.T) {
	entries := entriesFor(t, root("u1"), record("u2", "u1"))
	got, err := Filter(entries, Query{Where: `type == "user"`})
	if err != nil {
		t.Fatalf("Filter on type: %v", err)
	}
	if len(got) != 1 || got[0].UUID != "u1" {
		t.Fatalf("Filter on type returned %+v, want the user entry", got)
	}
}

// TestSinceStartsAfterTheMark: entries up to and including the mark have been
// judged; everything after it is this cycle's work.
func TestSinceStartsAfterTheMark(t *testing.T) {
	entries := entriesFor(t, root("u1"), record("u2", "u1"), record("u3", "u2"))

	got := Since(entries, "u2")
	if len(got) != 1 || got[0].UUID != "u3" {
		t.Fatalf("Since = %+v, want only what came after the mark", got)
	}
}

// TestSinceWithNoMarkGivesEverything: a first cycle has read nothing, so the
// whole session is its work — correct rather than a special case.
func TestSinceWithNoMarkGivesEverything(t *testing.T) {
	entries := entriesFor(t, root("u1"), record("u2", "u1"))
	if got := Since(entries, ""); len(got) != 2 {
		t.Fatalf("Since with no mark returned %d entries, want all of them", len(got))
	}
}

// TestSinceWithAMarkThatIsNotHereGivesEverything: the record it pointed into is
// gone or belongs to another conversation, and the safe reading of a lost
// position is to look again. Re-reading a turn costs a second look; skipping
// one loses a violation for good.
func TestSinceWithAMarkThatIsNotHereGivesEverything(t *testing.T) {
	entries := entriesFor(t, root("u1"), record("u2", "u1"))
	if got := Since(entries, "from-another-conversation"); len(got) != 2 {
		t.Fatalf("Since with an unknown mark returned %d entries, want all of them", len(got))
	}
}

// TestSinceTheLastEntryGivesNothing: a cycle in which nothing new happened is
// an empty answer, not the session over again.
func TestSinceTheLastEntryGivesNothing(t *testing.T) {
	entries := entriesFor(t, root("u1"), record("u2", "u1"))
	if got := Since(entries, "u2"); len(got) != 0 {
		t.Fatalf("Since the last entry returned %+v, want nothing", got)
	}
}

// TestMarkIsWhereReadingStopped, and is empty when there was nothing to read —
// which leaves the previous mark standing rather than resetting it.
func TestMarkIsWhereReadingStopped(t *testing.T) {
	entries := entriesFor(t, root("u1"), record("u2", "u1"))
	if got := Mark(entries); got != "u2" {
		t.Fatalf("Mark = %q, want the last entry read", got)
	}
	if got := Mark(nil); got != "" {
		t.Fatalf("Mark of nothing = %q, want empty", got)
	}
}
