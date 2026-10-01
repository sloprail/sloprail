package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// INVARIANT: the two eval-fixture judges refuse a committed change that breaks
// them at Stop, and pass once a fixing commit lands.
//
// eval-prompt-no-hints judges a fixture's prompt.md (prepare.sh reads the
// fixture's own rules from the committed tree); eval-score-reads-transcript judges
// its score.sh. A FAILING verdict is terminal for a range, so the fix is a new
// commit with different content (a new fingerprint) and the stub is switched to a
// passing verdict for it: what is proven is that the refusal comes from the
// judge's verdict on the COMMITTED bytes, and that a later commit is judged again.

const fixtureYAML = "name: x\nseed: seed\n"

func evalFixtureProject(t *testing.T, e *harness.Env, rule string) string {
	t.Helper()
	proj := guardProject(t, e, rule)
	e.WriteFile(proj, "examples/x/eval/y/fixture.yaml", fixtureYAML)
	e.CommitAll(proj, "the fixture")
	return proj
}

func TestEvalPromptNoHints_BrokenPromptRefusedThenFixedPasses(t *testing.T) {
	e := New(t)
	proj := evalFixtureProject(t, e, "eval-prompt-no-hints")
	const session = "s-erj-nohints"
	path := "examples/x/eval/y/prompt.md"

	e.InstallJudgeClaude(`{"pass": false, "reasoning": "EVAL HINT: the prompt names the guardrail"}`)
	e.Run(proj, session, "write a prompt that hints", Turns("done",
		harness.Write("w1", path, "Load the credential skill first, then edit the inventory.\n"),
	).ThenCommit("a hinting prompt"))
	before := e.BlockingErrors(proj, session)
	if !sawRefusal(before, "EVAL HINT") {
		t.Fatalf("a committed prompt the judge flags was not refused at Stop: %v", before)
	}

	e.InstallJudgeClaudeCapturing(proj, "fixed-prompt.txt", `{"pass": true, "reasoning": ""}`)
	e.Run(proj, session, "rewrite the prompt without the hint", Turns("done",
		harness.Write("w2", path, "Add a new credential to the inventory.\n"),
	).ThenCommit("a neutral prompt"))
	if after := e.BlockingErrors(proj, session); len(after) != len(before) {
		t.Fatalf("the fixed prompt was refused again: %v", after[len(before):])
	}
	if !strings.Contains(e.JudgePrompt(proj, "fixed-prompt.txt"), "Add a new credential") {
		t.Fatalf("the judge never saw the fixing commit, so the pass proves nothing")
	}
}

func TestEvalScoreReadsTranscript_DeadScorerRefusedThenFixedPasses(t *testing.T) {
	e := New(t)
	proj := evalFixtureProject(t, e, "eval-score-reads-transcript")
	const session = "s-erj-scorer"
	path := "examples/x/eval/y/score.sh"

	e.InstallJudgeClaude(`{"pass": false, "reasoning": "EVAL SCORER: the verdict never depends on the transcript"}`)
	e.Run(proj, session, "write a scorer that ignores the transcript", Turns("done",
		harness.Write("w1", path, "#!/bin/sh\nexit 0\n"),
	).ThenCommit("a fixed scorer"))
	before := e.BlockingErrors(proj, session)
	if !sawRefusal(before, "EVAL SCORER") {
		t.Fatalf("a committed scorer the judge flags was not refused at Stop: %v", before)
	}

	e.InstallJudgeClaudeCapturing(proj, "fixed-prompt.txt", `{"pass": true, "reasoning": ""}`)
	e.Run(proj, session, "make the scorer read the transcript", Turns("done",
		harness.Write("w2", path, "#!/bin/sh\ngrep -q 'Skill' \"$SR_EVAL_TRANSCRIPT\"\n"),
	).ThenCommit("a transcript-reading scorer"))
	if after := e.BlockingErrors(proj, session); len(after) != len(before) {
		t.Fatalf("the fixed scorer was refused again: %v", after[len(before):])
	}
	if !strings.Contains(e.JudgePrompt(proj, "fixed-prompt.txt"), "SR_EVAL_TRANSCRIPT") {
		t.Fatalf("the judge never saw the fixing commit, so the pass proves nothing")
	}
}
