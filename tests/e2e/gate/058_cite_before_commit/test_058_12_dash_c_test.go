package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// other makes a second repository with the same citation-guarded docs rule, beside the session's
// own project: the target of a `git -C <other> commit` run from the project's cwd.
func other(t *testing.T, e *Env) string {
	t.Helper()
	dir := e.Project()
	e.GitInit(dir)
	e.WriteFile(dir, "docs/seed.md", "seed\n")
	e.WriteFile(dir, "src/seed.go", "package seed\n")
	e.CommitAll(dir, "the other project")
	e.FileGuard(dir, "cited-docs", citedDocs, map[string]string{"ok.sh": "#!/bin/sh\nexit 0\n"})
	e.CommitAll(dir, "the rule")
	return dir
}

func stageIn(id, dir, path, body string) string {
	return "mkdir -p \"$(dirname " + dir + "/" + path + ")\" && printf '%s' '" + body + "' > " + dir + "/" + path + " && git -C " + dir + " add " + path
}

// T058_12: `git -C <other repo> commit` from a cwd in a different repo is checked against the OTHER
// repo: a guarded file there needs a cite (the citation refusal, not "could not check").
func TestT058_12_DashCCommitIsCheckedInTheTargetRepo(t *testing.T) {
	e, proj := project(t)
	oth := other(t, e)
	res := e.Run(proj, "s-058-12", prompt, Turns("done",
		Bash("a", stageIn("a", oth, "docs/a.md", "a")),
		Bash("c", "git -C "+oth+" commit -q -m 'add a'"),
	))
	if !res.Refused() {
		t.Fatalf("an uncited -C commit of a guarded file was not refused:\n%s", res.Output)
	}
	has(t, res.Output, "docs/a.md")
	has(t, res.Output, "Sloprail-Cites-User")
	if strings.Contains(res.Output, "could not check") {
		t.Fatalf("the gate could not check a -C commit:\n%s", res.Output)
	}
	if strings.Contains(e.Git(oth, "log", "--format=%s"), "add a") {
		t.Fatal("the refused commit was made")
	}
}

// T058_13: the same from a cwd in a different repo, committing nothing guarded: allowed.
func TestT058_13_DashCCommitOfUnguardedFileIsAllowed(t *testing.T) {
	e, proj := project(t)
	oth := other(t, e)
	res := e.Run(proj, "s-058-13", prompt, Turns("done",
		Bash("a", stageIn("a", oth, "src/x.go", "package x")),
		Bash("c", "git -C "+oth+" commit -q -m 'add x'"),
	))
	if res.Refused() {
		t.Fatalf("a -C commit of an unguarded file was refused:\n%s", res.Output)
	}
	has(t, e.Git(oth, "log", "--format=%s"), "add x")
}

// T058_14: a -C commit with a resolving trailer passes.
func TestT058_14_DashCCitedCommitPasses(t *testing.T) {
	e, proj := project(t)
	oth := other(t, e)
	res := e.Run(proj, "s-058-14", prompt, Turns("done",
		Bash("a", stageIn("a", oth, "docs/a.md", "a")),
		Bash("c", "git -C "+oth+" commit -q -m 'add a' -m 'Sloprail-Cites-User: "+quote+"'"),
	))
	if res.Refused() {
		t.Fatalf("a cited -C commit was refused:\n%s", res.Output)
	}
	has(t, e.Git(oth, "log", "--format=%s"), "add a")
}

// T058_15: `git -C <other> commit --amend` is judged in the other repo too.
func TestT058_15_DashCAmendIsCheckedInTheTargetRepo(t *testing.T) {
	e, proj := project(t)
	oth := other(t, e)
	res := e.Run(proj, "s-058-15", prompt, Turns("done",
		Bash("a", stageIn("a", oth, "docs/seed.md", "changed")),
		Bash("c", "git -C "+oth+" commit -q --amend --no-edit"),
	))
	if !res.Refused() {
		t.Fatalf("an uncited -C amend touching a guarded file was not refused:\n%s", res.Output)
	}
	has(t, res.Output, "docs/seed.md")
	if strings.Contains(res.Output, "could not check") {
		t.Fatalf("the gate could not check a -C amend:\n%s", res.Output)
	}
}

// T058_16: an amend of an unguarded file in the other repo is allowed.
func TestT058_16_DashCAmendOfUnguardedFileIsAllowed(t *testing.T) {
	e, proj := project(t)
	oth := other(t, e)
	res := e.Run(proj, "s-058-16", prompt, Turns("done",
		Bash("a", stageIn("a", oth, "src/seed.go", "package seed2")),
		Bash("c", "git -C "+oth+" commit -q --amend --no-edit"),
	))
	if res.Refused() {
		t.Fatalf("a -C amend of an unguarded file was refused:\n%s", res.Output)
	}
}

// T058_17: a folder that cannot be told (a cd to a variable) is allowed with a one-line note: the
// file-guards check the citation at Stop and in CI.
func TestT058_17_UnknownFolderIsAllowedWithANote(t *testing.T) {
	e, proj := project(t)
	res := e.Run(proj, "s-058-17", prompt, Turns("done",
		stage("a", "src/x.go", "package x"),
		Bash("c", "d=.; cd $d && git commit -q -m 'add x'"),
	))
	if res.Refused() {
		t.Fatalf("a commit in an unknowable folder was refused:\n%s", res.Output)
	}
}

// T058_20: a throwaway repository created and committed in by the same command (its folder does not
// exist at check time) is allowed.
func TestT058_20_FolderCreatedByTheCommandIsAllowed(t *testing.T) {
	e, proj := project(t)
	probe := filepath.Join(t.TempDir(), "probe", "repo")
	res := e.Run(proj, "s-058-20", prompt, Turns("done",
		Bash("c", "mkdir -p "+probe+" && git -C "+probe+" init -q && git -C "+probe+" -c commit.gpgsign=false -c user.email=a@b -c user.name=a commit -q --allow-empty -m init"),
	))
	if res.Refused() {
		t.Fatalf("a commit in a folder the command creates was refused:\n%s", res.Output)
	}
}

// T058_21: an existing repository with no `require: citation` rules is allowed, even when the hook's
// own project has one.
func TestT058_21_RuleLessRepositoryIsAllowed(t *testing.T) {
	e, proj := project(t)
	bare := e.Project()
	e.GitInit(bare)
	e.WriteFile(bare, "docs/seed.md", "seed\n")
	e.CommitAll(bare, "plain")
	res := e.Run(proj, "s-058-21", prompt, Turns("done",
		Bash("a", stageIn("a", bare, "docs/a.md", "a")),
		Bash("c", "git -C "+bare+" commit -q -m 'add a'"),
	))
	if res.Refused() {
		t.Fatalf("a commit in a rule-less repository was refused:\n%s", res.Output)
	}
}

// T058_22: a repository whose rules require a citation still needs one (nothing relaxed there).
func TestT058_22_RuledRepositoryStillRefused(t *testing.T) {
	e, proj := project(t)
	oth := other(t, e)
	res := e.Run(proj, "s-058-22", prompt, Turns("done",
		Bash("a", stageIn("a", oth, "docs/a.md", "a")),
		Bash("c", "git -C "+oth+" commit -q -m 'add a'"),
	))
	if !res.Refused() {
		t.Fatalf("a commit in a ruled repository was not refused:\n%s", res.Output)
	}
}

// T058_18: `git -C link/..` with link -> <other>/docs/sub is the parent of the symlink's TARGET, the
// way git resolves it: the commit is checked in <other>/docs, not in the lexical parent of link.
func TestT058_18_DashCThroughASymlinkIsResolvedPhysically(t *testing.T) {
	e, proj := project(t)
	oth := other(t, e)
	e.WriteFile(oth, "docs/sub/.keep", "")
	e.CommitAll(oth, "sub")
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(filepath.Join(oth, "docs", "sub"), link); err != nil {
		t.Fatal(err)
	}
	res := e.Run(proj, "s-058-18", prompt, Turns("done",
		Bash("a", stageIn("a", oth, "docs/a.md", "a")),
		Bash("c", "git -C "+link+"/.. commit -q -m 'add a'"),
	))
	if !res.Refused() {
		t.Fatalf("a commit through a symlinked -C was not checked in the physical folder:\n%s", res.Output)
	}
	has(t, res.Output, "docs/a.md")
	if strings.Contains(e.Git(oth, "log", "--format=%s"), "add a") {
		t.Fatal("the refused commit was made")
	}
}

// T058_19: GIT_DIR / GIT_WORK_TREE / GIT_INDEX_FILE in front of git are not replayed: fail closed
// with the could-not-check message rather than judging a different repository.
func TestT058_19_GitEnvRedirectionFailsClosed(t *testing.T) {
	for i, assign := range []string{"GIT_DIR=", "GIT_WORK_TREE=", "GIT_INDEX_FILE="} {
		e, proj := project(t)
		oth := other(t, e)
		res := e.Run(proj, "s-058-19-"+string(rune('a'+i)), prompt, Turns("done",
			Bash("c", assign+oth+"/x git -C "+oth+" commit -q -m 'add x' --allow-empty"),
		))
		if !res.Refused() {
			t.Fatalf("%s before git commit was not refused:\n%s", assign, res.Output)
		}
		has(t, res.Output, "could not check")
		has(t, res.Output, "git -C <literal dir> commit")
	}
}

// T058_23: a commit message that merely MENTIONS GIT_DIR is not a redirection and is not refused for
// it; a real assignment still is (T058_19, T058_24).
func TestT058_23_MessageMentioningGitDirIsNotARedirection(t *testing.T) {
	e, proj := project(t)
	oth := other(t, e)
	res := e.Run(proj, "s-058-23", prompt, Turns("done",
		Bash("c", "git -C "+oth+" commit -q --allow-empty -m \"mentions GIT_DIR and GIT_INDEX_FILE=x\""),
	))
	if res.Refused() {
		t.Fatalf("a message mentioning GIT_DIR was refused:\n%s", res.Output)
	}
}

// T058_24: GIT_DIR=x git commit is still refused (fail closed).
func TestT058_24_RealGitDirAssignmentStillRefused(t *testing.T) {
	e, proj := project(t)
	oth := other(t, e)
	res := e.Run(proj, "s-058-24", prompt, Turns("done",
		Bash("c", "GIT_DIR="+oth+"/.git git -C "+oth+" commit -q --allow-empty -m x"),
	))
	if !res.Refused() {
		t.Fatalf("a real GIT_DIR assignment was not refused:\n%s", res.Output)
	}
}

// T058_25: the other spellings of a redirection are read from the parsed invocation too: an `env`
// wrapper, an `export` earlier on the line, and git's own --git-dir.
func TestT058_25_OtherRedirectionSpellingsStillRefused(t *testing.T) {
	for i, cmd := range []string{
		"env GIT_DIR=%s/.git git -C %s commit -q --allow-empty -m x",
		"export GIT_DIR=%s/.git; git -C %s commit -q --allow-empty -m x",
		"git --git-dir=%s/.git -C %s commit -q --allow-empty -m x",
		"declare -x GIT_DIR=%s/.git; git -C %s commit -q --allow-empty -m x",
		"typeset -x GIT_DIR=%s/.git; git -C %s commit -q --allow-empty -m x",
		"declare -gx GIT_DIR=%s/.git; git -C %s commit -q --allow-empty -m x",
		"f() { local -x GIT_DIR=%s/.git; git -C %s commit -q --allow-empty -m x; }; f",
		"declare $OPT GIT_DIR=%s/.git; git -C %s commit -q --allow-empty -m x",
		"f() { export GIT_DIR=%s/.git; }; f; git -C %s commit -q --allow-empty -m x",
		"f() { declare -gx GIT_DIR=%s/.git; }; f; git -C %s commit -q --allow-empty -m x",
	} {
		e, proj := project(t)
		oth := other(t, e)
		res := e.Run(proj, "s-058-25-"+string(rune('a'+i)), prompt, Turns("done",
			Bash("c", fmt.Sprintf(cmd, oth, oth)),
		))
		if !res.Refused() {
			t.Fatalf("%q was not refused:\n%s", cmd, res.Output)
		}
		has(t, res.Output, "could not check")
	}
}

// T058_26: a function's `local -x` stays in its body: it is not a redirection of the commit after it.
func TestT058_26_LocalExportInAFunctionDoesNotLeak(t *testing.T) {
	e, proj := project(t)
	oth := other(t, e)
	res := e.Run(proj, "s-058-26", prompt, Turns("done",
		Bash("c", "f() { local -x GIT_DIR="+oth+"/.git; }; f; git -C "+oth+" commit -q --allow-empty -m x"),
	))
	if res.Refused() {
		t.Fatalf("a function's local export was read as a redirection:\n%s", res.Output)
	}
}
