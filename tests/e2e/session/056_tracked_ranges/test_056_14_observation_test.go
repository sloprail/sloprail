package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// Auto-tracking is by OBSERVATION: each hook records the branches' tips of the session's folders,
// and a branch whose tip MOVED during the session (however it moved) with commits beyond origin's
// default branch is tracked. No reflog is read, so a rebase, a reset or a cherry-pick (which a
// "commit" reflog line would miss) are as visible as a commit. Over-tracking is accepted: the
// agent can untrack with a reason.

// movedBranch stages a project where `topic` (and `other`, carrying a commit docs/o.md that
// origin/main lacks) exist before the session, starts the session, runs the commands in a second
// call, and returns the tracked range of `topic`.
func movedBranch(t *testing.T, sess, commands string) (*Env, string, sessionstate.TrackedRange) {
	t.Helper()
	e, proj := project(t)
	e.Git(proj, "switch", "-q", "-c", "other")
	e.WriteFile(proj, "docs/o.md", "the other work")
	e.CommitAll(proj, "other: a commit origin lacks")
	e.Git(proj, "switch", "-q", "main")
	e.Git(proj, "branch", "topic")
	e.Run(proj, sess, "start", Turns("done", Bash("b0", "true")))
	for _, r := range ranges(t, e, proj, sess) {
		if r.Head == "topic" || r.Head == "other" {
			t.Fatalf("premise: %s is tracked before it moved: %+v", r.Head, r)
		}
	}
	e.Run(proj, sess, "move the tip", Turns("done", Bash("b1", commands)))
	for _, r := range ranges(t, e, proj, sess) {
		if r.Head == "topic" {
			return e, proj, r
		}
	}
	t.Fatalf("the moved branch is not tracked: %+v", ranges(t, e, proj, sess))
	return nil, "", sessionstate.TrackedRange{}
}

// T056_14: a tip moved by a rebase is tracked at its new tip.
func TestT056_14_ARebasedTipIsTracked(t *testing.T) {
	const sess = "s-056-14"
	e, proj := project(t)
	e.Git(proj, "switch", "-q", "-c", "topic")
	e.WriteFile(proj, "docs/t.md", "topic work")
	e.CommitAll(proj, "topic work")
	e.Git(proj, "switch", "-q", "main")
	e.WriteFile(proj, "docs/m.md", "main moved")
	e.CommitAll(proj, "main moved")
	e.Run(proj, sess, "start", Turns("done", Bash("b0", "true")))
	e.Run(proj, sess, "rebase", Turns("done", Bash("b1", "git switch -q topic && git rebase -q main && git switch -q main")))
	got := false
	for _, r := range ranges(t, e, proj, sess) {
		if r.Head == "topic" && r.Tracked() && r.HeadSHA == e.Git(proj, "rev-parse", "topic") {
			got = true
		}
	}
	if !got {
		t.Fatalf("a rebased tip is not tracked: %+v", ranges(t, e, proj, sess))
	}
}

// T056_15: a tip moved by a reset to other commits is tracked.
func TestT056_15_AResetTipIsTracked(t *testing.T) {
	_, _, r := movedBranch(t, "s-056-15", "git switch -q topic && git reset -q --hard other && git switch -q main")
	if !r.Tracked() {
		t.Fatalf("a branch reset onto new commits is not tracked: %+v", r)
	}
}

// T056_16: a tip moved by a cherry-pick is tracked.
func TestT056_16_ACherryPickedTipIsTracked(t *testing.T) {
	e, proj, r := movedBranch(t, "s-056-16", "git switch -q topic && git cherry-pick other && git switch -q main")
	if !r.Tracked() || r.HeadSHA != e.Git(proj, "rev-parse", "topic") {
		t.Fatalf("a cherry-picked tip is not tracked at its new tip: %+v", r)
	}
}

// T056_17: a git error while observing fails CLOSED: the Stop refuses with a reason, it never
// reads "could not look" as "nothing tracked".
func TestT056_17_AGitErrorWhileObservingRefusesTheStop(t *testing.T) {
	e, proj := project(t)
	const sess = "s-056-17"
	e.Run(proj, sess, "start", Turns("done", Bash("b0", "true")))
	if err := os.WriteFile(filepath.Join(proj, ".git", "packed-refs"), []byte("garbage\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := e.StopNow(proj, sess, false)
	if !harness.Blocked(res) || !strings.Contains(res.Output, "could not be observed") {
		t.Fatalf("a Stop that could not observe the branches let the turn end or refused for another reason:\n%s", res.Output)
	}
}

// T056_18: a Stop that cannot name its session asks for identity only where there is something to
// verify; a project with no rule at all is never blocked for it.
func TestT056_18_ASessionlessStopInAProjectWithNoRulesIsNotBlocked(t *testing.T) {
	e := harness.New(t, harness.WithoutShippedFileGuards(), harness.NoAutoCheck())
	proj := e.Project()
	e.GitInit(proj)
	e.WriteFile(proj, "README.md", "no rules here")
	e.CommitAll(proj, "base")
	payload, _ := json.Marshal(map[string]any{"cwd": proj, "stop_hook_active": false, "hook_event_name": "Stop"})
	res := e.CLIDirectStdinEnv(proj, string(payload), e.SessionEnv(""), "sr-session", "stop")
	if harness.Blocked(res) {
		t.Fatalf("a project with no rules was blocked for a session it cannot name:\n%s", res.Output)
	}
}
