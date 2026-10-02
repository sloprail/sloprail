package e2e

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The Stop fails CLOSED. A session it cannot name, or a registry it cannot read, is not
// "nothing to judge": the turn is refused with a reason, because reading the absence of a
// record as a clean slate is how work goes unverified.

// unjudgedCommit makes a session whose range holds a commit nobody has judged, and returns
// the session id.
func unjudgedCommit(t *testing.T, e *Env, proj, sess string) {
	t.Helper()
	e.Run(proj, sess, "write the doc", Turns("done", harness.CommitFile("c1", "docs/a.md", "the release is Friday", "add a")))
}

// T056_10: a Stop whose payload names no session, and no readable record of one, is refused.
func TestT056_10_AStopThatCannotNameItsSessionIsRefused(t *testing.T) {
	e, proj := project(t)
	unjudgedCommit(t, e, proj, "s-056-10")

	payload, _ := json.Marshal(map[string]any{"cwd": proj, "stop_hook_active": false, "hook_event_name": "Stop"})
	res := e.CLIDirectStdinEnv(proj, string(payload), e.SessionEnv(""), "sr-session", "stop")
	if !harness.Blocked(res) {
		t.Fatalf("a Stop that could not identify its session let the turn end:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "cannot be identified") {
		t.Fatalf("the Stop refused, but not for the reason that matters (the session is unnamed):\n%s", res.Output)
	}
}

// T056_11: a session whose registry of tracked ranges is unreadable is refused at Stop with
// a reason; it is never read as "no ranges".
func TestT056_11_AnUnreadableRegistryRefusesTheStop(t *testing.T) {
	e, proj := project(t)
	const sess = "s-056-11"
	unjudgedCommit(t, e, proj, sess)
	if rs := ranges(t, e, proj, sess); len(rs) == 0 {
		t.Fatalf("premise: the session tracks no range, so a damaged registry loses nothing")
	}

	e.CorruptSessionState(proj, sess)
	res := e.StopNow(proj, sess, false)
	if !harness.Blocked(res) {
		t.Fatalf("a Stop over a registry that cannot be read let the turn end:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "session registry") || !strings.Contains(res.Output, "cannot be read") {
		t.Fatalf("the Stop refused, but not because the registry of tracked ranges could not be read:\n%s", res.Output)
	}
}

// T056_12: commits left on a detached HEAD are a range of their own, refused as such; once a
// branch holds them, that branch's range answers for them and the detached one is skipped.
func TestT056_12_ADetachedRangeIsVerifiedUntilABranchHoldsIt(t *testing.T) {
	e, proj := project(t)
	const sess = "s-056-12"
	e.Run(proj, sess, "work on a detached head", Turns("done",
		Bash("d1", "git switch -q --detach"),
		harness.CommitFile("c1", "docs/a.md", "the release is Friday", "add a on no branch"),
	))

	var detached *sessionstate.TrackedRange
	rs := ranges(t, e, proj, sess)
	for i := range rs {
		if len(rs[i].Head) >= 40 && rs[i].Tracked() {
			detached = &rs[i]
		}
	}
	if detached == nil {
		t.Fatalf("the commits on a detached HEAD are tracked as no range: %+v", rs)
	}
	joined := strings.Join(e.BlockingErrorsFrom(proj, sess, "Stop"), "\n")
	if !strings.Contains(joined, "detached at") || !strings.Contains(joined, "not judged yet") {
		t.Fatalf("the unjudged detached range was not refused as one:\n%s", joined)
	}

	// Give the commits a branch: that branch's range holds them, so the detached range is
	// not asked about again.
	e.Run(proj, sess, "name the work", Turns("done", Bash("b1", "git switch -q -c rescued")))
	res := e.StopNow(proj, sess, false)
	if !harness.Blocked(res) || !strings.Contains(res.Output, "rescued") {
		t.Fatalf("the branch holding the commits was not refused for them:\n%s", res.Output)
	}
	if strings.Contains(res.Output, "detached at") {
		t.Fatalf("the detached range was verified as well although a tracked branch holds its commit:\n%s", res.Output)
	}
}

// T056_13: a sub-agent's worktree is removed and its branch is gone with it: the range is
// pinned at its last tip (refs/sloprail/pins/…), so the commits stay reachable, and the root's
// Stop still refuses them.
func TestT056_13_ARemovedWorktreeWithNoBranchKeepsItsRangePinned(t *testing.T) {
	e, proj := project(t)
	const sess = "s-056-13"
	// The sub-agent stops without judging its range: the root's Stop is what answers for it.
	sub := harness.SubagentScriptUnjudged(t, Turns("sub done",
		Bash("b1", "git switch -q -c sub-gone"),
		harness.CommitFile("c1", "docs/a.md", "the release is Friday", "sub adds a"),
	))
	e.Run(proj, sess, "delegate", Turns("root done", harness.Dispatch("d1", "write the docs", sub, "worktree")))

	var wt string
	for _, f := range e.SessionFolders(proj, sess) {
		if f.Role == sessionstate.FolderSubagentWorktree {
			wt = f.Path
		}
	}
	if wt == "" {
		t.Fatalf("premise: no sub-agent worktree is registered: %+v", e.SessionFolders(proj, sess))
	}
	tip := e.Git(wt, "rev-parse", "HEAD")

	e.Git(proj, "worktree", "remove", "--force", wt)
	e.Git(proj, "branch", "-D", "sub-gone")
	payload, _ := json.Marshal(map[string]any{
		"session_id": sess, "transcript_path": e.TranscriptPath(proj, sess), "cwd": proj,
		"hook_event_name": "WorktreeRemove", "worktree_path": wt,
	})
	if r := e.CLIDirectStdinEnv(proj, string(payload), e.SessionEnv(""), "sr-session", "worktree-remove"); r.Code != 0 {
		t.Fatalf("the hook blocked the removal: exit %d\n%s", r.Code, r.Output)
	}

	pins := e.Git(proj, "for-each-ref", "--format=%(objectname) %(refname)", "refs/sloprail/pins")
	if !strings.Contains(pins, tip) {
		t.Fatalf("the gone branch's last tip %s is not pinned (refs/sloprail/pins: %q), so its commits can be collected", tip, pins)
	}
	res := e.StopNow(proj, sess, false)
	if !harness.Blocked(res) || !strings.Contains(res.Output, "not judged yet") {
		t.Fatalf("the root's Stop let a pinned, unjudged range go:\n%s", res.Output)
	}
}
