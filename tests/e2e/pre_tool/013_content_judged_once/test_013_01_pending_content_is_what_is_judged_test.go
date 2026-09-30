// Package e2e covers content_judged_once: a file's content is judged per commit
// range, and a verdict recorded for one content is never a licence for a different
// one.
//
// This suite exists because of a refusal bypass every unit test missed: the engine
// keyed a pending write's verdict on the content ALREADY ON DISK, so two offers
// against an unchanged file produced the same key and the second inherited the
// first's pass — never judged, never refused. A file-guard now judges COMMITS: a
// range that passed is recorded and the rule's base moves to its head, so a later
// cycle that changed nothing has nothing to judge, and a later commit — including a
// violation introduced at the same path — is a new range and is always judged. A
// refused range does not move the base, so it is judged again with whatever is
// committed on top of it. These tests prove that through the real dispatch, reading
// the Changeset payload.
//
// The ledger under the guard's own folder records every time the check is ASKED,
// which is what separates "judged" from "not asked again".
package e2e

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// judgeGuard is a file-guard: a memories/ markdown file is not fine if its
// committed content holds SECRET. It records every ask into its own
// ledger (SR_GUARDRAIL_DIR/ran), so a test can say whether the content was judged
// or skipped.
const judgeGuard = `match: "**/*.md"
checks:
  - script: ./judge.sh
`

// judgeScript records every ask, reads the committed content off the Changeset
// payload, and refuses when it holds SECRET. A real judge works the same way.
const judgeScript = `#!/bin/sh
payload="$(cat)"
echo ran >> "$SR_GUARDRAIL_DIR/ran"
case "$payload" in
  *SECRET*) echo '{"reason":"that content carries a secret"}'; exit 1 ;;
esac
exit 0
`

// judgeRail installs the rule and commits it. A rule's range starts at the last
// commit that touched its own folder, so a rule committed together with the
// session's work would judge an empty range.
func judgeRail(e *harness.Env, proj string) {
	e.FileGuard(proj, "no-secrets", judgeGuard, map[string]string{"judge.sh": judgeScript})
	e.CommitAll(proj, "the project before the session")
}

// asks is how many times the check was asked — the ledger line count.
func asks(e *harness.Env, proj string) int {
	return e.FileGuardLedger(proj, "no-secrets", "ran")
}

// T013_01: a benign file is judged once, and a later cycle offering the SAME
// benign content is not judged again.
//
// The permissive edge. A fine file that passed must not be re-judged every cycle (a
// judge is a model call, and asking again can block work already fixed). The rule's
// base moved to the head it passed at, so the count grows in the first cycle and
// does NOT grow in the second, which re-offers identical content (nothing to commit).
func TestT013_01_BenignContentIsJudgedThenNotAskedAgain(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	judgeRail(e, proj)

	sess := "sess-013-01"

	res := e.Run(proj, sess, "write a benign memory", Turns("done",
		Write("t1", "memories/note.md", "benign"),
	).ThenCommit("add the memory"))
	assert.False(t, res.Refused(), "nothing here is a secret")
	first := asks(e, proj)
	assert.Greater(t, first, 0, "the benign file must be judged at least once")

	// A later cycle re-offering the SAME benign content changes nothing, so there is
	// no new commit and nothing new to judge — the check is not asked again.
	e.Run(proj, sess, "offer the same benign content again", Turns("done",
		Write("t2", "memories/note.md", "benign"),
	))
	after := asks(e, proj)
	assert.Equal(t, first, after,
		"a fine file that already passed must not be judged again while nothing new is committed")
}

// T013_02: a violation introduced by a LATER write at the same path is still
// caught.
//
// FINDING 1, end to end, and the reason this suite exists. The first content is
// benign and passes, moving the rule's base to that head. The second commit puts a
// secret at the same path — a new range with different content, so the earlier pass
// is no licence, and it is judged and refused. Under the defect the verdict was
// keyed on the content on disk, so the offer matched content that had just passed
// and was skipped — the rule was never asked.
func TestT013_02_AViolationIntroducedByALaterWriteIsStillCaught(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	judgeRail(e, proj)

	sess := "sess-013-02"

	// Cycle 1: benign, passes.
	res := e.Run(proj, sess, "write benign", Turns("done",
		Write("t1", "memories/note.md", "benign"),
	).ThenCommit("add the memory"))
	assert.False(t, res.Refused(), "the benign content is fine")
	afterBenign := asks(e, proj)
	assert.Greater(t, afterBenign, 0, "the benign content must be judged")

	// Cycle 2: a secret at the SAME path. A new range with different content, so the
	// earlier pass is no licence: it is judged and refused.
	e.Run(proj, sess, "put a secret at the same path", Turns("done",
		Write("t2", "memories/note.md", "SECRET=hunter2"),
	).ThenCommit("put a secret in the memory"))
	afterSecret := asks(e, proj)
	assert.Greater(t, afterSecret, afterBenign,
		"a pass recorded for earlier content is not a licence for a later one — the secret must be judged, not skipped")
	// And the turn was blocked (the after-check refuses at Stop).
	assert.NotEmpty(t, e.BlockingErrorsFrom(proj, sess, "Stop"),
		"the secret introduced by a later write must be refused")
}

// T013_03: a refusal keeps refusing while the file stays bad.
//
// The route the exemption's own defence overlooked: it argued a stored pass was
// safe because "once the write lands the next cycle judges it again" — which holds
// only if the content CHANGES. Here the file is left holding a secret; a further,
// unrelated commit does not touch it, yet the refused range did not move, so the
// still-not-fine file is judged again with the new commit on top of it and refused
// again. A refusal is not a licence, and a refusal the dispatcher DROPPED would
// leave the second cycle nothing to hold it to.
func TestT013_03_ARefusalKeepsRefusingWhileTheFileStaysBad(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	judgeRail(e, proj)

	sess := "sess-013-03"

	// Cycle 1: leave a secret. Judged and refused.
	e.Run(proj, sess, "leave a secret", Turns("done",
		Write("t1", "memories/note.md", "SECRET=first"),
	).ThenCommit("add the memory"))
	afterFirst := asks(e, proj)
	assert.Greater(t, afterFirst, 0, "the secret must be judged in the first cycle")
	assert.NotEmpty(t, e.BlockingErrorsFrom(proj, sess, "Stop"), "an unfixed secret must block the turn")

	// Cycle 2: unrelated work that does NOT touch the bad file. The still-not-fine
	// file must be judged again anyway.
	e.Run(proj, sess, "do something unrelated", Turns("done",
		Write("t2", "memories/other.md", "clean"),
	).ThenCommit("add another memory"))
	afterUnrelated := asks(e, proj)
	assert.Greater(t, afterUnrelated, afterFirst,
		"an unfixed violation must be put back in front of the rule every cycle")
}
