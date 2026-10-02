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

// (3) a session commit that later lands on the default branch by a fast-forward push is
// still the session's: judged (and refused) until it passed. There is no landed filtering.
func TestT003_48_ASessionCommitThatLandedUpstreamIsStillJudged(t *testing.T) {
	e, proj, _ := project(t, docsRule)
	remoteWithUpstream(t, e, proj)
	main := e.Git(proj, "branch", "--show-current")

	e.Run(proj, "s-003-48c", "push", Turns("done",
		harness.CommitFile("c1", "docs/a.md", "FORBIDDEN words", "add a"),
		Bash("p1", "git push -q origin HEAD:refs/heads/"+main+" && git fetch -q origin"),
	))
	if got := stopRefusals(e, proj, "s-003-48c"); !strings.Contains(got, refusalText) {
		t.Fatalf("a session commit that landed upstream was not judged:\n%s", got)
	}
	blocks := stopBlocks(e, proj, "s-003-48c")

	e.Run(proj, "s-003-48c", "fix", Turns("fixed", harness.CommitFile("c2", "docs/a.md", "clean words", "fix a")))
	if n := stopBlocks(e, proj, "s-003-48c"); n != blocks {
		t.Fatalf("the fixed commit was still refused:\n%s", newBlocks(e, proj, "s-003-48c", blocks))
	}
}
