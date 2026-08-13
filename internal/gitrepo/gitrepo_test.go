package gitrepo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// initRepo makes a real repository in a temporary directory.
//
// A real one rather than a fake: what is being tested is the reading of git's
// own output, and a stub would only prove this package agrees with a second
// copy of the assumptions it already holds.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "--initial-branch=main")
	git(t, dir, "config", "user.email", "test@example.invalid")
	git(t, dir, "config", "user.name", "Test")
	return dir
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "git %s: %s", strings.Join(args, " "), out)
	return strings.TrimSpace(string(out))
}

func commit(t *testing.T, dir, name, body string) string {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "add "+name)
	return git(t, dir, "rev-parse", "HEAD")
}

func TestHead_ReportsCommitAndBranch(t *testing.T) {
	dir := initRepo(t)
	want := commit(t, dir, "a.txt", "one")

	pos, err := Head(dir)
	require.NoError(t, err)
	assert.Equal(t, want, pos.Commit)
	assert.Equal(t, "main", pos.Branch)
}

func TestHead_BranchChangesWithCheckout(t *testing.T) {
	// The branch is what makes a switch to another line of history noticeable.
	// If it did not change here, nothing downstream could ever detect one.
	dir := initRepo(t)
	commit(t, dir, "a.txt", "one")

	git(t, dir, "checkout", "-b", "feature")
	featureCommit := commit(t, dir, "b.txt", "two")

	pos, err := Head(dir)
	require.NoError(t, err)
	assert.Equal(t, "feature", pos.Branch)
	assert.Equal(t, featureCommit, pos.Commit)

	git(t, dir, "checkout", "main")
	pos, err = Head(dir)
	require.NoError(t, err)
	assert.Equal(t, "main", pos.Branch)
	assert.NotEqual(t, featureCommit, pos.Commit)
}

func TestHead_CommitMovesAlongTheSameBranch(t *testing.T) {
	// Committing is not switching. The branch stays put and only the commit
	// moves, which is what lets a caller tell "the agent committed" apart from
	// "the agent changed lines of history".
	dir := initRepo(t)
	first := commit(t, dir, "a.txt", "one")

	before, err := Head(dir)
	require.NoError(t, err)

	second := commit(t, dir, "b.txt", "two")
	after, err := Head(dir)
	require.NoError(t, err)

	assert.Equal(t, before.Branch, after.Branch)
	assert.Equal(t, first, before.Commit)
	assert.Equal(t, second, after.Commit)
}

func TestHead_DetachedHeadHasNoBranch(t *testing.T) {
	// A rebase, a bisect, or a checkout of a bare commit all produce one. It is
	// a real state rather than a failure, and reporting it as an empty branch
	// means a session that starts detached and stays detached compares equal to
	// itself rather than being re-baselined on every cycle.
	dir := initRepo(t)
	sha := commit(t, dir, "a.txt", "one")
	commit(t, dir, "b.txt", "two")

	git(t, dir, "checkout", "--detach", sha)

	pos, err := Head(dir)
	require.NoError(t, err)
	assert.Equal(t, sha, pos.Commit)
	assert.Empty(t, pos.Branch)
}

func TestHead_RepositoryWithNoCommitIsAnAbsentPosition(t *testing.T) {
	// The very first session in a new project is in this state. There is
	// nothing wrong and nothing to measure from, so it is reported as an empty
	// position rather than as an error.
	dir := initRepo(t)

	pos, err := Head(dir)
	require.NoError(t, err)
	assert.Empty(t, pos.Commit)
}

func TestHead_NotARepositoryIsASentinel(t *testing.T) {
	// A project that is not a repository is an ordinary thing for a person to
	// have. The caller recognises it by the sentinel and carries on, rather
	// than matching on whatever git happened to print.
	dir := t.TempDir()
	// A repository anywhere above the temp directory would make this a
	// repository too; git's own ceiling stops the search here.
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))

	_, err := Head(dir)
	assert.ErrorIs(t, err, ErrNotARepository)
}

func TestHead_UntrackedFilesDoNotAffectThePosition(t *testing.T) {
	// The position is about history, not about the working tree's contents.
	// Asking for untracked files would make this walk the whole tree on every
	// hook run for an answer it does not use.
	dir := initRepo(t)
	want := commit(t, dir, "a.txt", "one")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("x"), 0o644))

	pos, err := Head(dir)
	require.NoError(t, err)
	assert.Equal(t, want, pos.Commit)
	assert.Equal(t, "main", pos.Branch)
}

func TestParseBranchHeaders_IgnoresEntryLines(t *testing.T) {
	// Porcelain v2 interleaves entry lines with the headers, and a path
	// containing spaces splits like a header would. Only the "# name value"
	// shape is read.
	pos, err := parseBranchHeaders(strings.Join([]string{
		"# branch.oid abc123",
		"# branch.head main",
		"1 .M N... 100644 100644 100644 aaa bbb some file.txt",
		"? untracked branch.head evil",
	}, "\n"))
	require.NoError(t, err)
	assert.Equal(t, "abc123", pos.Commit)
	assert.Equal(t, "main", pos.Branch)
}

func TestHead_TwoDetachedHeadsAreTellableApart(t *testing.T) {
	// F2. Both report no branch, so the branch cannot be what distinguishes
	// them — the commit has to, and Contains is what asks.
	dir := initRepo(t)
	first := commit(t, dir, "a.txt", "one")
	second := commit(t, dir, "b.txt", "two")

	git(t, dir, "checkout", "--detach", first)
	a, err := Head(dir)
	require.NoError(t, err)

	git(t, dir, "checkout", "--detach", second)
	b, err := Head(dir)
	require.NoError(t, err)

	require.Equal(t, a.Branch, b.Branch, "both detached, so the branch is the same empty value")
	assert.NotEqual(t, a.Commit, b.Commit, "and the commit is the only thing that differs")
}

func TestHead_DuringARebaseReportsTheBranchBeingRebased(t *testing.T) {
	// F2's other half. A rebase detaches HEAD and moves it at every step, but
	// the line of history being worked on has not changed — and reading each
	// step as a new line would re-baseline a caller several times over one
	// rebase.
	dir := initRepo(t)
	for _, n := range []string{"a", "b", "c", "d"} {
		commit(t, dir, n+".txt", n)
	}

	rebase := exec.Command("git", "rebase", "-i", "HEAD~3")
	rebase.Dir = dir
	rebase.Env = append(os.Environ(),
		"GIT_SEQUENCE_EDITOR=sed -i.bak s/^pick/edit/",
		"GIT_EDITOR=true")
	out, err := rebase.CombinedOutput()
	require.NoErrorf(t, err, "git rebase -i: %s", out)
	t.Cleanup(func() {
		abort := exec.Command("git", "rebase", "--abort")
		abort.Dir = dir
		_ = abort.Run()
	})

	pos, err := Head(dir)
	require.NoError(t, err)
	assert.Equal(t, "main", pos.Branch,
		"a rebase stopped at edit is detached, but the branch being rebased is known")
}

func TestHead_DuringABisectReportsTheBranchBisectFrom(t *testing.T) {
	// The same recovery for the other operation that walks a detached HEAD.
	dir := initRepo(t)
	root := commit(t, dir, "a.txt", "one")
	commit(t, dir, "b.txt", "two")
	commit(t, dir, "c.txt", "three")

	git(t, dir, "bisect", "start", "HEAD", root)
	t.Cleanup(func() {
		reset := exec.Command("git", "bisect", "reset")
		reset.Dir = dir
		_ = reset.Run()
	})

	pos, err := Head(dir)
	require.NoError(t, err)
	assert.Equal(t, "main", pos.Branch, "a bisect records the branch it started from")
}

func TestHead_ACorruptRepositoryIsAFaultRatherThanAnAbsence(t *testing.T) {
	// F4. git says "fatal: not a git repository" for a corrupted HEAD as well
	// as for a directory with no repository at all, so the message alone cannot
	// tell them apart.
	//
	// Reported as a fault because a caller that reads this as an absence stops
	// measuring anything and says nothing about why — the recorded baseline is
	// abandoned in silence, which is the unsafe direction, not the safe one.
	dir := initRepo(t)
	commit(t, dir, "a.txt", "one")
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".git", "HEAD"),
		[]byte("garbage-not-a-ref\n"), 0o644))

	_, err := Head(dir)
	require.Error(t, err, "a repository that cannot be read is not a repository that is absent")
	assert.NotErrorIs(t, err, ErrNotARepository,
		"reported as ErrNotARepository, the caller silently abandons the baseline it had")
}

func TestContains_ReachableAndUnreachable(t *testing.T) {
	// What tells a history the tree is still on from one it has left, in the
	// two cases that matter.
	dir := initRepo(t)
	first := commit(t, dir, "a.txt", "one")
	commit(t, dir, "b.txt", "two")

	got, err := Contains(dir, first)
	require.NoError(t, err)
	assert.True(t, got, "an ancestor of HEAD is reachable")

	// Another line, off the root, that does not contain the tip of main.
	tip := git(t, dir, "rev-parse", "HEAD")
	git(t, dir, "checkout", "-b", "other", first)
	commit(t, dir, "z.txt", "elsewhere")

	got, err = Contains(dir, tip)
	require.NoError(t, err)
	assert.False(t, got, "a commit on the line that was left is not reachable")
}

func TestContains_AnAbsentCommitIsNotReachable(t *testing.T) {
	// A commit git does not have is a real state — a rebase dropped it, a reset
	// discarded it — and it is one where the recorded point is no longer a
	// place this tree can measure from. Not an error to refuse over.
	dir := initRepo(t)
	commit(t, dir, "a.txt", "one")

	got, err := Contains(dir, "0123456789012345678901234567890123456789")
	require.NoError(t, err)
	assert.False(t, got)
}

func TestContains_ABrokenRepositoryIsReportedRatherThanCalledUnreachable(t *testing.T) {
	// The same shape as F4, one level down. "Not reachable" would be a claim
	// about a history nothing managed to read, and a caller acting on it would
	// move its point on the strength of a failure.
	dir := initRepo(t)
	c := commit(t, dir, "a.txt", "one")
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".git", "HEAD"),
		[]byte("garbage-not-a-ref\n"), 0o644))

	_, err := Contains(dir, c)
	assert.Error(t, err, "a repository that cannot be read must not answer the question")
}

func TestHead_RebaseInALinkedWorktreeFindsItsOwnOperation(t *testing.T) {
	// A linked worktree keeps its operation files under .git/worktrees/<name>/
	// rather than in .git/, so joining ".git" onto the directory would look in
	// the wrong place and report a bare detached HEAD. The location comes from
	// `git rev-parse --git-path`, which resolves it per worktree.
	dir := initRepo(t)
	for _, n := range []string{"a", "b", "c", "d"} {
		commit(t, dir, n+".txt", n)
	}
	linked := filepath.Join(t.TempDir(), "linked")
	git(t, dir, "worktree", "add", "-b", "wtbranch", linked)

	rebase := exec.Command("git", "rebase", "-i", "HEAD~3")
	rebase.Dir = linked
	rebase.Env = append(os.Environ(),
		"GIT_SEQUENCE_EDITOR=sed -i.bak s/^pick/edit/",
		"GIT_EDITOR=true")
	out, err := rebase.CombinedOutput()
	require.NoErrorf(t, err, "git rebase -i: %s", out)
	t.Cleanup(func() {
		abort := exec.Command("git", "rebase", "--abort")
		abort.Dir = linked
		_ = abort.Run()
	})

	pos, err := Head(linked)
	require.NoError(t, err)
	assert.Equal(t, "wtbranch", pos.Branch,
		"the operation belongs to this worktree, and is where this worktree keeps it")
}
