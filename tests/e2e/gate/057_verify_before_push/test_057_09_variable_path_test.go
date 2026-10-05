package e2e

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// unverifiedOther is a second repository, with a bare origin, whose last commit a file-guard
// refuses: a push of it must be refused whatever way the command line spells its folder.
func unverifiedOther(t *testing.T, e *Env) (oth string, pushed func() bool) {
	t.Helper()
	oth = e.Project()
	e.GitInit(oth)
	e.WriteFile(oth, "docs/seed.md", "seed\n")
	e.CommitAll(oth, "the other project")
	e.FileGuard(oth, "docs", docsRule, map[string]string{"check.sh": recorder(filepath.Join(t.TempDir(), "l.jsonl"))})
	e.CommitAll(oth, "the rule")
	e.WriteFile(oth, "docs/a.md", "FORBIDDEN words")
	e.CommitAll(oth, "add a")
	bare := e.Origin(oth)
	return oth, func() bool {
		return exec.Command("git", "-C", bare, "rev-parse", "--verify", "-q", "refs/heads/work").Run() == nil
	}
}

// T057_10: a folder the line assigns to a shell variable earlier is the folder the push runs in:
// every spelling of the assignment is resolved, and the other repository's commits are judged. The
// session's own project is clean, so judging it instead would let the push through.
func TestT057_10_DashCFromAVariableTheLineAssignsIsVerifiedInThatRepo(t *testing.T) {
	for i, cmd := range []string{
		"D=%s; git -C $D push -q origin HEAD:refs/heads/work",
		"D=%s && git -C $D push -q origin HEAD:refs/heads/work",
		"D=%s; git -C \"$D\" push -q origin HEAD:refs/heads/work",
		"D=%s; git -C ${D} push -q origin HEAD:refs/heads/work",
		"export D=%s; git -C $D push -q origin HEAD:refs/heads/work",
		"D=%s; PATH=/usr/local/bin:$PATH git -C $D push -q origin HEAD:refs/heads/work",
		"D=%s && cd $D && git push -q origin HEAD:refs/heads/work",
		"D=%s; cd \"$D\"; git push -q origin HEAD:refs/heads/work",
		"A=%s; D=$A; git -C $D push -q origin HEAD:refs/heads/work",
	} {
		e, proj, _ := project(t, docsRule)
		oth, pushed := unverifiedOther(t, e)
		c := strings.ReplaceAll(cmd, "%s", oth)
		res := e.Run(proj, "s-057-10-"+string(rune('a'+i)), "push the other repo", Turns("done", Bash("p", c)))
		if !res.Refused() {
			t.Fatalf("%q was not refused:\n%s", c, res.Output)
		}
		if !strings.Contains(res.Output, oth) {
			t.Fatalf("%q: the refusal does not name the target repo %s:\n%s", c, oth, res.Output)
		}
		if pushed() {
			t.Fatalf("%q: the refused commits were pushed", c)
		}
	}
}

// T057_11: the same spellings let a push of VERIFIED commits through: resolving the variable is not a
// blanket refusal.
func TestT057_11_DashCFromAVariableIsPermittedOnceVerified(t *testing.T) {
	for i, cmd := range []string{
		"D=%s; git -C $D push -q origin HEAD:refs/heads/work",
		"D=%s && cd $D && git push -q origin HEAD:refs/heads/work",
	} {
		e, proj, _ := project(t, docsRule)
		ok := e.Project()
		e.GitInit(ok)
		e.WriteFile(ok, "docs/seed.md", "seed\n")
		e.CommitAll(ok, "the other project")
		e.FileGuard(ok, "docs", docsRule, map[string]string{"check.sh": recorder(filepath.Join(t.TempDir(), "l.jsonl"))})
		e.CommitAll(ok, "the rule")
		e.WriteFile(ok, "docs/a.md", "clean words")
		e.CommitAll(ok, "add a")
		bare := e.Origin(ok)
		root := e.Git(ok, "rev-list", "--max-parents=0", "HEAD")
		c := strings.ReplaceAll(cmd, "%s", ok)
		res := e.Run(proj, "s-057-11-"+string(rune('a'+i)), "judge and push", Turns("done",
			Bash("j", "cd "+ok+" && sr-checks run --base "+root+" --head HEAD"),
			Bash("p", c),
		))
		if res.Refused() {
			t.Fatalf("%q: a verified push was refused:\n%s", c, res.Output)
		}
		if exec.Command("git", "-C", bare, "rev-parse", "--verify", "-q", "refs/heads/work").Run() != nil {
			t.Fatalf("%q: the verified commits were not pushed", c)
		}
	}
}

// T057_12: a word the engine cannot resolve where the folder, the remote or the refspec stands fails
// closed, with a reason that tells the agent to write the literal: the push is judged against nothing
// it could not see, so it is not judged at all.
func TestT057_12_UnresolvableFolderOrRefFailsClosed(t *testing.T) {
	for i, cmd := range []string{
		"git -C $(pwd)/x push -q origin HEAD:refs/heads/work",
		"git -C \"$(pwd)\" push -q origin HEAD:refs/heads/work",
		"git -C $UNSET_DIR push -q origin HEAD:refs/heads/work",
		"git -C ${HOME}/x push -q origin HEAD:refs/heads/work",
		"git -C ${D:-/x} push -q origin HEAD:refs/heads/work",
		"D=$(pwd); git -C $D push -q origin HEAD:refs/heads/work",
		"cd $(pwd) && git push -q origin HEAD:refs/heads/work",
		"cd $UNSET_DIR && git push -q origin HEAD:refs/heads/work",
		"git push -q $REMOTE HEAD:refs/heads/work",
		"git push -q origin $REF",
		"git $OPTS push -q origin HEAD:refs/heads/work",
		"D=/x; read D; git -C $D push -q origin HEAD:refs/heads/work",
		"git -C ~/x push -q origin HEAD:refs/heads/work",
	} {
		e, proj, _ := project(t, docsRule)
		res := e.Run(proj, "s-057-12-"+string(rune('a'+i)), "push", Turns("done", Bash("p", cmd)))
		if !res.Refused() {
			t.Fatalf("%q was not refused:\n%s", cmd, res.Output)
		}
		if !strings.Contains(res.Output, "literal") {
			t.Fatalf("%q: the refusal does not tell the agent to write the literal:\n%s", cmd, res.Output)
		}
	}
}

// T057_13: a variable in a command that is not a push is none of this gate's business, and a plain
// push in a clean repository is unchanged.
func TestT057_13_VariablesElsewhereOnTheLineAreNotRefused(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	res := e.Run(proj, "s-057-13", "look around", Turns("done",
		Bash("a", "git -C \"$(pwd)\" status -s"),
		Bash("b", "git log -1 --format=%s \"$(git rev-parse HEAD)\""),
	))
	if res.Refused() {
		t.Fatalf("a read with a substitution was refused:\n%s", res.Output)
	}
}
