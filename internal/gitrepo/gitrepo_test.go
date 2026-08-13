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
