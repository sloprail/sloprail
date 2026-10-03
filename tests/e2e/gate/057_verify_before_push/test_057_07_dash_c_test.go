package e2e

import (
	"fmt"
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

// T057_08: GIT_DIR in front of git is not replayed: the push is refused, not judged in another repo.
func TestT057_08_GitDirBeforePushFailsClosed(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	res := e.Run(proj, "s-057-08", "push", Turns("done",
		Bash("p", "GIT_DIR="+proj+"/.git git push -q origin HEAD:refs/heads/work"),
	))
	if !res.Refused() {
		t.Fatalf("a push under GIT_DIR was not refused:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "GIT_DIR") {
		t.Fatalf("the refusal does not name GIT_DIR:\n%s", res.Output)
	}
}

// T057_09: every way of exporting a redirection is read from the parsed line: an env wrapper, export,
// declare/typeset/local with -x (combined flags too), --git-dir, and a declaration it cannot read.
func TestT057_09_OtherRedirectionSpellingsFailClosed(t *testing.T) {
	for i, cmd := range []string{
		"env GIT_DIR=%s/.git git push -q origin HEAD:refs/heads/work",
		"export GIT_DIR=%s/.git; git push -q origin HEAD:refs/heads/work",
		"declare -x GIT_DIR=%s/.git; git push -q origin HEAD:refs/heads/work",
		"typeset -x GIT_DIR=%s/.git; git push -q origin HEAD:refs/heads/work",
		"declare -gx GIT_DIR=%s/.git; git push -q origin HEAD:refs/heads/work",
		"f() { local -x GIT_DIR=%s/.git; git push -q origin HEAD:refs/heads/work; }; f",
		"declare $OPT GIT_DIR=%s/.git; git push -q origin HEAD:refs/heads/work",
		"git --git-dir=%s/.git push -q origin HEAD:refs/heads/work",
	} {
		e, proj, _ := project(t, docsRule)
		c := fmt.Sprintf(cmd, proj)
		res := e.Run(proj, "s-057-09-"+string(rune('a'+i)), "push", Turns("done", Bash("p", c)))
		if !res.Refused() {
			t.Fatalf("%q was not refused:\n%s", c, res.Output)
		}
	}
}
