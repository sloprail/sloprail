package e2e

import (
	"strings"
	"testing"
)

// T058_39: a git invocation whose options or subcommand the engine could not resolve may be a commit:
// the gate fires on it and refuses, fail closed, telling the agent to use the literal subcommand. This
// holds through the wrappers that carry it.
func TestT058_39_HiddenSubcommandFailsClosed(t *testing.T) {
	for i, cmd := range []string{
		"git $UNSET_SUB commit -q --allow-empty -m x",
		"git \"$(echo commit)\" -q --allow-empty -m x",
		"git -C %s $UNSET_SUB -q --allow-empty -m x",
		"git $UNSET_SUB",
		"timeout $T git commit -q --allow-empty -m x",
		"env -S \"$A\" git commit -q --allow-empty -m x",
		"env $E git commit -q --allow-empty -m x",
		"timeout -k $K 5 git commit -q --allow-empty -m x",
		"timeout $T -- git commit -q --allow-empty -m x",
		"env -iS 'git commit -q --allow-empty -m x'",
	} {
		e, proj := project(t)
		oth := other(t, e)
		c := strings.ReplaceAll(cmd, "%s", oth)
		res := e.Run(proj, "s-058-39-"+string(rune('a'+i)), prompt, Turns("done", Bash("c", c)))
		if !res.Refused() {
			t.Fatalf("%q was not refused:\n%s", c, res.Output)
		}
		if i < 4 {
			has(t, res.Output, "literal subcommand")
		}
	}
}

// T058_40: `builtin cd` and `command cd` move the shell like `cd`: a literal folder is the folder the
// commit is checked in, one that is a variable the line never assigned is refused; `env -S 'cmd'` is
// read as the command it runs.
func TestT058_40_BuiltinAndCommandCdAndEnvSplitAreTracked(t *testing.T) {
	for i, cmd := range []string{
		"builtin cd %s && git commit -q -m 'add a'",
		"command cd %s && git commit -q -m 'add a'",
		"D=%s; builtin cd \"$D\"; git commit -q -m 'add a'",
		"D=%s; command cd $D; git commit -q -m 'add a'",
		"env -S 'git -C %s commit -q -m \"add a\"'",
	} {
		e, proj := project(t)
		oth := other(t, e)
		c := strings.ReplaceAll(cmd, "%s", oth)
		res := e.Run(proj, "s-058-40-"+string(rune('a'+i)), prompt, Turns("done",
			Bash("a", stageIn("a", oth, "docs/a.md", "a")),
			Bash("c", c),
		))
		if !res.Refused() {
			t.Fatalf("%q: an uncited commit of a guarded file was not refused:\n%s", c, res.Output)
		}
		has(t, res.Output, "docs/a.md")
		has(t, res.Output, "Sloprail-Cites-User")
		if strings.Contains(e.Git(oth, "log", "--format=%s"), "add a") {
			t.Fatalf("%q: the refused commit was made", c)
		}
	}
	for i, cmd := range []string{
		"builtin cd \"$UNSET_DIR\"; git commit -q --allow-empty -m x",
		"command cd $UNSET_DIR && git commit -q --allow-empty -m x",
	} {
		e, proj := project(t)
		res := e.Run(proj, "s-058-40-u"+string(rune('a'+i)), prompt, Turns("done", Bash("c", cmd)))
		if !res.Refused() {
			t.Fatalf("%q was not refused:\n%s", cmd, res.Output)
		}
		has(t, res.Output, "could not check")
	}
}
