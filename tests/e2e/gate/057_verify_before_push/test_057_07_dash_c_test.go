package e2e

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// T057_07: `git -C <other repo> push` from a cwd in a different repo is verified in the OTHER repo:
// its refused commit does not leave, and the gate does not judge the session's own (clean) repo.
func TestT057_07_DashCPushIsVerifiedInTheTargetRepo(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	oth := e.Project()
	e.GitInit(oth)
	e.WriteFile(oth, "docs/seed.md", "seed\n")
	e.CommitAll(oth, "the other project")
	e.FileGuard(oth, "docs", docsRule, map[string]string{"check.sh": recorder(filepath.Join(t.TempDir(), "l.jsonl"))})
	e.CommitAll(oth, "the rule")
	e.WriteFile(oth, "docs/a.md", "FORBIDDEN words")
	e.CommitAll(oth, "add a")

	res := e.Run(proj, "s-057-07", "push the other repo", Turns("done",
		Bash("p", "git -C "+oth+" push -q origin HEAD:refs/heads/work"),
	))
	if !res.Refused() {
		t.Fatalf("a -C push of refused commits was not refused:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, oth) {
		t.Fatalf("the refusal does not name the target repo %s:\n%s", oth, res.Output)
	}
	if exec.Command("git", "-C", e.Origin(oth), "rev-parse", "--verify", "-q", "refs/heads/work").Run() == nil {
		t.Fatal("the refused commits were pushed")
	}
}
