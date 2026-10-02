package e2e

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T003_48: a commit already on the remote's default branch that the session only
// PULLED or REBASED onto is not the session's work, and is not judged. A commit the
// session made stays judged even after it landed there, until it passed.

const asUpstream = "-c user.name=upstream -c user.email=up@example.com"

// remoteWithUpstream gives proj an origin whose default branch is main, and returns a
// function that lands one more commit upstream (from a second clone).
func remoteWithUpstream(t *testing.T, e *Env, proj string) (land func(path, content, msg string)) {
	t.Helper()
	main := e.Git(proj, "branch", "--show-current")
	bare := e.Origin(proj)
	e.Git(proj, "push", "-q", "origin", "HEAD:refs/heads/"+main)
	e.Git(proj, "fetch", "-q", "origin")
	e.Git(proj, "remote", "set-head", "origin", main)
	up := filepath.Join(t.TempDir(), "up")
	e.Git(filepath.Dir(up), "clone", "-q", bare, up)
	return func(path, content, msg string) {
		t.Helper()
		e.WriteFile(up, path, content)
		e.Git(up, "add", "-A")
		args := append(strings.Fields(asUpstream), "commit", "-q", "-m", msg)
		e.Git(up, args...)
		e.Git(up, "push", "-q", "origin", "HEAD:refs/heads/"+main)
	}
}

func lastFiles(t *testing.T, ledgerPath string) string {
	t.Helper()
	return strings.Join(paths(lastRun(t, ledgerPath).Files), ",")
}

// (1) `git pull --rebase` brings an upstream commit that edits a guarded file: it is not
// judged; the session's own violating commit in the same range still is.
func TestT003_48_APulledUpstreamCommitIsNotJudgedAndOwnWorkStillIs(t *testing.T) {
	e, proj, led := project(t, docsRule)
	land := remoteWithUpstream(t, e, proj)
	main := e.Git(proj, "branch", "--show-current")
	land("docs/upstream.md", "FORBIDDEN but someone else's and reviewed", "upstream edits docs")

	e.Run(proj, "s-003-48a", "pull", Turns("done",
		harness.CommitFile("c1", "docs/a.md", "clean words", "add a"),
		Bash("p1", "git pull --rebase -q origin "+main),
	))
	if got := stopRefusals(e, proj, "s-003-48a"); got != "" {
		t.Fatalf("a commit only pulled from upstream was judged:\n%s", got)
	}
	if got := lastFiles(t, led); got != "docs/a.md" {
		t.Fatalf("the changeset should hold the session's own file only, got %q", got)
	}
	blocks := stopBlocks(e, proj, "s-003-48a")

	land("docs/upstream2.md", "FORBIDDEN again, upstream", "upstream edits docs again")
	e.Run(proj, "s-003-48a", "own violation", Turns("done",
		harness.CommitFile("c2", "docs/b.md", "FORBIDDEN words", "add b"),
		Bash("p2", "git pull --rebase -q origin "+main),
	))
	got := newBlocks(e, proj, "s-003-48a", blocks)
	if !strings.Contains(got, refusalText) {
		t.Fatalf("the session's own violating commit was not refused:\n%s", got)
	}
	if files := lastFiles(t, led); strings.Contains(files, "upstream") || !strings.Contains(files, "docs/b.md") {
		t.Fatalf("the refused changeset should hold the session's files, never upstream's; got %q", files)
	}
}

// (2) a branch cut when origin/main was older, rebased onto a newer origin/main that
// carries a change the session never made: that change is not judged.
func TestT003_48_ABranchRebasedOntoANewerMainIsJudgedOnItsOwnCommits(t *testing.T) {
	e, proj, led := project(t, docsRule)
	land := remoteWithUpstream(t, e, proj)
	land("docs/upstream.md", "FORBIDDEN, upstream, uncited", "upstream edits docs")

	e.Run(proj, "s-003-48b", "rebase", Turns("done",
		Bash("b1", "git switch -q -c feat"),
		harness.CommitFile("c1", "docs/a.md", "clean words", "add a"),
		Bash("b2", "git fetch -q origin && git rebase -q origin/"+e.Git(proj, "branch", "--show-current")),
	))
	if got := stopRefusals(e, proj, "s-003-48b"); got != "" {
		t.Fatalf("a branch rebased onto a newer main was judged over the upstream change:\n%s", got)
	}
	if got := lastFiles(t, led); got != "docs/a.md" {
		t.Fatalf("the changeset should hold the branch's own file only, got %q", got)
	}
}

// (3) a session commit that lands on the default branch by a fast-forward push leaves an EMPTY
// range locally: the base is always the merge base with the remote default branch, so the local
// Stop (early feedback only) judges nothing. CI covers it: it verifies the push event's
// before..after (and a pull request's merge-base(target, head)..head).
func TestT003_48_ASessionCommitThatLandedUpstreamIsNotInTheLocalRangeCIOnPushCoversIt(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	remoteWithUpstream(t, e, proj)
	main := e.Git(proj, "branch", "--show-current")

	e.Run(proj, "s-003-48c", "push", Turns("done",
		harness.CommitFile("c1", "docs/a.md", "FORBIDDEN words", "add a"),
		Bash("p1", "sh "+shipScript(e, proj, "git push -q origin HEAD:refs/heads/"+main+" && git fetch -q origin")), // a script: the verify-before-push gate reads the command line, not the script
	))
	if got := stopRefusals(e, proj, "s-003-48c"); got != "" {
		t.Fatalf("the local Stop judged a commit that landed on the default branch (CI on push covers it):\n%s", got)
	}
}

// (4) a session's feature branch fast-forward-pushed to origin/main (then fetched): same, the range
// is empty locally; CI on push covers it.
func TestT003_48_AFeatureBranchFastForwardedToMainIsNotInTheLocalRangeCIOnPushCoversIt(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	remoteWithUpstream(t, e, proj)
	main := e.Git(proj, "branch", "--show-current")

	e.Run(proj, "s-003-48d", "push", Turns("done",
		Bash("b1", "git switch -q -c feat"),
		harness.CommitFile("c1", "docs/a.md", "FORBIDDEN words", "add a"),
		Bash("p1", "sh "+shipScript(e, proj, "git push -q origin HEAD:refs/heads/"+main+" && git fetch -q origin")), // a script: the verify-before-push gate reads the command line, not the script
	))
	if got := stopRefusals(e, proj, "s-003-48d"); got != "" {
		t.Fatalf("the local Stop judged a feature-branch commit that landed on the default branch (CI on push covers it):\n%s", got)
	}
}

// (5) the session commits AND fast-forward-pushes to origin/main in ONE command: the same, nothing
// is left in the local range; CI on push covers it.
func TestT003_48_ACommitPushedFastForwardInOneCommandIsNotInTheLocalRangeCIOnPushCoversIt(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	remoteWithUpstream(t, e, proj)
	main := e.Git(proj, "branch", "--show-current")

	script := "mkdir -p docs && echo 'FORBIDDEN words' > docs/a.md && git add -A && git commit -q -m 'add a' && git push -q origin HEAD:refs/heads/" + main
	e.WriteFile(proj, "../ship.sh", script)
	e.Run(proj, "s-003-48e", "commit and push", Turns("done",
		Bash("p1", "sh "+filepath.Join(filepath.Dir(proj), "ship.sh")), // one hook window: the verify-before-push gate reads the command line, not the script
	))
	if got := stopRefusals(e, proj, "s-003-48e"); got != "" {
		t.Fatalf("the local Stop judged a commit that landed on the default branch (CI on push covers it):\n%s", got)
	}
}

// shipScript writes a script next to the project that runs command, and returns its path.
func shipScript(e *Env, proj, command string) string {
	e.WriteFile(proj, "../ship-push.sh", "cd "+proj+" && "+command)
	return filepath.Join(filepath.Dir(proj), "ship-push.sh")
}
