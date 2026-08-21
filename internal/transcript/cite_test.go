package transcript

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The answer-envelope extraction is the delicate half of cite, so it is tested
// on its own rather than only through a substring search: what counts as the
// user's words is a judgement about a shape, and a test naming the shape catches
// a regression a "did it match" test would let through.

// TestExtractAnswersPullsOnlyTheAnswer: from `The user answered:
// "<question>"="<answer>". ...`, only the <answer> is the user's word — the
// question is the agent's and the trailing sentence is the harness's.
func TestExtractAnswersPullsOnlyTheAnswer(t *testing.T) {
	// Verbatim shape from a real transcript.
	envelope := `The user answered: "Which invariant should get an emoji, and what's the intent?"="#1 - but name of invariant owning this probably wrongly described". Read the answers carefully — they may request clarification, changes, or that you not proceed — and follow what they actually say.`

	got := extractAnswers(envelope)
	require.Equal(t, []string{"#1 - but name of invariant owning this probably wrongly described"}, got)
	assert.NotContains(t, got[0], "Which invariant", "the question is not the user's words")
	assert.NotContains(t, got[0], "Read the answers", "the harness's instruction is not the user's words")
}

// TestExtractAnswersMultiQuestion is the regression the reviewer caught: one
// AskUserQuestion call routinely asks SEVERAL questions, and the harness writes
// every Q/A pair into ONE string. Each ANSWER must be extracted separately, and
// NO question text may survive — the old first-join-to-last-quote parse returned
// a single blob carrying the SECOND question, making the agent's own words
// citable.
func TestExtractAnswersMultiQuestion(t *testing.T) {
	// A real two-question envelope shape (decoded — literal quotes).
	two := `The user answered: "How should the a10n-evals work land relative to the existing PR #5?"="Close 5 and link to newly opened from main", "The a10n changes are on claude/spec-application-system-scope, 8 commits, unpushed. Open a PR there too?"="Yes, push and open PR". Read the answers carefully — they may request clarification.`

	got := extractAnswers(two)
	require.Equal(t, []string{
		"Close 5 and link to newly opened from main",
		"Yes, push and open PR",
	}, got, "each answer is extracted separately, and no question text leaks")
	// The agent's question text must appear in NONE of the extracted words.
	for _, a := range got {
		assert.NotContains(t, a, "How should the a10n-evals", "a question leaked into an answer")
		assert.NotContains(t, a, "Open a PR there too", "the second question leaked — the exact regression")
		assert.NotContains(t, a, "Read the answers", "the trailer leaked")
	}

	// Three questions, to prove it is not a two-only fix.
	three := `The user answered: "Q1 pick one?"="alpha", "Q2 which colour?"="green", "Q3 proceed?"="yes go ahead". Read the answers carefully — x.`
	assert.Equal(t, []string{"alpha", "green", "yes go ahead"}, extractAnswers(three))
}

// TestExtractAnswersNotAnEnvelope: an ordinary tool_result body — a command's
// output that merely mentions the phrase — is not an answer envelope and yields
// nothing, so its text is never searched as the user's words.
func TestExtractAnswersNotAnEnvelope(t *testing.T) {
	assert.Empty(t, extractAnswers("total 42\n-rw-r--r-- 1 user staff file.go"),
		"a plain tool result is not an answer envelope")
	assert.Empty(t, extractAnswers("The user answered without the join shape"),
		"the prefix alone, without the \"=\" join, is not an envelope")
}

// TestExtractAnswersKeepsQuotesInsideTheAnswer: an answer that itself contains a
// quoted phrase is kept whole — an inner quote is not a pair boundary, because
// what follows it is not a `"="`-terminated question, so it does not truncate the
// user's words.
func TestExtractAnswersKeepsQuotesInsideTheAnswer(t *testing.T) {
	// Decoded shape: the inner quotes around draft are literal.
	envelope := `The user answered: "what should it say?"="call it "draft" mode". Read the answers carefully.`
	got := extractAnswers(envelope)
	require.Len(t, got, 1)
	assert.Contains(t, got[0], `draft`, "an inner quoted phrase is kept")
	assert.Contains(t, got[0], "call it", "the answer is not truncated at the first inner quote")
	assert.Contains(t, got[0], "mode", "the answer is not truncated after the inner quote either")
}

// TestExtractAnswersHandlesInnerCommaQuoteAndTruncation covers the brittle bits
// the reviewer flagged: an answer containing the literal `", "` sequence (which
// is NOT a pair boundary because no `"="`-terminated question follows it), and a
// truncated envelope with no trailer and no closing quote (the answer runs to the
// end of what was recorded).
func TestExtractAnswersHandlesInnerCommaQuoteAndTruncation(t *testing.T) {
	// The answer literally contains `", "` — but there is only one pair, so it is
	// all one answer, not split into a phantom second.
	inner := `The user answered: "which items?"="apples", "oranges" and pears". Read the answers carefully.`
	got := extractAnswers(inner)
	require.Len(t, got, 1, "a `\", \"` inside the only answer must not fabricate a second answer")
	assert.Contains(t, got[0], "apples")
	assert.Contains(t, got[0], "oranges")
	assert.Contains(t, got[0], "pears")

	// Truncated: no trailer, no closing quote. The answer is what was recorded.
	trunc := `The user answered: "Q1?"="A1 got cut off mid`
	assert.Equal(t, []string{"A1 got cut off mid"}, extractAnswers(trunc))

	// A question that itself contains quotes (real corpus shape) must still be
	// skipped, and only the answer kept.
	qquotes := `The user answered: "There's no entity describing "Entity" itself. What now?"="#2; backfill entities". Read the answers carefully — and`
	assert.Equal(t, []string{"#2; backfill entities"}, extractAnswers(qquotes),
		"inner quotes in the QUESTION must not derail the parse or leak")
}

// TestExtractAnswersNeverLeaksQuestionEvenWhenAnswerHasCommaQuote pins the SAFETY
// guarantee for the one genuinely-ambiguous shape: an answer that contains the
// literal `", "` sequence AND is followed by another pair. This does not occur in
// the measured corpus (0 of 53 multi-question envelopes), and it cannot be
// disambiguated from a flat string — the parser errs toward TRUNCATING the answer
// rather than toward keeping the following question, because a false citation of
// the agent's own words is the harm to avoid.
//
// The property asserted is therefore one-directional: whatever the parser keeps,
// it must contain NO question text. The answer may be truncated; a question must
// never appear.
func TestExtractAnswersNeverLeaksQuestionEvenWhenAnswerHasCommaQuote(t *testing.T) {
	// Answer 1 contains `", "`; a real second pair follows with a distinctive
	// question token QUESTIONWORD that must never be citable.
	envelope := `The user answered: "first q?"="a list: "one", "two" done", "the QUESTIONWORD second?"="clean answer". Read the answers carefully.`
	got := extractAnswers(envelope)
	require.NotEmpty(t, got)
	for _, a := range got {
		assert.NotContains(t, a, "QUESTIONWORD",
			"question text leaked into an answer — the exact harm the parser must never cause: %q", a)
		assert.NotContains(t, a, "second?",
			"the second question leaked: %q", a)
	}
	// The clean second answer is still recovered whole.
	assert.Contains(t, got, "clean answer", "the following answer must still be extracted")
}

// TestUserWordsPlainStringMessage: the ordinary typed message — content as a bare
// string — is the user's words.
func TestUserWordsPlainStringMessage(t *testing.T) {
	e := Entry{Type: EntryUser, Message: rawMessage(`{"role":"user","content":"please refactor the parser"}`)}
	words := userWords(e)
	require.NotEmpty(t, words)
	assert.Contains(t, words, "please refactor the parser")
}

// TestUserWordsListWithTextBlocks: a user message whose content is a list of text
// blocks contributes each block's text.
func TestUserWordsListWithTextBlocks(t *testing.T) {
	e := Entry{Type: EntryUser, Message: rawMessage(
		`{"role":"user","content":[{"type":"text","text":"first part"},{"type":"text","text":"second part"}]}`)}
	words := userWords(e)
	assert.Contains(t, words, "first part")
	assert.Contains(t, words, "second part")
}

// TestUserWordsAnswerFromToolResult: an AskUserQuestion answer arrives on a user
// entry as a tool_result envelope, and userWords extracts the answer from it —
// but not the surrounding tool_result body of a non-answer result.
func TestUserWordsAnswerFromToolResult(t *testing.T) {
	answerEntry := Entry{Type: EntryUser, Message: rawMessage(
		`{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1",` +
			`"content":"The user answered: \"proceed?\"=\"yes, go with option B\". Read carefully."}]}`)}
	words := userWords(answerEntry)
	found := false
	for _, w := range words {
		if w == "yes, go with option B" {
			found = true
		}
	}
	assert.True(t, found, "the answer text is extracted from the tool_result envelope: %v", words)

	// A non-answer tool_result contributes nothing.
	plainResult := Entry{Type: EntryUser, Message: rawMessage(
		`{"role":"user","content":[{"type":"tool_result","tool_use_id":"t2","content":"build succeeded in 3s"}]}`)}
	assert.Empty(t, userWords(plainResult),
		"an ordinary tool_result is not the user's words")
}

// TestCiteUniqueMatch: a quote in exactly one user message resolves to that line.
func TestCiteUniqueMatch(t *testing.T) {
	p := newProject(t)
	path := p.write("a-session",
		userMsg("u1", "please REFACTOR the auth module"), // line 1
		record("a1", "u1"),             // line 2
		userMsg("u2", "and add tests"), // line 3
	)

	matches, err := Cite(path, "auth module")
	require.NoError(t, err)
	require.Len(t, matches, 1)
	assert.Equal(t, path, matches[0].Path)
	assert.Equal(t, 1, matches[0].Line)
}

// TestCiteAmbiguousMatchesEveryUserLine: a quote in several user messages returns
// one candidate per entry, in file order.
func TestCiteAmbiguousMatchesEveryUserLine(t *testing.T) {
	p := newProject(t)
	path := p.write("a-session",
		userMsg("u1", "the TOKEN goes here"),                   // line 1
		assistantText("a1", "u1", "the TOKEN is echoed by me"), // line 2 — must NOT match
		userMsg("u2", "yes the TOKEN again"),                   // line 3
	)

	matches, err := Cite(path, "TOKEN")
	require.NoError(t, err)
	require.Len(t, matches, 2, "both user lines, and not the assistant line")
	assert.Equal(t, 1, matches[0].Line)
	assert.Equal(t, 3, matches[1].Line)
}

// TestCiteNoMatch: a quote in nothing the user said resolves to no candidates —
// including a quote that appears ONLY in an assistant turn.
func TestCiteNoMatch(t *testing.T) {
	p := newProject(t)
	path := p.write("a-session",
		userMsg("u1", "do the thing"),
		assistantText("a1", "u1", "I will use the SECRETMARKER internally"),
	)

	matches, err := Cite(path, "SECRETMARKER")
	require.NoError(t, err)
	assert.Empty(t, matches, "a quote only in the agent's own output is not citable")
}

// TestCiteExcludesHarnessInjectedUserMessages: a quote that appears only in a
// harness-injected user-role message — a <task-notification>, a <system-reminder>,
// or a slash-command envelope — is NOT citable. These arrive as `user` entries
// with plain string content (the same shape a typed message has) but are not the
// person's own words, so grounding a claim on them would be a false citation, the
// same as citing the agent's own output.
func TestCiteExcludesHarnessInjectedUserMessages(t *testing.T) {
	p := newProject(t)
	path := p.write("a-session",
		userMsg("u1", "<system-reminder>\nremember to POLICYWORD before ending\n</system-reminder>"),
		userMsg("u2", "[SYSTEM NOTIFICATION - NOT USER INPUT]\n<task-notification>POLICYWORD done</task-notification>"),
		userMsg("u3", "<command-name>/POLICYWORD</command-name>"),
		userMsg("u4", "please handle the POLICYWORD task"), // the ONLY genuine user line
	)

	matches, err := Cite(path, "POLICYWORD")
	require.NoError(t, err)
	// Only the real typed message (u4, physical line 4) is a candidate — the three
	// harness-injected messages carrying the same word are excluded.
	require.Len(t, matches, 1, "only the genuinely typed user message is citable")
	assert.Equal(t, 4, matches[0].Line)
}

// TestCiteMatchesAnAnswer: a quote landing on an AskUserQuestion answer resolves
// to the user entry carrying that answer envelope.
func TestCiteMatchesAnAnswer(t *testing.T) {
	p := newProject(t)
	path := p.write("a-session",
		userMsg("u1", "here is the task"), // line 1
		record("a1", "u1"),                // line 2
		// line 3: the answer envelope.
		`{"type":"user","uuid":"u2","parentUuid":"a1","isSidechain":false,"message":{"role":"user","content":[`+
			`{"type":"tool_result","tool_use_id":"t1","content":"The user answered: \"pick one\"=\"the second option please\". Read carefully."}]}}`,
	)

	matches, err := Cite(path, "second option")
	require.NoError(t, err)
	require.Len(t, matches, 1, "the answer is citable")
	assert.Equal(t, 3, matches[0].Line, "it resolves to the line the answer envelope sits on")
}

// TestCiteMatchIsPathAndLineOnly: a match carries exactly its location — Path and
// Line — and nothing more. The whole-envelope fetch that a judge-prepare piece
// needs for an answer-grounded change is NOT on the match; it is transcript.
// EnvelopeAt, called with a citation's path and line (see envelope_test.go). This
// pins the layering the reviewer asked for: cite mints the location, and the
// envelope is fetched separately by whoever needs it.
func TestCiteMatchIsPathAndLineOnly(t *testing.T) {
	p := newProject(t)
	envelope := `The user answered: "which approach for the auth rewrite?"="go with the second option please". Read carefully.`
	path := p.write("a-session",
		userMsg("u1", "here is the task"),
		record("a1", "u1"),
		`{"type":"user","uuid":"u2","parentUuid":"a1","isSidechain":false,"message":{"role":"user","content":[`+
			`{"type":"tool_result","tool_use_id":"t1","content":`+jsonQuote(envelope)+`}]}}`,
	)

	matches, err := Cite(path, "second option")
	require.NoError(t, err)
	require.Len(t, matches, 1)
	// The match is its location and nothing else: an answer-grounded match resolves
	// to the envelope's line, and the envelope itself is fetched via EnvelopeAt, not
	// carried here.
	assert.Equal(t, CitationMatch{Path: path, Line: 3}, matches[0],
		"the match is exactly {Path, Line}; the envelope is EnvelopeAt's to fetch")
}

// TestCiteEmptyQuoteMatchesNothing: an empty quote resolves nowhere rather than
// everywhere — a citation of "" is a mistake, not a request for every user line.
func TestCiteEmptyQuoteMatchesNothing(t *testing.T) {
	p := newProject(t)
	path := p.write("a-session", userMsg("u1", "anything"), userMsg("u2", "something"))

	matches, err := Cite(path, "")
	require.NoError(t, err)
	assert.Empty(t, matches, "an empty quote matches nothing")
}

// userMsg is a plain user message with string content on one line.
func userMsg(uuid, content string) string {
	return `{"type":"user","uuid":"` + uuid + `","parentUuid":null,"isSidechain":false,` +
		`"message":{"role":"user","content":` + jsonQuote(content) + `}}`
}

// assistantText is an assistant turn whose content is a text block — the agent's
// own words, which cite must never treat as citable.
func assistantText(uuid, parent, text string) string {
	return `{"type":"assistant","uuid":"` + uuid + `","parentUuid":"` + parent + `","isSidechain":false,` +
		`"message":{"role":"assistant","content":[{"type":"text","text":` + jsonQuote(text) + `}]}}`
}

// jsonQuote renders s as a JSON string literal for embedding in a fixture line.
func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
