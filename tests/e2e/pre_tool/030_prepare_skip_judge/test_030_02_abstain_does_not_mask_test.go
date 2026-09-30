package e2e

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// These drive the ABSTAIN-vs-permit distinction end to end through the mock: a
// judge whose prepare skips is FOLLOWED by a second check, and the second check's
// verdict is what must decide. If skip forced a pass, the second check's refusal
// would be masked and the write wrongly permitted; because skip abstains, the
// second check still runs and first-refusal still wins.
//
// The guard here has TWO checks in order: the skipping judge, then a script check
// (./gate.sh) that refuses when the file's content holds VETO. Both cases below use
// the guarded/skip-me/ path so the judge's prepare abstains; they differ only in
// whether the file content trips the second check.

// maskGuard is a PreFileWrite gate whose checks are, in order: the standing
// require-known-result.sh, the skipping judge, and a content check. The judge reuses the shared prepare/template
// (skip-me path -> abstain); the gate refuses on VETO.
const maskGuard = `on:
  - event: PreFileWrite
    match: 'event.path startsWith "guarded/" and event.path endsWith ".md"'
checks:
  - script: ./require-known-result.sh
  - prepare: ./prepare.sh
    judge: ./judge.md.j2
    model: size-md
    timeout: 25s
  - script: ./gate.sh
`

// gateScript is the SECOND check: it refuses when the settled/pending content holds
// VETO, and passes otherwise. It reads the flat payload the same way any script
// check does.
const gateScript = `#!/bin/sh
payload="$(cat)"
case "$payload" in
  *VETO*) echo '{"reason":"the second check vetoes this content"}'; exit 1 ;;
esac
exit 0
`

// T030_03: the judge abstains (skip-me path), and the SECOND check REFUSES — the
// write must still be REFUSED. This is the whole point of abstain over permit: an
// abstaining judge does not mask a later check's refusal. The judge model is never
// invoked (no prompt captured), proving the refusal came from the second check, not
// a leaked judge.
func TestT030_03_AbstainDoesNotMaskALaterRefusal(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installReviewGuard(e, proj, maskGuard, map[string]string{
		"prepare.sh":  prepareScript,
		"judge.md.j2": judgeTemplate,
		"gate.sh":     gateScript,
	})
	// A judge that would PASS if it ran — so the refusal cannot be attributed to the
	// judge. It captures its prompt, so a leaked model call would leave a witness.
	e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt", `{"pass": true, "reasoning": ""}`)

	res := e.Run(proj, "s-030-03", "write a skip-path file the gate vetoes", Turns("done",
		Write("w1", "guarded/skip-me/note.md", "this body carries VETO"),
	))

	// The abstaining judge dropped out; the second check refused; the write is blocked.
	assert.True(t, res.Refused(),
		"an abstaining judge must NOT mask the second check's refusal — the write should be blocked:\n"+res.Output)
	assert.True(t, res.Saw("the second check vetoes this content"),
		"the refusal must be the SECOND check's, not the judge's:\n"+res.Output)
	// The judge model was never invoked — the abstain skipped it, and the refusal is
	// wholly the second check's.
	assert.Empty(t, e.JudgePrompt(proj, "judge-prompt.txt"),
		"the skipped judge's model must not run even when a later check refuses")
	// prepare ran (the judge check was reached and abstained).
	assert.NotEmpty(t, seenPaths(e, proj), "prepare must have run for the judge check to abstain")
}

// T030_04: the judge abstains (skip-me path), and the SECOND check PASSES (no VETO)
// -> the write is PERMITTED. The abstain drops out and the passing gate leaves the
// chain with no refusal. The judge model is still never invoked.
func TestT030_04_AbstainThenPassPermits(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installReviewGuard(e, proj, maskGuard, map[string]string{
		"prepare.sh":  prepareScript,
		"judge.md.j2": judgeTemplate,
		"gate.sh":     gateScript,
	})
	// A judge that would REFUSE if it ran, so a leaked model call would flip the
	// expected permit to a refusal and fail this test.
	e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt", `{"pass": false, "reasoning": "should never run"}`)

	res := e.Run(proj, "s-030-04", "write a skip-path file the gate allows", Turns("done",
		Write("w1", "guarded/skip-me/note.md", "a perfectly fine body"),
	))

	assert.True(t, res.Permitted(),
		"an abstaining judge followed by a passing check must permit the write:\n"+res.Output)
	assert.Empty(t, e.JudgePrompt(proj, "judge-prompt.txt"),
		"the skipped judge's model must not run")
	assert.NotEmpty(t, seenPaths(e, proj), "prepare must have run for the judge check to abstain")
}
