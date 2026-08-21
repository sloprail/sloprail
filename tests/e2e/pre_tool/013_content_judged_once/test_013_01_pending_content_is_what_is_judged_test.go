// Package e2e covers content_judged_once: a file's content is judged per cycle,
// and a verdict recorded for one content is never a licence for a different one.
//
// # RE-VEHICLED onto the NEW file-guard nature (was old GUARDRAIL.md hooks)
//
// This suite exists because of a refusal bypass every unit test missed: the engine
// keyed a pending write's verdict on the content ALREADY ON DISK, so two offers
// against an unchanged file produced the same key and the second inherited the
// first's pass — never judged, never refused. The NEW format's re-fire/skip
// machinery is the same revalidation store the old format used
// (services/sr-session/revalidation.go, driven from nature_fileguard.go's
// runFileGuardsPost): a file-guard's after-check fingerprints the SETTLED CONTENT
// (rev.Subject -> fingerprint.Of(newContent)), records the verdict per fingerprint
// (rev.Record), and SKIPS a later cycle only when the SAME content already passed
// (rev.Skip). So a fine file is judged once and then skipped while unchanged, and
// any DIFFERENT content — including a violation introduced later at the same path —
// has a different fingerprint and is always judged. These tests re-prove that
// against the new dispatch, reading the FLAT CheckPayload (`.event.newContent`).
//
// The check is an AFTER-check (non-preventive), because the skip/re-fire is a
// property of the settled-content path — a Post event carries the settled bytes,
// and revalidation keys on those. The ledger under the guard's own folder records
// every time the check is ASKED, which is what separates "judged" from "skipped".
package e2e

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// judgeGuard is a NEW-FORMAT after-check file-guard: a memories/ markdown file is
// not fine if its settled content holds SECRET. It records every ask into its own
// ledger (SR_GUARDRAIL_DIR/ran), so a test can say whether the content was judged
// or skipped.
const judgeGuard = `match: "**/*.md"
checks:
  - script: ./judge.sh
`

// judgeScript records every ask, reads the settled content off the FLAT payload
// (`.event.newContent`), and refuses when it holds SECRET. A real judge works the
// same way — the after-the-fact event carries the settled bytes.
const judgeScript = `#!/bin/sh
payload="$(cat)"
echo ran >> "$SR_GUARDRAIL_DIR/ran"
case "$payload" in
  *SECRET*) echo '{"reason":"that content carries a secret"}'; exit 1 ;;
esac
exit 0
`

// judgeRail installs the rule.
func judgeRail(e *harness.Env, proj string) {
	e.FileGuard(proj, "no-secrets", judgeGuard, map[string]string{"judge.sh": judgeScript})
}

// asks is how many times the check was asked — the ledger line count.
func asks(e *harness.Env, proj string) int {
	return e.FileGuardLedger(proj, "no-secrets", "ran")
}

// T013_01: a benign file is judged once, and a later cycle offering the SAME
// benign content is skipped rather than re-judged.
//
// The permissive edge of the exemption. A fine file that passed at its fingerprint
// must not be re-judged every cycle (a judge is a model call, and asking again can
// block work already fixed). The skip fired where it should: the count grows in the
// first cycle and does NOT grow in the second, which re-offers identical content.
func TestT013_01_BenignContentIsJudgedThenSkipped(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	judgeRail(e, proj)

	sess := "sess-013-01"

	res := e.Run(proj, sess, "write a benign memory", Turns("done",
		Write("t1", "memories/note.md", "benign"),
	))
	assert.False(t, res.Refused(), "nothing here is a secret")
	first := asks(e, proj)
	assert.Greater(t, first, 0, "the benign file must be judged at least once")

	// A later cycle re-offering the SAME benign content. It passed at that
	// fingerprint, so it is skipped — the check is not asked again about it.
	e.Run(proj, sess, "offer the same benign content again", Turns("done",
		Write("t2", "memories/note.md", "benign"),
	))
	after := asks(e, proj)
	assert.Equal(t, first, after,
		"a fine file that already passed at this content must be skipped, not re-judged — the skip failed to fire")
}

// T013_02: a violation introduced by a LATER write at the same path is still
// caught.
//
// FINDING 1, end to end, and the reason this suite exists. The first content is
// benign and passes, recording a pass keyed on THAT content's fingerprint. The
// second content puts a secret at the same path — a DIFFERENT fingerprint, so the
// stored pass cannot match it, so it is judged and refused. Under the defect the
// subject was the content on disk, so the offer fingerprinted content that had just
// passed, matched the stored pass, and was skipped — the rule was never asked. The
// content-based fingerprint is what makes the later violation impossible to exempt.
func TestT013_02_AViolationIntroducedByALaterWriteIsStillCaught(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	judgeRail(e, proj)

	sess := "sess-013-02"

	// Cycle 1: benign, passes.
	res := e.Run(proj, sess, "write benign", Turns("done",
		Write("t1", "memories/note.md", "benign"),
	))
	assert.False(t, res.Refused(), "the benign content is fine")
	afterBenign := asks(e, proj)
	assert.Greater(t, afterBenign, 0, "the benign content must be judged")

	// Cycle 2: a secret at the SAME path. Different content, different fingerprint,
	// so the earlier pass is no licence: it is judged and refused.
	e.Run(proj, sess, "put a secret at the same path", Turns("done",
		Write("t2", "memories/note.md", "SECRET=hunter2"),
	))
	afterSecret := asks(e, proj)
	assert.Greater(t, afterSecret, afterBenign,
		"a pass recorded for earlier content is not a licence for a later one — the secret must be judged, not skipped")
	// And the turn was blocked (the after-check refuses at Stop).
	assert.NotEmpty(t, e.BlockingErrorsFrom(proj, sess, "Stop"),
		"the secret introduced by a later write must be refused")
}

// T013_03: a refusal re-fires while the file stays bad.
//
// The route the exemption's own defence overlooked: it argued the skip was safe
// because "once the write lands the file no longer yields that fingerprint, and the
// next cycle judges it again" — which holds only if the content CHANGES. Here the
// file is left holding a secret; a further, unrelated cycle does not touch it, yet
// the still-not-fine file is put back in front of the rule (readdOutstanding
// re-adds the outstanding path to the diff) and refused again. A refusal is not a
// licence, and a refusal the dispatcher DROPPED would leave no row for the re-fire.
func TestT013_03_ARefusalReFiresWhileTheFileStaysBad(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	judgeRail(e, proj)

	sess := "sess-013-03"

	// Cycle 1: leave a secret. Judged and refused.
	e.Run(proj, sess, "leave a secret", Turns("done",
		Write("t1", "memories/note.md", "SECRET=first"),
	))
	afterFirst := asks(e, proj)
	assert.Greater(t, afterFirst, 0, "the secret must be judged in the first cycle")
	assert.NotEmpty(t, e.BlockingErrorsFrom(proj, sess, "Stop"), "an unfixed secret must block the turn")

	// Cycle 2: unrelated work that does NOT touch the bad file. The still-not-fine
	// file must be re-judged anyway — the re-fire.
	e.Run(proj, sess, "do something unrelated", Turns("done",
		Write("t2", "memories/other.md", "clean"),
	))
	afterUnrelated := asks(e, proj)
	assert.Greater(t, afterUnrelated, afterFirst,
		"an unfixed violation must be put back in front of the rule every cycle — the re-fire failed")
}
