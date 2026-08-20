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

// TestExtractAnswerPullsOnlyTheAnswer: from `The user answered:
// "<question>"="<answer>". ...`, only the <answer> is the user's word — the
// question is the agent's and the trailing sentence is the harness's.
func TestExtractAnswerPullsOnlyTheAnswer(t *testing.T) {
	// Verbatim shape from a real transcript.
	envelope := `The user answered: "Which invariant should get an emoji, and what's the intent?"="#1 - but name of invariant owning this probably wrongly described". Read the answers carefully — they may request clarification, changes, or that you not proceed — and follow what they actually say.`

	answer, ok := extractAnswer(envelope)
	require.True(t, ok, "this is an answer envelope")
	assert.Equal(t, "#1 - but name of invariant owning this probably wrongly described", answer)
	assert.NotContains(t, answer, "Which invariant", "the question is not the user's words")
	assert.NotContains(t, answer, "Read the answers", "the harness's instruction is not the user's words")
}

// TestExtractAnswerNotAnEnvelope: an ordinary tool_result body — a command's
// output that merely mentions the phrase — is not an answer envelope and reports
// false, so its text is never searched as the user's words.
func TestExtractAnswerNotAnEnvelope(t *testing.T) {
	_, ok := extractAnswer("total 42\n-rw-r--r-- 1 user staff file.go")
	assert.False(t, ok, "a plain tool result is not an answer envelope")

	_, ok = extractAnswer("The user answered without the join shape")
	assert.False(t, ok, "the prefix alone, without the \"=\" join, is not an envelope")
}

// TestExtractAnswerKeepsQuotesInsideTheAnswer: an answer that itself contains a
// quoted phrase is kept whole — the close is the last quote before the trailing
// sentence, so an inner quote does not truncate the user's words.
func TestExtractAnswerKeepsQuotesInsideTheAnswer(t *testing.T) {
	envelope := `The user answered: "what should it say?"="call it \"draft\" mode". Read the answers carefully.`
	answer, ok := extractAnswer(envelope)
	require.True(t, ok)
	assert.Contains(t, answer, `draft`, "an inner quoted phrase is kept")
	assert.Contains(t, answer, "call it", "the answer is not truncated at the first inner quote")
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
