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
// proves the gate still refuses a genuinely unmapped prompt in the same session
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
