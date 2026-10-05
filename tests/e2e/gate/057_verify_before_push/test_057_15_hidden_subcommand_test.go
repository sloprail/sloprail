package e2e

import (
	"strings"
	"testing"
)

// T057_15: a git invocation whose options or subcommand the engine could not resolve (a variable the
// line never assigned, a substitution) may be a push: the gate fires on it and refuses, fail closed,
// telling the agent to use the literal subcommand. This holds through the wrappers that carry it.
func TestT057_15_HiddenSubcommandFailsClosed(t *testing.T) {
	for i, cmd := range []string{
		"git $UNSET_SUB push -q origin HEAD:refs/heads/work",
		"git \"$(echo push)\" -q origin HEAD:refs/heads/work",
		"git -C %s $UNSET_SUB -q origin HEAD:refs/heads/work",
		"git $UNSET_SUB",
		"timeout $T git push -q origin HEAD:refs/heads/work",
		"env -S \"$A\" git push -q origin HEAD:refs/heads/work",
		"env $E git push -q origin HEAD:refs/heads/work",
		"nice $N git push -q origin HEAD:refs/heads/work",
		"timeout -k $K 5 git push -q origin HEAD:refs/heads/work",
		"timeout $T -- git push -q origin HEAD:refs/heads/work",
		"env -iS 'git push -q origin HEAD:refs/heads/work'",
		"env -S 'git\\_push -q origin HEAD:refs/heads/work'",
	} {
		e, proj, _ := project(t, docsRule)
		oth, pushed := unverifiedOther(t, e)
		c := strings.ReplaceAll(cmd, "%s", oth)
		res := e.Run(proj, "s-057-15-"+string(rune('a'+i)), "push", Turns("done", Bash("p", c)))
		if !res.Refused() {
			t.Fatalf("%q was not refused:\n%s", c, res.Output)
		}
		if i < 4 && !strings.Contains(res.Output, "literal subcommand") {
			t.Fatalf("%q: the refusal does not tell the agent to use the literal subcommand:\n%s", c, res.Output)
		}
		if pushed() {
			t.Fatalf("%q: commits were pushed", c)
		}
	}
}

// T057_18: a word lost in front of `git` (a wrapper's value) or in an argument of a command that is not a
// push is none of the gates' business: it is not refused.
func TestT057_18_LostWordsAroundANonPushAreNotRefused(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	res := e.Run(proj, "s-057-18", "look around", Turns("done",
		Bash("a", "timeout $T git status -s"),
		Bash("b", "nice -n $N git log -1"),
		Bash("c", "git status -s \"$X\""),
	))
	if res.Refused() {
		t.Fatalf("a non-push with a lost word was refused:\n%s", res.Output)
	}
}

// T057_16: `builtin cd` and `command cd` move the shell like `cd`: a literal folder is the folder the
// push runs in (its commits are judged), one that is a variable the line never assigned is refused.
func TestT057_16_BuiltinAndCommandCdAreTracked(t *testing.T) {
	for i, cmd := range []string{
		"builtin cd %s && git push -q origin HEAD:refs/heads/work",
		"command cd %s && git push -q origin HEAD:refs/heads/work",
		"D=%s; builtin cd \"$D\"; git push -q origin HEAD:refs/heads/work",
		"D=%s; command cd $D; git push -q origin HEAD:refs/heads/work",
		"command -p cd %s && git push -q origin HEAD:refs/heads/work",
		"env -S 'git -C %s push -q origin HEAD:refs/heads/work'",
		"env -i -S'git -C %s push -q origin HEAD:refs/heads/work'",
	} {
		e, proj, _ := project(t, docsRule)
		oth, pushed := unverifiedOther(t, e)
		c := strings.ReplaceAll(cmd, "%s", oth)
		res := e.Run(proj, "s-057-16-"+string(rune('a'+i)), "push the other repo", Turns("done", Bash("p", c)))
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
	for i, cmd := range []string{
		"builtin cd \"$UNSET_DIR\"; git push -q origin HEAD:refs/heads/work",
		"command cd $UNSET_DIR && git push -q origin HEAD:refs/heads/work",
	} {
		e, proj, _ := project(t, docsRule)
		res := e.Run(proj, "s-057-16-u"+string(rune('a'+i)), "push", Turns("done", Bash("p", cmd)))
		if !res.Refused() {
			t.Fatalf("%q was not refused:\n%s", cmd, res.Output)
		}
		if !strings.Contains(res.Output, "literal") {
			t.Fatalf("%q: the refusal does not tell the agent to write the literal:\n%s", cmd, res.Output)
		}
	}
}

// T057_17: a git command whose subcommand is hidden is refused by the results-branch gate too, and one
// whose subcommand is literal (or a gap only in a message) is left alone.
func TestT057_17_HiddenSubcommandIsRefusedByTheResultsBranchGate(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	res := e.Run(proj, "s-057-17", "write it", Turns("done", Bash("w", "git $UNSET_SUB update-ref refs/heads/x HEAD")))
	if !res.Refused() || !res.Saw("checks-ref-sr-only") {
		t.Fatalf("a hidden subcommand was not refused by checks-ref-sr-only:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "literal subcommand") {
		t.Fatalf("the refusal does not tell the agent to use the literal subcommand:\n%s", res.Output)
	}
	res = e.Run(proj, "s-057-17", "look", Turns("done", Bash("r", "git log -1 --format=%s \"$(git rev-parse HEAD)\"")))
	if res.Refused() {
		t.Fatalf("a read with a substitution in its argument was refused:\n%s", res.Output)
	}
}
