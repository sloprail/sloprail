package e2e

import (
	"fmt"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T036_07: only what the person typed is intake. A tool's output and an
// AskUserQuestion answer envelope both re-enter the transcript as `type:"user"`
// records carrying a tool_result block; neither is a user message, so neither is
// residue the agent owes a task or a #skip for.
//
// The one real prompt is mapped to a task, then the agent runs a command and asks
// (and is answered) a question. The gate used to count those two records as extra
// unaccounted user messages and refuse; it must admit. The control right after
// proves (T036_08) the gate still refuses a genuinely unmapped prompt in the same session
// shape (T036_01 covers the plain case).
func TestT036_07_ToolResultsAndAnswerEnvelopesAreNotResidue(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)

	sess := "s-036-07"
	ref := fmt.Sprintf("%s:%d-%d", e.TranscriptPath(proj, sess), e.RootMessageLine(sess), e.RootMessageLine(sess))
	e.WriteFile(proj, "tasks/task-a/ASK.md", "# Task A\n\nRaised by the user request ("+ref+").\n")
	e.CommitAll(proj, "install + task")

	ask, answer := harness.AskUserQuestion("q1", "Which colour?", "blue")
	res := e.Run(proj, sess, "please handle request A", Turns("done",
		harness.Bash("b1", "echo hello"),
		ask,
		answer,
		Say("m1", "Recorded it as task-a."),
	))

	if blocks := e.BlockingErrorsFrom(proj, sess, "Stop"); strings.Contains(strings.Join(blocks, "\n"), residueReason) {
		t.Errorf("tool_result records or an answer envelope were counted as unaccounted user messages:\n%v", blocks)
	}
	if res.Refused() {
		t.Errorf("the gate refused a session whose only real user message was mapped to a task:\n%s", res.Output)
	}
}

// T036_08: the control for T036_07. The same session shape (a command's output and
// an answered question) with the real prompt mapped to NO task is still refused:
// excluding tool_result records narrows what counts as a user message, it does not
// stop the gate from refusing the one that is.
func TestT036_08_UnmappedPromptStillRefusedAmongToolResults(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	e.CommitAll(proj, "install")

	sess := "s-036-08"
	ask, answer := harness.AskUserQuestion("q1", "Which colour?", "blue")
	e.Run(proj, sess, "please handle request A", Turns("done",
		harness.Bash("b1", "echo hello"),
		ask,
		answer,
		Say("m1", "Looked at it, recorded nothing."),
	))

	joined := strings.Join(e.BlockingErrorsFrom(proj, sess, "Stop"), "\n")
	if !strings.Contains(joined, residueReason) {
		t.Fatalf("an unmapped prompt was not refused when tool_result records surrounded it:\n%s", joined)
	}
}

// T036_09: an entry is dropped only when EVERY content block is a tool_result. A
// user record that carries a tool_result AND a text block holds words the person
// typed, so it is still a user message: unmapped, it is refused, even though the
// real prompt is mapped to a task.
func TestT036_09_MixedToolResultAndTextEntryIsStillIntake(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)

	sess := "s-036-09"
	ref := fmt.Sprintf("%s:%d-%d", e.TranscriptPath(proj, sess), e.RootMessageLine(sess), e.RootMessageLine(sess))
	e.WriteFile(proj, "tasks/task-a/ASK.md", "# Task A\n\nRaised by the user request ("+ref+").\n")
	e.CommitAll(proj, "install + task")

	e.Run(proj, sess, "please handle request A", Turns("done",
		harness.Bash("b1", "echo hello"),
		harness.ToolResultWithText("mx1", "ok", "and also please do request B"),
		Say("m1", "Recorded request A."),
	))

	joined := strings.Join(e.BlockingErrorsFrom(proj, sess, "Stop"), "\n")
	if !strings.Contains(joined, residueReason) {
		t.Fatalf("a user entry carrying text next to a tool_result was dropped from intake:\n%s", joined)
	}
}
