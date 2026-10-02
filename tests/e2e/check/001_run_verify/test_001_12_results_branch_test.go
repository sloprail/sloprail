package e2e

import (
	"encoding/json"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// T001_12: `run` stores what it judged on the branch sloprail/checks of origin, as a10n-shaped
// runs (rule, range, checks with their status and fingerprint), and never checks it out.
func TestT001_12_RunPushesItsRunsToTheResultsBranch(t *testing.T) {
	e, proj, base := judgeProject(t, verdictPass)
	commitDoc(e, proj, "docs/a.md", "the release is Friday\n", "add a")
	head := e.Git(proj, "rev-parse", "HEAD")

	if r := run(e, proj, base, "HEAD"); r.Code != 0 {
		t.Fatalf("run: exit %d:\n%s", r.Code, r.Output)
	}

	if out := e.Git(proj, "ls-remote", "--heads", e.Origin(proj), "sloprail/checks"); !strings.Contains(out, "refs/heads/sloprail/checks") {
		t.Fatalf("origin has no sloprail/checks branch:\n%s", out)
	}
	if tree := e.Git(proj, "ls-tree", "-r", "--name-only", "refs/sloprail/checks"); !strings.Contains(tree, "/seg/") || strings.Contains(tree, "docs/a.md") {
		t.Fatalf("the branch holds more or less than segments:\n%s", tree)
	}
	runs := e.CacheRecords(proj)
	var judgeRun bool
	for _, r := range runs {
		if r.Rule != "file-guard/docs" {
			continue
		}
		if r.HeadRef != head || r.BaseRef != base || !r.Complete {
			t.Fatalf("the run does not record its range and finish: %+v", r)
		}
		// One verdict per guard x subject: the guard's record holds the pass and its
		// fingerprint, and the steps inside it include the judge's pass.
		for _, c := range r.Checks {
			if c.Kind != "guard" || c.Status != "pass" || c.Fingerprint == "" {
				continue
			}
			if steps, _ := json.Marshal(c.Metadata["steps"]); regexp.MustCompile(`"kind":"[^"]*judge[^"]*","status":"pass"`).Match(steps) {
				judgeRun = true
			}
		}
	}
	if !judgeRun {
		t.Fatalf("no finished run of file-guard/docs holds a guard pass with its fingerprint and the judge's pass among its steps: %+v", runs)
	}
	if status := e.Git(proj, "status", "--porcelain"); strings.Contains(status, "sloprail") {
		t.Fatalf("the results branch touched the working tree:\n%s", status)
	}
}

// T001_13: two machines judging at the same time never conflict: both results land, and a third
// clone verifies both ranges without asking a model.
func TestT001_13_ConcurrentPushersDoNotConflict(t *testing.T) {
	e, proj, base := judgeProject(t, verdictPass)
	e.PushBranch(proj, "main")
	a, b := e.CloneFresh(proj), e.CloneFresh(proj)
	// Each clone makes its own change and judges it; the shim is installed in each.
	for _, c := range []string{a, b} {
		e.InstallJudgeClaudeCapturing(c, promptFile, verdictPass)
	}
	commitDoc(e, a, "docs/a.md", "from machine a\n", "add a")
	commitDoc(e, b, "docs/b.md", "from machine b\n", "add b")

	var wg sync.WaitGroup
	codes := make([]int, 2)
	for i, c := range []string{a, b} {
		wg.Add(1)
		go func(i int, c string) {
			defer wg.Done()
			codes[i] = run(e, c, base, "HEAD").Code
		}(i, c)
	}
	wg.Wait()
	if codes[0] != 0 || codes[1] != 0 {
		t.Fatalf("a concurrent run failed (exits %v)", codes)
	}

	// Another machine: both pushes' results are on origin, in one linear history.
	if merges := e.Git(a, "rev-list", "--merges", "--count", "origin/sloprail/checks"); merges != "0" {
		e.Git(a, "fetch", "-q", "origin")
	}
	e.Git(a, "fetch", "-q", "origin")
	if n := e.Git(a, "rev-list", "--count", "origin/sloprail/checks"); n != "2" {
		t.Fatalf("origin's results branch holds %s commits, want 2 (one per pusher)", n)
	}
	if merges := e.Git(a, "rev-list", "--merges", "--count", "origin/sloprail/checks"); merges != "0" {
		t.Fatalf("the results branch has %s merge commits: history must be linear", merges)
	}
	// Each side's content verifies from the OTHER machine's clone, with no judge there.
	e.Git(a, "fetch", "-q", "origin")
	e.Git(b, "fetch", "-q", "origin")
	for _, c := range []string{a, b} {
		e.InstallJudgeClaudeCapturing(c, promptFile, verdictFail) // a call would fail the verify
	}
	if v := verify(e, a, base, "HEAD"); v.Code != 0 {
		t.Fatalf("machine a cannot verify its own result after the race: exit %d:\n%s", v.Code, v.Output)
	}
	if v := verify(e, b, base, "HEAD"); v.Code != 0 {
		t.Fatalf("machine b cannot verify its own result after the race: exit %d:\n%s", v.Code, v.Output)
	}
}
