package transcript

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func entriesFor(t *testing.T, lines ...string) []Entry {
	t.Helper()
	p := newProject(t)
	entries, err := Read(p.write("a-session", lines...))
	require.NoError(t, err, "Read")
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
	require.NoError(t, err, "Filter")
	for _, e := range got {
		assert.False(t, e.IsSidechain, "a sub-agent's entry %q answered for the main line", e.UUID)
	}
	assert.Len(t, got, 2, "want the entries on the main line")

	withSubs, err := Filter(entries, Query{IncludeSidechains: true})
	require.NoError(t, err, "Filter")
	assert.Len(t, withSubs, 3, "a rule asking whether work was delegated means precisely those")
}

// TestFilterWhereReadsTheEntry: the expression sees the canonical names, so a
// rule author learns one syntax rather than two.
func TestFilterWhereReadsTheEntry(t *testing.T) {
	entries := entriesFor(t, root("u1"), record("u2", "u1"), record("u3", "u2"))

	got, err := Filter(entries, Query{Where: `type == "assistant"`})
	require.NoError(t, err, "Filter")
	assert.Len(t, got, 2, "want the assistant entries")

	byUUID, err := Filter(entries, Query{Where: `uuid == "u1"`})
	require.NoError(t, err, "Filter")
	require.Len(t, byUUID, 1)
	assert.Equal(t, "u1", byUUID[0].UUID)
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
	require.NoError(t, err, "Filter")
	require.Len(t, got, 1, "want just the tool call")
	assert.Equal(t, "u2", got[0].UUID)

	result, err := Filter(entries, Query{Where: `toolUseResult?.stdout contains "deleted"`})
	require.NoError(t, err, "Filter")
	require.Len(t, result, 1, "want just the result entry")
	assert.Equal(t, "u3", result[0].UUID)
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
	require.NoError(t, err, "an entry of another shape must not fail the whole answer")
	require.Len(t, got, 1, "want just the tool call")
	assert.Equal(t, "u2", got[0].UUID)
}

// TestFilterErrorsWhenTheExpressionNeverRan is the counterweight to the
// shape-mismatch leniency above, and closes the hole it opened. Letting every
// evaluation failure be a non-match meant a rule broken for ALL entries
// reported no violations and exited successfully — the silently vacuous rule
// this product exists to prevent, produced by the product itself.
//
// One entry of another shape is ordinary. Not one entry evaluating is a rule
// that never looked, and it must say so.
func TestFilterErrorsWhenTheExpressionNeverRan(t *testing.T) {
	entries := entriesFor(t, root("u1"), record("u2", "u1"))

	_, err := Filter(entries, Query{Where: `int("notanumber") == 1`})
	require.Error(t, err, "a rule that fails on every entry must not report no violations")
	require.ErrorIs(t, err, ErrExpressionNeverRan)
	assert.Contains(t, err.Error(), "without having looked")
}

// TestFilterStillAnswersWhenSomeEntriesEvaluate: the check is "did anything
// evaluate", not "did everything". A rule that legitimately looks at one shape
// of entry is the ordinary case and must keep working.
func TestFilterStillAnswersWhenSomeEntriesEvaluate(t *testing.T) {
	entries := entriesFor(t,
		`{"type":"user","uuid":"u1","parentUuid":null,"isSidechain":false,"timestamp":"t",`+
			`"message":{"role":"user","content":"words"}}`,
		`{"type":"assistant","uuid":"u2","parentUuid":"u1","isSidechain":false,"timestamp":"t",`+
			`"message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash"}]}}`,
	)

	got, err := Filter(entries, Query{Where: `message?.content[0]?.name == "Bash"`})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "u2", got[0].UUID)
}

// TestFilterAnExpressionThatRanAndSaidNo is the case that must NOT be mistaken
// for a rule that never ran. `1/0` is not an error in this expression
// language — division is float, so it is +Inf, and `+Inf == 1` is a real,
// successful "no". Erring on it would refuse a working rule.
func TestFilterAnExpressionThatRanAndSaidNo(t *testing.T) {
	entries := entriesFor(t, root("u1"), record("u2", "u1"))

	got, err := Filter(entries, Query{Where: `1/0 == 1`})
	require.NoError(t, err, "an expression that evaluated successfully to false is a working rule, not a broken one")
	assert.Empty(t, got)
}

// TestFilterAnEmptySessionIsNotAVacuousRule: with no entries to offer, there is
// nothing for an expression to fail on, and the answer is simply empty. The
// check is about a rule that could not look, not about a session with nothing
// in it.
func TestFilterAnEmptySessionIsNotAVacuousRule(t *testing.T) {
	got, err := Filter(nil, Query{Where: `int("notanumber") == 1`})
	require.NoError(t, err)
	assert.Empty(t, got)
}

// TestFilterAbsenceIsAnAnswer: asking about something that never happened is an
// empty answer rather than an error. Absence is a legitimate finding, and often
// the one a rule is looking for.
func TestFilterAbsenceIsAnAnswer(t *testing.T) {
	entries := entriesFor(t, root("u1"), record("u2", "u1"))

	got, err := Filter(entries, Query{Where: `type == "nothing-like-this"`})
	require.NoError(t, err, "Filter")
	assert.Empty(t, got)
	// Empty rather than null, so a hook piping this into a JSON tool gets a
	// list to iterate over instead of something it has to special-case.
	blob, err := json.Marshal(got)
	require.NoError(t, err, "marshal")
	assert.JSONEq(t, "[]", string(blob), "an empty answer must be a list to iterate, not null")
}

// TestFilterRefusesAnExpressionThatWillNotCompile: a rule that silently never
// fires is worse than one that will not load, because the first looks like a
// rule being satisfied.
func TestFilterRefusesAnExpressionThatWillNotCompile(t *testing.T) {
	entries := entriesFor(t, root("u1"))
	_, err := Filter(entries, Query{Where: `type ==`})
	require.Error(t, err, "an expression that will not compile must be an error, not a filter admitting nothing")

	_, err = Filter(entries, Query{Where: `uuid`})
	require.Error(t, err, "an expression that is not a boolean must be an error")
	// A name no entry has is caught here rather than silently matching nothing
	// later — the same reason a guardrail's matcher is refused at load.
	_, err = Filter(entries, Query{Where: `toolName == "Bash"`})
	require.Error(t, err, "an expression naming a field that does not exist must be an error")
}

// TestFilterAsksAboutTypeByName pins that `type` is readable at all. It is also
// one of the expression language's own builtins, so an environment that did not
// declare it would let the builtin win and refuse the most obvious question
// anyone would ask of an entry.
func TestFilterAsksAboutTypeByName(t *testing.T) {
	entries := entriesFor(t, root("u1"), record("u2", "u1"))
	got, err := Filter(entries, Query{Where: `type == "user"`})
	require.NoError(t, err, "Filter on type")
	require.Len(t, got, 1, "want the user entry")
	assert.Equal(t, "u1", got[0].UUID)
}

// TestSinceStartsAfterTheMark: entries up to and including the mark have been
// judged; everything after it is this cycle's work.
func TestSinceStartsAfterTheMark(t *testing.T) {
	entries := entriesFor(t, root("u1"), record("u2", "u1"), record("u3", "u2"))

	got := Since(entries, "u2")
	require.Len(t, got, 1, "want only what came after the mark")
	assert.Equal(t, "u3", got[0].UUID)
}

// TestSinceWithNoMarkGivesEverything: a first cycle has read nothing, so the
// whole session is its work — correct rather than a special case.
func TestSinceWithNoMarkGivesEverything(t *testing.T) {
	entries := entriesFor(t, root("u1"), record("u2", "u1"))
	assert.Len(t, Since(entries, ""), 2, "a first cycle reads the whole session")
}

// TestSinceWithAMarkThatIsNotHereGivesEverything: the record it pointed into is
// gone or belongs to another conversation, and the safe reading of a lost
// position is to look again. Re-reading a turn costs a second look; skipping
// one loses a violation for good.
func TestSinceWithAMarkThatIsNotHereGivesEverything(t *testing.T) {
	entries := entriesFor(t, root("u1"), record("u2", "u1"))
	assert.Len(t, Since(entries, "from-another-conversation"), 2, "a lost position means looking again, not skipping")
}

// TestSinceTheLastEntryGivesNothing: a cycle in which nothing new happened is
// an empty answer, not the session over again.
func TestSinceTheLastEntryGivesNothing(t *testing.T) {
	entries := entriesFor(t, root("u1"), record("u2", "u1"))
	assert.Empty(t, Since(entries, "u2"), "a cycle in which nothing new happened is an empty answer")
}

// TestMarkIsWhereReadingStopped, and is empty when there was nothing to read —
// which leaves the previous mark standing rather than resetting it.
func TestMarkIsWhereReadingStopped(t *testing.T) {
	entries := entriesFor(t, root("u1"), record("u2", "u1"))
	assert.Equal(t, "u2", Mark(entries), "the mark is the last entry read")
	assert.Empty(t, Mark(nil), "nothing read leaves the previous mark standing")
}
