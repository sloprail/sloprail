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

// TestIsHumanQueuedCommand pins the classification decision: `commandMode` is
// the sole gate. `"prompt"` is the person typing (verbatim shape from a real
// transcript, humanTurn/origin included as they actually appear — confirming,
// not deciding), `"task-notification"` is the harness's own background-task
// notice, and anything not a queued_command attachment at all is neither.
func TestIsHumanQueuedCommand(t *testing.T) {
	human := `{"type":"queued_command","prompt":"do the thing","source_uuid":"s1",` +
		`"commandMode":"prompt","origin":{"kind":"human"},"humanTurn":true,"timestamp":"2026-09-25T10:07:21.363Z"}`
	_, ok := isHumanQueuedCommand(json.RawMessage(human))
	assert.True(t, ok, "commandMode:prompt is the person typing")

	// humanTurn is often absent on genuine human messages (older client
	// versions did not write it) — it must not be required.
	humanNoTurnFlag := `{"type":"queued_command","prompt":"do the other thing","source_uuid":"s2","commandMode":"prompt"}`
	_, ok = isHumanQueuedCommand(json.RawMessage(humanNoTurnFlag))
	assert.True(t, ok, "commandMode:prompt resolves even without humanTurn or origin present")

	notification := `{"type":"queued_command","prompt":"<task-notification>...</task-notification>","commandMode":"task-notification"}`
	_, ok = isHumanQueuedCommand(json.RawMessage(notification))
	assert.False(t, ok, "commandMode:task-notification is the harness, never the person")

	other := `{"type":"environment","snapshot":{}}`
	_, ok = isHumanQueuedCommand(json.RawMessage(other))
	assert.False(t, ok, "a non-queued_command attachment is not this at all")

	assert.False(t, func() bool { _, ok := isHumanQueuedCommand(nil); return ok }(),
		"no attachment payload is not a human queued command")
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

// TestCiteResolvesAMidTurnQueuedCommand is the regression for the bug this
// change fixes: a message the person sends WHILE a turn is already running
// arrives as an `attachment` entry (`attachment.type: "queued_command"`,
// `commandMode: "prompt"`), not a `type:"user"` message — and before this fix,
// cite's walk only ever looked at `type:"user"` entries, so it found nothing and
// silently refused every guardrail grounded in a mid-turn message.
func TestCiteResolvesAMidTurnQueuedCommand(t *testing.T) {
	p := newProject(t)
	path := p.write("a-session",
		userMsg("u1", "start the task"),                                                  // line 1
		toolUseMsg("a1", "u1", "Bash", "run"),                                            // line 2
		toolResultMsg("u2", "a1", "some tool output"),                                    // line 3
		queuedCommandMsg("q1", "u2", "also automatically detect the intent of the user"), // line 4
	)

	matches, err := Cite(path, "automatically detect the intent of the user")
	require.NoError(t, err)
	require.Len(t, matches, 1, "a genuinely human-typed mid-turn message resolves")
	assert.Equal(t, 4, matches[0].Line, "it cites the attachment line itself, not a queue-operation line")
}

// TestCiteQueuedCommandNotUnderToolResult: a human-typed queued_command resolves
// under the `user` pool, and must NOT resolve under `tool_result` — it is the
// person's own words, not a tool's output, and the two pools stay disjoint the
// same way an ordinary typed message never resolves under tool_result.
func TestCiteQueuedCommandNotUnderToolResult(t *testing.T) {
	p := newProject(t)
	path := p.write("a-session",
		userMsg("u1", "start the task"),
		queuedCommandMsg("q1", "u1", "please add a UNIQUEMARKER feature"),
	)

	userMatches, err := CiteWithSources(path, "UNIQUEMARKER", []SourceType{SourceUser})
	require.NoError(t, err)
	require.Len(t, userMatches, 1, "resolves under the user pool")
	assert.Equal(t, 2, userMatches[0].Line)

	toolResultMatches, err := CiteWithSources(path, "UNIQUEMARKER", []SourceType{SourceToolResult})
	require.NoError(t, err)
	assert.Empty(t, toolResultMatches, "a queued command is the user's words, not a tool_result — must not resolve here")
}

// TestCiteExcludesTaskNotificationQueuedCommand: a background task's completion
// notice is queued through the SAME queued_command attachment shape as a
// genuine mid-turn message, distinguished only by `commandMode`
// ("task-notification" vs "prompt"). It is the harness speaking, not the
// person, so it must not resolve — the same reasoning harnessInjected applies
// to a `<task-notification>` carried on a `type:"user"` record.
func TestCiteExcludesTaskNotificationQueuedCommand(t *testing.T) {
	p := newProject(t)
	path := p.write("a-session",
		userMsg("u1", "start the task"),
		taskNotificationAttachment("q1", "u1", "Background command MYTASKMARKER completed"),
		queuedCommandMsg("q2", "q1", "the real MYTASKMARKER message from me"), // the only genuine line
	)

	matches, err := Cite(path, "MYTASKMARKER")
	require.NoError(t, err)
	require.Len(t, matches, 1, "only the human-typed queued command is citable")
	assert.Equal(t, 3, matches[0].Line)
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

// --- SourceToolResult: grounding a delivery OBSERVATION in a tool's output ---
//
// The mirror of the user-pool tests above. A tool_result is written on a `user`
// entry as a `type:"tool_result"` content block; its body is the tool's output —
// a test that came back green, a command's result. cite --source-types tool_result
// resolves a quote against THAT body (the pool the default refuses), and refuses a
// quote that is only the user's words. These are the two halves of the mirror.

// TestCiteToolResultResolvesToolOutput: a quote of a command's RESULT resolves
// under SourceToolResult to the user entry carrying the tool_result — and does NOT
// resolve under the default SourceUser, which is the pool a delivery observation
// needs and the default deliberately excludes.
func TestCiteToolResultResolvesToolOutput(t *testing.T) {
	p := newProject(t)
	path := p.write("a-session",
		userMsg("u1", "run the tests"),                             // line 1 — the user's words
		toolUseMsg("a1", "u1", "Bash", "go test ./..."),            // line 2 — the agent's call
		toolResultMsg("u2", "a1", "ok  sloprail/auth  0.4s\nPASS"), // line 3 — the RESULT
	)

	// tool_result pool: the green result resolves to its line.
	got, err := CiteWithSources(path, "PASS", []SourceType{SourceToolResult})
	require.NoError(t, err)
	require.Len(t, got, 1, "the tool result is citable under the tool_result pool")
	assert.Equal(t, 3, got[0].Line, "it resolves to the line the tool_result sits on")

	// user pool (the default): the SAME quote must NOT resolve — a tool's output is
	// not the user's words, which is exactly why observations need the other pool.
	none, err := Cite(path, "PASS")
	require.NoError(t, err)
	assert.Empty(t, none, "the default user pool refuses a tool result — the gap observations fill")
}

// TestCiteToolResultRefusesUserWords is the other half of the mirror: a quote that
// is only in a USER message does not resolve under SourceToolResult. An
// "observation" citing the user's ask instead of a tool result is a mis-citation,
// and the tool_result pool refuses it so the deterministic guard can catch it.
func TestCiteToolResultRefusesUserWords(t *testing.T) {
	p := newProject(t)
	path := p.write("a-session",
		userMsg("u1", "please migrate the AUTHMODULE"),             // line 1 — user only
		toolResultMsg("u2", "u1", "migrated 3 files successfully"), // line 2 — tool output
	)

	got, err := CiteWithSources(path, "AUTHMODULE", []SourceType{SourceToolResult})
	require.NoError(t, err)
	assert.Empty(t, got, "the user's ask is not a tool result — it must not resolve in the tool_result pool")

	// And it DOES resolve in the user pool, proving the quote is real and only the
	// pool selection kept it out above.
	inUser, err := Cite(path, "AUTHMODULE")
	require.NoError(t, err)
	require.Len(t, inUser, 1, "the same quote resolves as the user's words")
	assert.Equal(t, 1, inUser[0].Line)
}

// TestCiteBothPoolsResolveEither: --source-types user,tool_result accepts a match
// in either pool. A quote of the user's ask and a quote of a tool's result both
// resolve, each to its own line.
func TestCiteBothPoolsResolveEither(t *testing.T) {
	p := newProject(t)
	path := p.write("a-session",
		userMsg("u1", "run the BUILD and report"),              // line 1 — user
		toolUseMsg("a1", "u1", "Bash", "make"),                 // line 2 — the call
		toolResultMsg("u2", "a1", "BUILD succeeded: 0 errors"), // line 3 — tool result
	)
	both := []SourceType{SourceUser, SourceToolResult}

	// The user's word resolves (it is in the user pool).
	fromUser, err := CiteWithSources(path, "report", both)
	require.NoError(t, err)
	require.Len(t, fromUser, 1)
	assert.Equal(t, 1, fromUser[0].Line, "the user's ask resolves under the combined selection")

	// The tool's result resolves (it is in the tool_result pool). "BUILD" appears in
	// BOTH lines, so it is the per-line disambiguation that proves both pools are
	// searched: quoting the result-only phrase lands on the result line.
	fromResult, err := CiteWithSources(path, "0 errors", both)
	require.NoError(t, err)
	require.Len(t, fromResult, 1)
	assert.Equal(t, 3, fromResult[0].Line, "the tool result resolves under the combined selection")
}

// TestCiteToolResultExcludesAssistantAndHarnessNoise: the tool_result pool is still
// a search over tool_result blocks ONLY. A quote that appears in an assistant turn
// (the agent narrating that it ran something) does not resolve — narration is not a
// result — and a harness-injected user message contributes no tool_result body, so
// its noise is not citable as output either.
func TestCiteToolResultExcludesAssistantAndHarnessNoise(t *testing.T) {
	p := newProject(t)
	path := p.write("a-session",
		assistantText("a1", "u0", "I ran the suite and it PASSED, trust me"),            // narration — not a result
		userMsg("u1", "<system-reminder>\nPASSED is a policy word\n</system-reminder>"), // injected noise
		toolUseMsg("c1", "u1", "Bash", "go test"),                                       // the call
		toolResultMsg("u2", "c1", "--- FAIL: TestFoo (0.01s)"),                          // a real result, different word
	)

	// The agent's narration of a pass is not a tool result.
	got, err := CiteWithSources(path, "PASSED", []SourceType{SourceToolResult})
	require.NoError(t, err)
	assert.Empty(t, got, "assistant narration and injected noise carry no tool_result body to cite")

	// A genuine result body IS citable, confirming the pool is live.
	real, err := CiteWithSources(path, "FAIL: TestFoo", []SourceType{SourceToolResult})
	require.NoError(t, err)
	require.Len(t, real, 1, "a real tool_result body resolves")
	assert.Equal(t, 4, real[0].Line)
}

// TestToolResultTextReadsBodyNotAnswer pins the unit that separates the two pools:
// toolResultText returns the WHOLE tool_result body (what an observation cites),
// where answerText returns only the <answer> of an AskUserQuestion envelope (the
// user's words). On an ordinary result, answerText is empty and toolResultText has
// the output; on an answer envelope, both see text but read it for different ends.
func TestToolResultTextReadsBodyNotAnswer(t *testing.T) {
	// An ordinary tool result: body present for the tool_result pool, nothing for
	// the user pool.
	plain := rawMessage(`{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1",` +
		`"content":"ok  sloprail/auth  0.4s\nPASS"}]}`)
	require.Equal(t, []string{"ok  sloprail/auth  0.4s\nPASS"}, toolResultText(plain),
		"the whole result body is the tool_result pool's text")
	assert.Empty(t, answerText(plain), "an ordinary result carries no answer envelope")

	// A tool_result content list: each text block's body is kept.
	listBody := rawMessage(`{"role":"user","content":[{"type":"tool_result","tool_use_id":"t2",` +
		`"content":[{"type":"text","text":"line one"},{"type":"text","text":"line two"}]}]}`)
	assert.Equal(t, []string{"line one", "line two"}, toolResultText(listBody))

	// A plain typed message has no tool_result block, so the tool_result pool is
	// empty on it — a delivery observation cannot be grounded in the user's typing.
	typed := rawMessage(`{"role":"user","content":"please refactor the parser"}`)
	assert.Empty(t, toolResultText(typed), "a typed message has no tool_result body")
}

// --- ToolResultAt: the LINE-oriented tool_result classifier ---
//
// A delivery OBSERVATION is a `<abs-jsonl>:<ranges>` citation, so the deterministic
// check is "is the entry at THIS LINE a tool_result", not "does a quote resolve".
// ToolResultAt answers it on the same pool cite searches, so the two agree on what
// a tool_result is.

// TestToolResultAtResolvesToolResultLine: the cited line of a tool_result returns
// its content and isToolResult true; a line that is the user's ask or the agent's
// turn returns false — an observation pointing there is pointing at prose.
func TestToolResultAtResolvesToolResultLine(t *testing.T) {
	p := newProject(t)
	path := p.write("a-session",
		userMsg("u1", "run the tests"),                             // line 1 — user
		toolUseMsg("a1", "u1", "Bash", "go test ./..."),            // line 2 — assistant
		toolResultMsg("u2", "a1", "ok  sloprail/auth  0.4s\nPASS"), // line 3 — tool_result
	)

	// Line 3 IS a tool_result: content returned, flag true.
	text, ok, err := ToolResultAt(path, 3)
	require.NoError(t, err)
	require.True(t, ok, "line 3 is a tool_result")
	assert.Contains(t, text, "PASS", "the result content is returned")

	// Line 1 is the user's ask — NOT a tool_result.
	_, ok, err = ToolResultAt(path, 1)
	require.NoError(t, err)
	assert.False(t, ok, "a user message is not a tool_result")

	// Line 2 is the agent's tool_use turn — NOT a tool_result either.
	_, ok, err = ToolResultAt(path, 2)
	require.NoError(t, err)
	assert.False(t, ok, "an assistant turn is not a tool_result")
}

// TestToolResultAtAnswerEnvelopeIsNotAResult: an AskUserQuestion answer envelope is
// a tool_result BLOCK, but its body is the user's selected words, not a produced
// result — so ToolResultAt must report it as NOT a tool_result. Classifying it as
// one would let an agent cite the user's own answer as a delivery observation
// (proof the work happened), the exact user-words-as-delivery substitution the
// evidence floor exists to refuse. genuineToolResultText drops the answer-envelope
// block, so the classifier answers false. A genuine produced result on a later line
// still answers true, so the exclusion does not over-reject. (A plain typed message,
// by contrast, is not a tool_result at all.)
//
// The answer is paired with the AskUserQuestion call it answers, as it is in every
// real record. Without the call the result's provenance is unknown and it is
// dropped for THAT reason (citableResults), so the test would pass whether or not
// the answer-envelope check exists.
func TestToolResultAtAnswerEnvelopeIsNotAResult(t *testing.T) {
	p := newProject(t)
	path := p.write("a-session",
		userMsg("u1", "just a typed message"), // line 1 — no tool_result block
		namedCall("q1", "u1", "t1", "AskUserQuestion", `{"questions":[{"question":"pick one","options":[{"label":"option B"}]}]}`), // line 2 — the question
		`{"type":"user","uuid":"u2","parentUuid":"q1","isSidechain":false,"message":{"role":"user","content":[`+
			`{"type":"tool_result","tool_use_id":"t1","content":"The user answered: \"pick one\"=\"option B\". Read carefully."}]}}`, // line 3 — answer envelope
		toolUseMsg("a2", "u2", "Bash", "go test ./pkg/foo"), // line 4 — the call
		`{"type":"user","uuid":"u3","parentUuid":"a2","isSidechain":false,"message":{"role":"user","content":[`+
			`{"type":"tool_result","tool_use_id":"t-a2","content":"PASS: TestFoo (0.01s)\nok  pkg/foo"}]}}`, // line 5 — a real produced result
	)

	// A plain typed message is not a tool_result.
	_, ok, err := ToolResultAt(path, 1)
	require.NoError(t, err)
	assert.False(t, ok, "a typed message carries no tool_result block")

	// An AskUserQuestion answer re-enters the transcript as a tool_result block, but
	// its body is the USER's own answer, not a produced result — so it is NOT a
	// delivery observation. Classifying it as one would let an agent cite the user's
	// "yes, proceed" as proof the work happened, the exact substitution this check
	// exists to refuse. It must be false, matching ToolResultAt's own contract.
	_, ok, err = ToolResultAt(path, 3)
	require.NoError(t, err)
	assert.False(t, ok, "an answer envelope is the user's words, not a produced result — not a tool_result observation")

	// The same through a citation: the answer grounds in the user pool, never in
	// the tool_result pool.
	_, err = ResolveCitation(path, toolReq("option B"))
	assert.Error(t, err, "an AskUserQuestion answer grounded as tool output")
	got, err := ResolveCitation(path, userReq("option B"))
	require.NoError(t, err, "the answer is the user's words")
	assert.Equal(t, 3, got.Line)

	// A genuine tool-call result IS a tool_result observation, and its body is
	// returned — the fix rejects the answer envelope without over-rejecting real
	// results.
	text, ok, err := ToolResultAt(path, 5)
	require.NoError(t, err)
	assert.True(t, ok, "a real produced result is a tool_result observation")
	assert.Contains(t, text, "PASS: TestFoo", "the produced result's body is returned")
}

// TestToolResultAtMissingLine: a line past the end, or one that is not an entry, is
// "not a tool_result" (false, no error) rather than a crash — so a bad observation
// range is named by the caller, not fatal.
func TestToolResultAtMissingLine(t *testing.T) {
	p := newProject(t)
	path := p.write("a-session", userMsg("u1", "only one line"))

	_, ok, err := ToolResultAt(path, 99)
	require.NoError(t, err)
	assert.False(t, ok, "a line past the end is not a tool_result")
}

// TestParseSourceType maps names to pools and rejects the unknown — the empirical
// spellings are `user` and `tool_result`, and an entry type that is not a citable
// pool (`assistant`) or a typo is refused rather than silently searching nothing.
func TestParseSourceType(t *testing.T) {
	for _, name := range []string{"user", "tool_result"} {
		s, ok := ParseSourceType(name)
		require.True(t, ok, "%q is a known pool", name)
		assert.Equal(t, SourceType(name), s)
	}
	for _, name := range []string{"assistant", "system", "toolresult", "USER", ""} {
		_, ok := ParseSourceType(name)
		assert.False(t, ok, "%q is not a citable pool", name)
	}
}

// userMsg is a plain user message with string content on one line.
func userMsg(uuid, content string) string {
	return `{"type":"user","uuid":"` + uuid + `","parentUuid":null,"isSidechain":false,` +
		`"message":{"role":"user","content":` + jsonQuote(content) + `}}`
}

// toolResultMsg is a user entry carrying a tool_result block — the shape Claude
// Code writes a tool's output in: a `type:"user"` record whose content is a
// `type:"tool_result"` block whose `content` is the result text. This is the
// SourceToolResult pool's fixture, the mirror of userMsg. It answers the call
// its parent made (toolUseMsg's id is "t-<uuid>"): a result whose call is not in
// the record is of unknown provenance and is not in the pool.
func toolResultMsg(uuid, parent, result string) string {
	return `{"type":"user","uuid":"` + uuid + `","parentUuid":"` + parent + `","isSidechain":false,` +
		`"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t-` + parent + `","content":` +
		jsonQuote(result) + `}]}}`
}

// toolUseMsg is an assistant turn invoking a tool by name — the call whose result
// a following toolResultMsg carries. It exists so a fixture reads as a real
// call-then-result pair; cite never searches it (an assistant turn is not a pool).
func toolUseMsg(uuid, parent, name, input string) string {
	return `{"type":"assistant","uuid":"` + uuid + `","parentUuid":"` + parent + `","isSidechain":false,` +
		`"message":{"role":"assistant","content":[{"type":"tool_use","id":"t-` + uuid + `","name":` +
		jsonQuote(name) + `,"input":{"command":` + jsonQuote(input) + `}}]}}`
}

// assistantText is an assistant turn whose content is a text block — the agent's
// own words, which cite must never treat as citable.
func assistantText(uuid, parent, text string) string {
	return `{"type":"assistant","uuid":"` + uuid + `","parentUuid":"` + parent + `","isSidechain":false,` +
		`"message":{"role":"assistant","content":[{"type":"text","text":` + jsonQuote(text) + `}]}}`
}

// queuedCommandMsg is a message the person sent WHILE a turn was already
// running — the shape verbatim from a real transcript (line 2721 of a captured
// session): an `attachment` entry whose `attachment.type` is `queued_command`
// and whose `commandMode` is `"prompt"`, which is what tells this one apart from
// a harness-queued <task-notification> (see taskNotificationAttachment).
func queuedCommandMsg(uuid, parent, prompt string) string {
	return `{"type":"attachment","uuid":"` + uuid + `","parentUuid":"` + parent + `","isSidechain":false,` +
		`"attachment":{"type":"queued_command","prompt":` + jsonQuote(prompt) +
		`,"source_uuid":"src-` + uuid + `","commandMode":"prompt","origin":{"kind":"human"},"humanTurn":true}}`
}

// taskNotificationAttachment is a background task's completion notice, queued
// through the SAME `queued_command` attachment shape as a genuine mid-turn
// message but with `commandMode: "task-notification"` — the harness speaking on
// its own behalf, never the person. cite must not resolve a quote against it.
func taskNotificationAttachment(uuid, parent, prompt string) string {
	return `{"type":"attachment","uuid":"` + uuid + `","parentUuid":"` + parent + `","isSidechain":false,` +
		`"attachment":{"type":"queued_command","prompt":` + jsonQuote(prompt) + `,"commandMode":"task-notification"}}`
}

// jsonQuote renders s as a JSON string literal for embedding in a fixture line.
func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
