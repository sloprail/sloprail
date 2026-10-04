package e2e

import (
	"strings"
	"testing"
)

// T058_34: a folder the line assigns to a shell variable earlier is the folder the commit runs in:
// every spelling of the assignment is resolved and the commit is checked in THAT repository (the
// session's own project would not need a cite for it, so judging it instead lets the commit through).
func TestT058_34_FolderFromAVariableTheLineAssignsIsCheckedInThatRepo(t *testing.T) {
	for i, cmd := range []string{
		"D=%s; git -C $D commit -q -m 'add a'",
		"D=%s && git -C \"$D\" commit -q -m 'add a'",
		"D=%s; git -C ${D} commit -q -m 'add a'",
		"export D=%s; git -C $D commit -q -m 'add a'",
		"D=%s; PATH=/usr/local/bin:$PATH git -C $D commit -q -m 'add a'",
		"D=%s && cd $D && git commit -q -m 'add a'",
		"D=%s; cd \"$D\"; git commit -q -m 'add a'",
	} {
		e, proj := project(t)
		oth := other(t, e)
		c := strings.ReplaceAll(cmd, "%s", oth)
		res := e.Run(proj, "s-058-34-"+string(rune('a'+i)), prompt, Turns("done",
			Bash("a", stageIn("a", oth, "docs/a.md", "a")),
			Bash("c", c),
		))
		if !res.Refused() {
			t.Fatalf("%q: an uncited commit of a guarded file was not refused:\n%s", c, res.Output)
		}
		has(t, res.Output, "docs/a.md")
		has(t, res.Output, "Sloprail-Cites-User")
		if strings.Contains(res.Output, "could not check") {
			t.Fatalf("%q: the gate could not check it:\n%s", c, res.Output)
		}
		if strings.Contains(e.Git(oth, "log", "--format=%s"), "add a") {
			t.Fatalf("%q: the refused commit was made", c)
		}
	}
}

// T058_35: the same spellings let a cited commit, and a commit of an unguarded file, through.
func TestT058_35_FolderFromAVariableIsPermittedWhenCitedOrUnguarded(t *testing.T) {
	e, proj := project(t)
	oth := other(t, e)
	res := e.Run(proj, "s-058-35", prompt, Turns("done",
		Bash("a", stageIn("a", oth, "docs/a.md", "a")),
		Bash("c", "D="+oth+"; git -C $D commit -q -m 'add a' -m 'Sloprail-Cites-User: "+quote+"'"),
		Bash("b", stageIn("b", oth, "src/x.go", "package x")),
		Bash("d", "D="+oth+" && cd $D && git commit -q -m 'add x'"),
	))
	if res.Refused() {
		t.Fatalf("a cited or unguarded commit through a variable was refused:\n%s", res.Output)
	}
	log := e.Git(oth, "log", "--format=%s")
	has(t, log, "add a")
	has(t, log, "add x")
}

// T058_36: a word the engine cannot resolve where the folder or the subcommand stands fails closed
// with the could-not-check reason, which tells the agent to write the literal folder.
func TestT058_36_UnresolvableFolderFailsClosed(t *testing.T) {
	for i, cmd := range []string{
		"git -C $(pwd)/x commit -q --allow-empty -m x",
		"git -C \"$(pwd)\" commit -q --allow-empty -m x",
		"git -C $UNSET_DIR commit -q --allow-empty -m x",
		"git -C ${HOME}/x commit -q --allow-empty -m x",
		"git -C ${D:-/x} commit -q --allow-empty -m x",
		"D=$(pwd); git -C $D commit -q --allow-empty -m x",
		"D=/x; read D; git -C $D commit -q --allow-empty -m x",
		"git $OPTS commit -q --allow-empty -m x",
		"git -C ~/x commit -q --allow-empty -m x",
	} {
		e, proj := project(t)
		res := e.Run(proj, "s-058-36-"+string(rune('a'+i)), prompt, Turns("done", Bash("c", cmd)))
		if !res.Refused() {
			t.Fatalf("%q was not refused:\n%s", cmd, res.Output)
		}
		has(t, res.Output, "could not check")
		has(t, res.Output, "git -C <literal dir> commit")
	}
}

// T058_37: an unresolvable word in the message (a substitution) is not a reason to refuse a commit
// whose folder is known.
func TestT058_37_UnresolvableMessageIsNotRefused(t *testing.T) {
	e, proj := project(t)
	oth := other(t, e)
	res := e.Run(proj, "s-058-37", prompt, Turns("done",
		Bash("c", "git -C "+oth+" commit -q --allow-empty -m \"$(printf hello)\""),
	))
	if res.Refused() {
		t.Fatalf("a commit with a substituted message was refused:\n%s", res.Output)
	}
}
