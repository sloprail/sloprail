// Package e2e drives successive writes to one path through a real session.
//
// This suite exists because of a refusal bypass that every unit test missed.
// The engine keyed a pending write's verdict on the file ALREADY ON DISK, so
// two offers against an unchanged file produced the same key and the second
// inherited the first's pass — never judged, never refused. A unit test driving
// the resolver directly can be written to miss that; a session that really
// writes the same path twice cannot.
//
// It also settles a claim this branch previously made against itself. The
// dispatch was thought unreachable end to end, because a10n-claude-mock's
// main-session PreToolUse payload omits transcript_path. It really does omit
// it — but identity is read from the origin record INSIDE the transcript, so
// locating the file by the harness's own naming resolves the same conversation
// the handed-over path would. That fallback is what makes everything here
// reachable, and TestT013_04 fails without it.
//
// One boundary this suite ran into and does not paper over: a PreFileUpdate
// event carries a path and NO content. A rule bound to it cannot be shown what
// the write would put there, so the payload-reading rule below can only judge
// updates by what it reads off the disk. That is a gap in the file module's
// extraction, not in the revalidation this branch owns, and it is the reason
// TestT013_02 drives its bypass through the disk rather than through the
// payload.
package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// judgeDecl declares a rule bound to both pending file kinds.
const judgeDecl = `---
hooks:
  PreFileCreate:
    - hooks:
        - type: command
          command: ./judge.sh
  PreFileUpdate:
    - hooks:
        - type: command
          command: ./judge.sh
---

# Refuses a secret, wherever it can see one
`

// judgeScript refuses when it can see a secret, and logs every time it is asked
// so the ledger says whether it ran at all. %s is the file under test.
//
// It looks in two places because the two kinds show it different things: a
// creation carries the pending content on the event, while an update carries
// only a path and leaves the rule to read the file. A real judge hook works the
// same way, and a rule that could only see one of them would be blind to half
// the writes in a session.
const judgeScript = `#!/bin/sh
target=%q
payload="$(cat)"
echo ran >> ./ran.log
secret=no
case "$payload" in *SECRET*) secret=yes ;; esac
if [ -f "$target" ] && grep -q SECRET "$target" 2>/dev/null; then secret=yes; fi
if [ "$secret" = yes ]; then echo 'that payload carries a secret' >&2; exit 1; fi
exit 0
`

// judgeRail installs the rule, pointing it at the one file these tests write.
func judgeRail(e *harness.Env, proj string) {
	e.Guardrail(proj, "no-secrets", judgeDecl, map[string]string{
		"judge.sh": fmt.Sprintf(judgeScript, filepath.Join(proj, "notes.md")),
	})
}

// readOrEmpty returns what a project file holds, or "" when it is not there.
func readOrEmpty(t *testing.T, proj, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(proj, name))
	if os.IsNotExist(err) {
		return ""
	}
	require.NoError(t, err)
	return string(b)
}

func TestT013_01_BenignSuccessiveWritesAreNeverRefused(t *testing.T) {
	// The feature working, through a real session: a file written and then
	// offered again unchanged. The first offer is a creation and the second a
	// change — different kinds carrying different fields — and neither is
	// refused, because nothing here is a violation.
	//
	// What this guards is the exemption's permissive edge. A skip that fired
	// where it should not would show up as a rule that stopped being asked; a
	// verdict wrongly retained as a refusal would show up as a deny on content
	// that was fine. The ledger and the stream cover both directions.
	e := New(t)
	proj := e.Project()
	judgeRail(e, proj)

	res := e.Run(proj, "sess-013-01", "write the same thing twice", Turns("done",
		Write("t1", "notes.md", "benign"),
		Write("t2", "notes.md", "benign"),
	))

	assert.False(t, res.Saw("that payload carries a secret"), "nothing here is a secret")
	// Exactly twice, not merely "at least once".
	//
	// The comment above says a wrongly-fired skip "would show up as a rule that
	// stopped being asked" — and NotEmpty, which stood here, cannot show that.
	// Two writes of the same bytes to the same path are two DIFFERENT subjects,
	// a creation and a change, so both must be judged; an engine that let the
	// second inherit the first's pass leaves one entry, which NotEmpty accepts.
	// That inheritance is the exact bug this suite was written for, and it is
	// what T013_02 catches by asserting 4 rather than "some". The permissive
	// edge deserves the same sharpness as the violating one.
	assert.Equal(t, []string{"ran", "ran"}, e.Ledger(proj, "no-secrets", "ran.log"),
		"both writes must be judged — a skip that fired here would leave one entry")
	assert.Equal(t, "benign", readOrEmpty(t, proj, "notes.md"), "permitted writes land")
}

func TestT013_02_AViolationIntroducedByALaterWriteIsStillCaught(t *testing.T) {
	// FINDING 1, end to end, and the reason this suite exists.
	//
	// Three writes to the SAME path. The first two are benign and are permitted,
	// which records a pass keyed on what the engine thought the subject was. The
	// third puts a secret on disk, and the offer AFTER it must be refused.
	//
	// Under the defect the subject for an update was the file already on disk.
	// The benign write landed, so the next offer fingerprinted the very content
	// that had just passed, matched the stored pass, and was skipped — the rule
	// was never asked. The fix makes a pending update produce no subject at all,
	// so it can never be exempt and the rule is always asked.
	//
	// The assertion is the RUN COUNT, not the refusal. That distinction cost this
	// test its first version: a refusal appears either way, because the offer
	// carrying the secret is a creation-shaped payload the rule can read outright
	// and refuses on sight. What the bypass actually removes is the rule being
	// ASKED — with the defect in place this same scenario runs the hook twice
	// instead of four times, and the two it skips are the two that matter.
	e := New(t)
	proj := e.Project()
	judgeRail(e, proj)

	res := e.Run(proj, "sess-013-02", "write benign twice, then a secret, then again", Turns("done",
		Write("t1", "notes.md", "benign"),
		Write("t2", "notes.md", "benign"),
		Write("t3", "notes.md", "SECRET=hunter2"),
		Write("t4", "notes.md", "anything at all"),
	))

	assert.Equal(t, 4, len(e.Ledger(proj, "no-secrets", "ran.log")),
		"every pending offer must reach the rule — a pass recorded for earlier content is not a licence for a later one")
	assert.True(t, res.Saw("that payload carries a secret"),
		"and the secret must be refused when it is offered")
}

func TestT013_03_ARefusalReFiresWhileTheFileStaysBad(t *testing.T) {
	// The route the doc's own defence overlooked. It argued the exemption was
	// safe because "once the write lands the file no longer yields that
	// fingerprint, and the next cycle judges it again" — which holds only if the
	// write LANDS.
	//
	// Here the file is left holding a secret and is then offered twice more.
	// Nothing lands, so the disk does not move, so under the defect the second
	// and third offers were keyed on unchanged content. A refusal is not a
	// licence, so they re-fire either way — but a refusal that the dispatcher
	// DROPPED would leave no row, and the first thing to record a pass for that
	// fingerprint would be believed. Both offers must be refused.
	e := New(t)
	proj := e.Project()
	judgeRail(e, proj)

	res := e.Run(proj, "sess-013-03", "leave a secret, then keep writing", Turns("done",
		Write("t1", "notes.md", "SECRET=first"),
		Write("t2", "notes.md", "another try"),
		Write("t3", "notes.md", "one more try"),
	))

	assert.True(t, res.Saw("that payload carries a secret"),
		"an unfixed violation must be put back in front of the rule every cycle")

	// Asked on every offer rather than exempted partway through. Three writes,
	// three questions: the count is what a wrongly-granted exemption reduces.
	assert.Equal(t, 3, len(e.Ledger(proj, "no-secrets", "ran.log")),
		"every offer against a file that is still bad must reach the rule")
}
