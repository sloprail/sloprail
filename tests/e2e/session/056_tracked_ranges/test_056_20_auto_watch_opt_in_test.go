package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T056_20: the engine attaches git refs to the session on its own only when
// SR_AUTO_WATCH_GIT_REFS is set (every other test in this package runs with it set, through the
// harness). Unset, nothing is auto-watched; what the agent adds with `refs track` is watched and
// verified at Stop as ever.
func unwatchedFailingProject(t *testing.T) (*Env, string) {
	t.Helper()
	e := harness.New(t, harness.WithoutShippedFileGuards(), harness.WithoutAutoWatch())
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "docs", judgeRule, map[string]string{"rubric.md.j2": rubric})
	e.CommitAll(proj, "the rule")
	e.InstallJudgeClaudeCapturing(proj, promptFile, `{"pass": false, "reasoning": "the date is not in the sources"}`)
	return e, proj
}

func TestT056_20_WithoutTheVariableACommitIsNotAutoWatched(t *testing.T) {
	e, proj := unwatchedFailingProject(t)
	const sess = "s-056-20a"

	res := e.Run(proj, sess, "write the doc", Turns("done", harness.CommitFile("c1", "docs/a.md", "the release is Friday", "add a")))

	if rs := ranges(t, e, proj, sess); len(rs) != 0 {
		t.Fatalf("the engine tracked a ref on its own with SR_AUTO_WATCH_GIT_REFS unset: %+v", rs)
	}
	if got := e.AllBlockingErrorsFrom(proj, sess, "Stop"); len(got) != 0 {
		t.Fatalf("the Stop verified a range nobody asked to watch:\n%s", strings.Join(got, "\n"))
	}
	if strings.Contains(res.Output, "untracked") {
		t.Fatalf("the Stop mentioned untracked refs:\n%s", res.Output)
	}
}

func TestT056_20_AManuallyTrackedRefIsStillVerifiedAtStop(t *testing.T) {
	e, proj := unwatchedFailingProject(t)
	const sess = "s-056-20b"

	e.Run(proj, sess, "write the doc and watch it", Turns("done",
		harness.CommitFile("c1", "docs/a.md", "the release is Friday", "add a"),
		Bash("t1", "sr-session refs track --base "+e.Git(proj, "rev-parse", "HEAD~1")),
	))

	rs := ranges(t, e, proj, sess)
	if len(rs) != 1 || !rs[0].Tracked() || rs[0].AddedBy != sessionstate.RangeAgent {
		t.Fatalf("the agent's own `refs track` is not a tracked range: %+v", rs)
	}
	if joined := strings.Join(e.BlockingErrorsFrom(proj, sess, "Stop"), "\n"); !strings.Contains(joined, failedText) {
		t.Fatalf("a manually tracked range with a stored FAIL was not refused at Stop:\n%s", joined)
	}
	if r := refs(e, proj, sess, "untrack", "--reason", "the user's own work"); r.Code != 0 {
		t.Fatalf("untrack of a manually tracked ref: exit %d:\n%s", r.Code, r.Output)
	}
}
