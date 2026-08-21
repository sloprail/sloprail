package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// ENVELOPE WIRING for no-unasked-deletion: the whole answer ENVELOPE — the
// QUESTION and every answer — reaches the judge's prompt when the ask was an
// AskUserQuestion answer, not just the extracted answer.
//
// The shipped prepare (collect-quote-and-diff.sh) now, having a grounded quote,
// re-resolves it with `cite` and fetches the envelope at that line with
// `sr-session trajectory envelope` (which wraps internal/transcript EnvelopeAt).
// A judge grounded on an answer-quote needs the QUESTION to weigh the answer — an
// answer of "the second option" authorizes only what that option, in the
// question's own terms, covers (PR-19 review, cite.go:251). The stubbed verdict
// path (test_049_01/02) proves the quote and the diff reach the judge, but NOT the
// question: the renderer treats an undefined variable as empty, so the template's
// `{{ additionalContext.asked_envelope }}` renders whether prepare produced it or
// nothing. This closes that — it captures the rendered prompt and asserts the
// QUESTION TEXT is in it, which is present only if prepare fetched the envelope AND
// the template interpolated it.
//
// # Why TWO runs under one session
//
// An AskUserQuestion answer lands as a `user` record carrying a tool_result
// (harness.AnswerQuestion) — not a tool call, so it does not re-prompt the agent
// and cannot be a non-final turn that advances to a following Write in the same
// scenario. So the answer is emitted in its OWN Run, which leaves it in the
// session transcript; a SECOND Run under the SAME session id then does the
// removing Write, and the guard's cite (reading that same transcript by --path)
// grounds the sr:asked quote to the answer envelope from the first run. This is
// the harness's documented "Run more than once under the same conversation"
// (scenario.go), and it is how the answer and the guarded write share a trajectory.

// T049_15: the QUESTION the user answered reaches the judge's prompt via the
// envelope. Run 1 records the AskUserQuestion answer; Run 2's removal is grounded
// in it, so the prepare fetches the whole envelope and the template renders it —
// the judge sees what was ASKED, not only the answer quote.
func TestT049_15_AnsweredQuestionReachesJudgePrompt(t *testing.T) {
	e := newEnv(t)
	proj := nudProject(t, e)

	// The question the agent asked and the answer the user chose. The answer is the
	// authorizing quote; the QUESTION carries a token (ZZ_QUESTION) that appears
	// NOWHERE else — not in the prompt, not in the file — so finding it in the
	// rendered prompt can only mean the envelope was fetched and interpolated.
	const question = "which line should I remove from the ZZ_QUESTION memory?"
	const answer = "remove the second line please"

	seedCommittedMemory(t, e, proj, "memories/topic.md",
		"keep this line\nremove the second line\nprovenance: kept\n")

	e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt", `{"pass": true, "reasoning": ""}`)

	const sess = "s-049-15"

	// Run 1: the user's prompted answer lands as the answer envelope, which stays in
	// the session transcript. The prompt does NOT contain the answer substring, so
	// the quote resolves uniquely to the envelope line, not the prompt.
	e.Run(proj, sess, "here is a memory editing task", Turns("done",
		harness.AnswerQuestion("q1", [2]string{question, answer}),
	))

	// Run 2, SAME session: the removal, authorized by an sr:asked marker quoting the
	// ANSWER. cite (in the shipped script, and again in the prepare) grounds it to
	// the envelope from run 1; the prepare fetches the whole envelope there.
	e.Run(proj, sess, "now make the edit", Turns("done",
		Write("w1", "memories/topic.md",
			"---\n# sr:asked \"remove the second line please\"\n---\nkeep this line\nprovenance: kept\n"),
	))

	prompt := e.JudgePrompt(proj, "judge-prompt.txt")
	if prompt == "" {
		t.Fatalf("the judge never ran — no prompt captured (did the grounded ask reach the judge?)")
	}
	// The ANSWER (the authorizing quote) reaches the prompt via asked_quote — the
	// positive control, so the question assertion below is not vacuous.
	if !strings.Contains(prompt, answer) {
		t.Fatalf("the answer quote did not reach the judge prompt — the grounded-ask path did not run:\n%s", prompt)
	}
	// The QUESTION reaches the prompt ONLY via the whole envelope — cite's location
	// alone never carries it, and it appears nowhere else in the inputs. This is the
	// EnvelopeAt wiring proven end to end.
	if !strings.Contains(prompt, question) {
		t.Fatalf("the QUESTION the user answered did not reach the judge prompt — the envelope (EnvelopeAt) wiring is broken; the judge sees only the answer, not what was asked:\n%s", prompt)
	}
}

// T049_16: a MESSAGE-grounded removal (the ask is a plain user message, not an
// AskUserQuestion answer) still passes to the judge, and the judge runs — there is
// simply no envelope to add. This is the empty-but-fine case: the prepare's
// envelope fetch finds no answer envelope at a plain-message line, so
// asked_envelope is empty and the template omits the envelope section, but the
// quote and diff still reach the judge and the removal is judged normally. It
// proves the envelope fetch is BEST-EFFORT, not a gate.
func TestT049_16_MessageGroundedRemovalHasNoEnvelopeButStillJudges(t *testing.T) {
	e := newEnv(t)
	proj := nudProject(t, e)

	seedCommittedMemory(t, e, proj, "memories/topic.md",
		"keep this line\nremove the second line\n")

	e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt", `{"pass": true, "reasoning": ""}`)

	// The ask is the user's own PROMPT (a plain message), quoted verbatim. cite
	// grounds it to the prompt line; envelope finds no answer envelope there.
	const prompt = "please remove the second line"
	res := e.Run(proj, "s-049-16", prompt, Turns("done",
		Write("w1", "memories/topic.md",
			"---\n# sr:asked \"please remove the second line\"\n---\nkeep this line\n"),
	))

	if res.Refused() {
		t.Fatalf("a message-grounded removal was refused — an empty envelope must not turn a passed removal into a refusal:\n%s", res.Output)
	}
	captured := e.JudgePrompt(proj, "judge-prompt.txt")
	if captured == "" {
		t.Fatalf("the judge never ran on a message-grounded removal — the envelope fetch must be best-effort, not a gate")
	}
	// The quote still reaches the judge; the envelope section is simply absent.
	if !strings.Contains(captured, prompt) {
		t.Fatalf("the asked_quote did not reach the judge prompt on a message-grounded removal:\n%s", captured)
	}
}
