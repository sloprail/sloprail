package e2e

// pinned-invariant, the pins it must not take on faith. An fqn is written by the
// agent being judged, so its sha and line range are checked before anything is
// read with them: a range past the end of the spec pins no text at all (and a
// judge handed an empty <pinned> rules against nothing), and a "sha" that is
// really a git option makes the pin check write a file of the agent's choosing.

import (
	"testing"
)

const codeUpholdsHeading = "Does the marked code actually uphold this invariant?"

// T046_20: a pin past the end of the spec is refused by the pin check, and the
// judge is never asked.
func TestT046_20_OutOfRangePinIsRefusedBeforeTheJudge(t *testing.T) {
	e := newEnv(t)
	proj := biProject(t, e)
	sha := commitSpec(t, e, proj, "SPEC.md", specV1, "spec v1")
	e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt", `{"pass": true, "reasoning": "nothing to fail"}`)

	fqn := proj + "@" + sha + ":SPEC.md#L99-99"
	sess := "s-046-20"
	e.Run(proj, sess, "add code pinned past the end of the spec", Turns("done",
		Write("w1", "src/charge.go", invariantCode(fqn, "func charge(total int) {}\n")),
	).ThenCommit("write the files"))

	joined := joinBlocks(e.BlockingErrorsFrom(proj, sess, "Stop"))
	if !containsAll(joined, "L99-99", "pinned-invariant") {
		t.Fatalf("a pin past the end of the spec was not refused by the pin check:\n%s", joined)
	}
	if n := e.JudgeCalls(proj, "judge-prompt.txt", codeUpholdsHeading); n != 0 {
		t.Errorf("the judge was asked %d time(s) about a pin that names no text", n)
	}
}
