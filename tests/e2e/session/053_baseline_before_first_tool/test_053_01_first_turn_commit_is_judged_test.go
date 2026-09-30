package e2e

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// chargeGuard is a file-guard over the billing code: its check
// runs at Stop, over the files the cycle's difference holds. Nothing stops the
// write; the difference is the only way the guard ever sees the change — which
// is exactly what makes it blind when the difference is measured from the wrong
// point.
const chargeGuard = `match: src/*.go
checks:
  - script: ./check.sh
`

// refuseRefunds refuses a charge file that issues a refund, and records every
// time it is asked, so "the guard never ran" and "the guard ran and passed" are
// told apart. A script, so no model is needed.
const refuseRefunds = `#!/bin/sh
payload="$(cat)"
echo asked >> "$SR_GUARDRAIL_DIR/ledger"
if printf '%s' "$payload" | grep -q refund; then
  echo '{"reason":"a refund in the charge path breaks the no-negative-charge invariant"}'
  exit 1
fi
exit 0
`

// requireRecordUnwrittenAtSessionStart fails the test unless the session's
// record did not exist while SessionStart ran — the order real Claude Code
// writes it in, and the only one in which the defect these tests exist for can
// happen. Read off the record itself rather than trusted to the mock's default:
// the origin record (the first with no parent) is the SessionStart hook's own
// attachment only when nothing was written before the hook ran. If a mock ever
// wrote the record early again, these tests would pass without testing
// anything; this makes them fail instead.
func requireRecordUnwrittenAtSessionStart(t *testing.T, e *Env, proj, sess string) {
	t.Helper()
	body, err := os.ReadFile(e.TranscriptPath(proj, sess))
	if err != nil {
		t.Fatalf("read the session's record: %v", err)
	}
	for _, line := range strings.Split(string(body), "\n") {
		var rec struct {
			Type       string  `json:"type"`
			UUID       string  `json:"uuid"`
			ParentUUID *string `json:"parentUuid"`
			Attachment struct {
				HookEvent string `json:"hookEvent"`
			} `json:"attachment"`
		}
		if json.Unmarshal([]byte(line), &rec) != nil || rec.UUID == "" || rec.ParentUUID != nil {
			continue
		}
		if rec.Type != "attachment" || rec.Attachment.HookEvent != "SessionStart" {
			t.Fatalf("the record's origin is a %q entry, not the SessionStart hook's attachment: the "+
				"record existed before SessionStart ran, so this session is not the one real Claude "+
				"Code runs and proves nothing", rec.Type)
		}
		return
	}
	t.Fatalf("the session's record has no origin entry at all")
}

// setUpBilling makes a repository whose billing code and guard are committed —
// the tree a session begins on — and returns that commit.
func setUpBilling(t *testing.T, e *Env, proj string) string {
	t.Helper()
	e.GitInit(proj)
	e.WriteFile(proj, "src/charge.go", "package src\n\nfunc Charge(cents int) int { return cents }\n")
	e.FileGuard(proj, "charge-invariant", chargeGuard, map[string]string{"check.sh": refuseRefunds})
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "setup: billing and its guard")
	return e.Git(proj, "rev-parse", "HEAD")
}

// T053_01: an agent that commits its change in its FIRST turn still has that
// change judged at Stop.
//
// The real evaluation run that found this: the agent edited the billing code,
// `git commit`ed it, and stopped. SessionStart had recorded nothing — the
// transcript did not exist yet — so the first point was taken at that first
// Stop, on the agent's own final commit. The difference came back empty and the
// guard never ran.
//
// Taken at the first tool call instead, the point is the setup commit: the
// commit came from a tool call, and every tool call reaches PreToolUse first.
func TestT053_01_FirstTurnCommitIsStillJudged(t *testing.T) {
	e := New(t)
	e.SetStopBlockCap(1)
	proj := e.Project()
	setup := setUpBilling(t, e, proj)

	const sess = "s-053-01"
	e.Run(proj, sess, "add a goodwill refund to the charge path and commit it", Turns("done",
		Write("w1", "src/charge.go", "package src\n\nfunc Charge(cents int) int { return cents - refund(cents) }\n\nfunc refund(c int) int { return c / 10 }\n"),
		Bash("b1", "git add -A && git commit -m 'goodwill refund'"),
	))
	requireRecordUnwrittenAtSessionStart(t, e, proj, sess)

	if e.Git(proj, "log", "-1", "--format=%s", setup+"..HEAD") == "" {
		t.Fatalf("the agent did not commit, so this proves nothing about a committed change")
	}

	if got := e.Meta(proj, sess, metaBaselineCommit); got != setup {
		t.Errorf("baseline commit = %q, want the commit the session began on (%q) — "+
			"a point taken after the agent's first turn sits on its own commit", got, setup)
	}
	if e.FileGuardLedger(proj, "charge-invariant", "ledger") == 0 {
		t.Fatalf("the file-guard was never asked: the committed change never reached the " +
			"difference it judges")
	}
	blocks := strings.Join(e.BlockingErrorsFrom(proj, sess, "Stop"), "\n")
	if !strings.Contains(blocks, "no-negative-charge invariant") {
		t.Fatalf("the guard's refusal of the committed change did not block the turn; Stop said:\n%s", blocks)
	}
}

// T053_02: the same session, uncommitted — the control.
//
// The change is judged whether or not the agent commits it; this is the run
// that always worked, because a first-Stop point on an unmoved HEAD happens to
// be the right one. It pins that the arrangement above breaks nothing else: the
// guard fires, and the point is still the setup commit.
func TestT053_02_FirstTurnUncommittedIsJudged(t *testing.T) {
	e := New(t)
	e.SetStopBlockCap(1)
	proj := e.Project()
	setup := setUpBilling(t, e, proj)

	const sess = "s-053-02"
	e.Run(proj, sess, "add a goodwill refund to the charge path", Turns("done",
		Write("w1", "src/charge.go", "package src\n\nfunc Charge(cents int) int { return cents - refund(cents) }\n\nfunc refund(c int) int { return c / 10 }\n"),
	))
	requireRecordUnwrittenAtSessionStart(t, e, proj, sess)

	if got := e.Meta(proj, sess, metaBaselineCommit); got != setup {
		t.Errorf("baseline commit = %q, want %q", got, setup)
	}
	blocks := strings.Join(e.BlockingErrorsFrom(proj, sess, "Stop"), "\n")
	if !strings.Contains(blocks, "no-negative-charge invariant") {
		t.Fatalf("the guard's refusal did not block the turn; Stop said:\n%s", blocks)
	}
}
