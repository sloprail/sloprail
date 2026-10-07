package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// ENVELOPE WIRING for no-unasked-deletion: the whole answer ENVELOPE — the
// QUESTION and every answer — reaches the judge's prompt when the ask was an
// AskUserQuestion answer, not just the extracted answer.
//
// A removal citing an answer carries, as that citation's message, the whole
// envelope — the question with every answer given — and the judge template
// renders it. A judge grounded on an answer-quote needs the QUESTION to weigh the
// answer: an answer of "the second option" authorizes only what that option, in
// the question's own terms, covers. This captures the rendered prompt and asserts
// the QUESTION TEXT is in it.
//
// # Why TWO runs under one session
//
// An AskUserQuestion answer lands as a `user` record carrying a tool_result
// (harness.AnswerQuestion) — not a tool call, so it does not re-prompt the agent
// and cannot be a non-final turn that advances to a following Write in the same
// scenario. So the answer is emitted in its OWN Run, which leaves it in the
// session transcript; a SECOND Run under the SAME session id then makes the
// cited removal, whose quote resolves to the answer envelope from the first run. This is
// the harness's documented "Run more than once under the same conversation"
// (scenario.go), and it is how the answer and the guarded write share a trajectory.

// T049_15: the QUESTION the user answered reaches the judge's prompt via the
// envelope. Run 1 records the AskUserQuestion answer; Run 2's removal is grounded
// in it, so the citation carries the whole envelope and the template renders it —
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
		harness.AnswerIfAsked(t, "q1", [2]string{question, answer})...,
	))

	// Run 2, SAME session: the removal, authorized by an sr:asked marker quoting the
	// ANSWER. cite (in the shipped script, and again in the prepare) grounds it to
	// the envelope from run 1; the prepare fetches the whole envelope there.
	e.Run(proj, sess, "now make the edit", Turns("done",
		srWrite("w1", "memories/topic.md", "keep this line\nprovenance: kept\n", "remove the second line please"),
	).ThenCommit("write the files", harness.CitesUser("remove the second line please")))

	prompt := e.JudgePrompt(proj, "judge-prompt.txt")
	// Without an answer record (no question tool) the person never said the answer: the
	// removal's quote does not resolve, so nothing grounds it — the judge is never
	// asked and no question or answer reaches a prompt — and the file keeps its line.
	if !harness.HasCap(t, harness.CapAskUserQuestion) {
		if prompt != "" {
			t.Fatalf("the judge ran for a removal quoting words the person never said:\n%s", prompt)
		}
		body, err := os.ReadFile(filepath.Join(proj, "memories/topic.md"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), "remove the second line") {
			t.Fatalf("a removal grounded in words never said landed:\n%s", body)
		}
		return
	}
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
