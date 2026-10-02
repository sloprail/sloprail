package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/internal/sessionstate"
)

// T055_01: the session's own repository is registered as its root folder, and a repository a
// command moves history in is registered as an ad-hoc folder, both before anything runs there.
func TestT055_01_RootAndAdHocFoldersAreRegistered(t *testing.T) {
	e, proj, other := two(t)
	const sess = "s-055-01"

	e.Run(proj, sess, "work elsewhere", Turns("done",
		Bash("b1", "git -C "+other+" commit -q --allow-empty -m 'elsewhere'"),
	))

	folders := e.SessionFolders(proj, sess)
	if f, ok := folderAt(folders, real(t, proj)); !ok || f.Role != sessionstate.FolderRoot {
		t.Fatalf("the session's own repository is not its root folder: %+v", folders)
	}
	f, ok := folderAt(folders, real(t, other))
	if !ok || f.Role != sessionstate.FolderAdHoc {
		t.Fatalf("a repository a command committed in is not registered as an ad-hoc folder: %+v", folders)
	}
	if f.BaseRef == "" || f.BaseRef == sessionstate.FolderBaseUnborn {
		t.Fatalf("the ad-hoc folder does not record where work in it began: %+v", f)
	}
}

// T055_02: a repository the session only READS in is not a folder.
func TestT055_02_AReadOnlyCommandRegistersNothing(t *testing.T) {
	e, proj, other := two(t)
	const sess = "s-055-02"

	e.Run(proj, sess, "look elsewhere", Turns("done", Bash("b1", "git -C "+other+" log --oneline")))

	if _, ok := folderAt(e.SessionFolders(proj, sess), real(t, other)); ok {
		t.Fatal("a repository a command only read was registered as a folder")
	}
}

// T055_03: a gate of the OTHER repository judges a command run there: its own rules apply in
// its folder. The session's own project declares no such gate, and its own commands are free.
func TestT055_03_ThatRepositorysGatesJudgeACommandRunThere(t *testing.T) {
	e, proj, other := two(t)
	const sess = "s-055-03"
	e.Gate(other, "no-commits-here", `on:
  - event: PreCommandInvoke
    match: any(event.invocations, .bin == "git")
checks:
  - script: ./refuse.sh
`, map[string]string{"refuse.sh": "#!/bin/sh\ncat >/dev/null\necho '{\"reason\":\"OTHER-REPO-GATE: no commits here\"}'\nexit 1\n"})
	e.CommitAll(other, "the gate")

	res := e.Run(proj, sess, "commit in both", Turns("done",
		Bash("b1", "git commit -q --allow-empty -m 'in the project'"),
		Bash("b2", "git -C "+other+" commit -q --allow-empty -m 'in the other'"),
	))

	if got := strings.Join(res.Refusals(), "\n"); !strings.Contains(got, "OTHER-REPO-GATE") {
		t.Fatalf("the other repository's gate did not judge the command run in it:\n%s", got)
	}
	if n := e.Git(other, "rev-list", "--count", "HEAD"); n != "2" {
		t.Fatalf("the refused commit was made in the other repository (%s commits)", n)
	}
	if n := e.Git(proj, "rev-list", "--count", "HEAD"); n != "2" {
		t.Fatalf("the project's own command was refused by a gate it does not declare (%s commits)", n)
	}
}

// T055_04: commit-required at Stop covers the session's other folders under their own rules:
// an uncommitted change to a path a file-guard of THAT repository selects is refused.
func TestT055_04_CommitRequiredCoversTheOtherFolder(t *testing.T) {
	e, proj, other := two(t)
	const sess = "s-055-04"
	e.FileGuard(other, "docs", "match: \"docs/**\"\nchecks:\n  - script: ./check.sh\n", map[string]string{"check.sh": "#!/bin/sh\ncat >/dev/null\nexit 0\n"})
	e.CommitAll(other, "the rule")

	e.Run(proj, sess, "edit elsewhere", Turns("done",
		Bash("b1", "git -C "+other+" commit -q --allow-empty -m 'register'"),
		Bash("b2", "mkdir -p "+other+"/docs && echo hi > "+other+"/docs/new.md"),
	))

	got := strings.Join(e.BlockingErrorsFrom(proj, sess, "Stop"), "\n")
	if !strings.Contains(got, "Commit your work") || !strings.Contains(got, "docs/new.md") {
		t.Fatalf("an uncommitted guarded path in the session's other folder did not owe a commit:\n%s", got)
	}

	before := len(e.AllBlockingErrorsFrom(proj, sess, "Stop"))
	e.Run(proj, sess, "commit it", Turns("done", Bash("b3", "git -C "+other+" add -A && git -C "+other+" commit -q -m 'docs'")))
	if after := len(e.AllBlockingErrorsFrom(proj, sess, "Stop")); after != before {
		t.Fatalf("a committed folder still owed a commit (%d refusals, had %d)", after, before)
	}
}
