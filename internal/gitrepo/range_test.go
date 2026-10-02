package gitrepo

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// commitIn commits a file at a nested path (commit() only writes at the root).
func commitIn(t *testing.T, dir, rel, body string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o644))
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "edit "+rel)
	return git(t, dir, "rev-parse", "HEAD")
}

func TestResolveRange_IsTheMergeBaseOfBaseAndHeadUpToHead(t *testing.T) {
	dir := initRepo(t)
	fork := commitIn(t, dir, "a.txt", "a")
	git(t, dir, "checkout", "-q", "-b", "feature")
	head := commitIn(t, dir, "b.txt", "b")
	git(t, dir, "checkout", "-q", "-")
	commitIn(t, dir, "c.txt", "main moved on")

	_, err := ResolveRange(dir, "--all", "feature")
	require.Error(t, err, "an option is not a revision")

	r, err := ResolveRange(dir, "main", "feature")
	require.NoError(t, err)
	assert.Equal(t, fork, r.Base, "three-dot semantics: a base that moved on does not narrow the range")
	assert.Equal(t, head, r.Head)
}

func TestResolveRange_NamesTheFlagWhoseRevisionDoesNotResolve(t *testing.T) {
	dir := initRepo(t)
	commitIn(t, dir, "a.txt", "a")

	_, err := ResolveRange(dir, "no-such-branch", "HEAD")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--base")

	_, err = ResolveRange(dir, "HEAD", "no-such-branch")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--head")
}

func TestResolveRange_UnrelatedHistoriesAreAnErrorNotAnEmptyRange(t *testing.T) {
	dir := initRepo(t)
	commitIn(t, dir, "a.txt", "a")
	git(t, dir, "checkout", "-q", "--orphan", "other")
	commitIn(t, dir, "b.txt", "b")

	_, err := ResolveRange(dir, "main", "other")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "share no history")
}

func TestResolveRange_AGitFailureFailsClosed(t *testing.T) {
	_, err := ResolveRange(filepath.Join(t.TempDir(), "gone"), "HEAD", "HEAD")
	require.Error(t, err, "a directory git cannot run in is an error, never an empty range")

	_, err = ResolveRange(t.TempDir(), "HEAD", "HEAD")
	require.Error(t, err, "not a repository")
}

func TestResolveRange_ARepositoryWithNoCommitHasNoRange(t *testing.T) {
	dir := initRepo(t)
	_, err := ResolveRange(dir, "HEAD", "HEAD")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not resolve to a commit")
}

func TestRootCommit_IsTheFirstCommitWhateverTheBranch(t *testing.T) {
	dir := initRepo(t)
	first := commitIn(t, dir, "a.txt", "a")
	git(t, dir, "checkout", "-q", "-b", "feature")
	commitIn(t, dir, "b.txt", "b")

	got, err := RootCommit(dir)
	require.NoError(t, err)
	assert.Equal(t, first, got)
}

func TestRootCommit_NoCommitsAndNotARepository(t *testing.T) {
	_, err := RootCommit(initRepo(t))
	require.Error(t, err)

	_, err = RootCommit(t.TempDir())
	require.Error(t, err)
}

func TestHeadPushed_IsWhetherARemoteTrackingBranchContainsHead(t *testing.T) {
	dir := initRepo(t)
	pushed := commitIn(t, dir, "a.txt", "a")

	got, err := HeadPushed(dir)
	require.NoError(t, err)
	assert.False(t, got, "no remote-tracking branch")

	git(t, dir, "update-ref", "refs/remotes/origin/main", pushed)
	got, err = HeadPushed(dir)
	require.NoError(t, err)
	assert.True(t, got)

	commitIn(t, dir, "b.txt", "b")
	got, err = HeadPushed(dir)
	require.NoError(t, err)
	assert.False(t, got, "a commit built on the pushed one is not itself pushed")
}

func TestIsAncestor(t *testing.T) {
	dir := initRepo(t)
	first := commitIn(t, dir, "a.txt", "a")
	second := commitIn(t, dir, "b.txt", "b")

	for _, c := range []struct {
		a, b string
		want bool
	}{{first, second, true}, {second, second, true}, {second, first, false}, {"0123456789012345678901234567890123456789", second, false}} {
		got, err := IsAncestor(dir, c.a, c.b)
		require.NoError(t, err, "%s %s", c.a, c.b)
		assert.Equal(t, c.want, got, "%s %s", c.a, c.b)
	}

	_, err := IsAncestor(t.TempDir(), first, second)
	require.Error(t, err, "not a repository is an error, not 'no'")
}

func TestDefaultBase(t *testing.T) {
	t.Run("a repository with no default branch: git's empty tree", func(t *testing.T) {
		dir := initRepo(t)
		commitIn(t, dir, "a.txt", "a")
		git(t, dir, "branch", "-m", "work")
		assert.Equal(t, EmptyTree, DefaultBase(dir, "HEAD"))
	})
	t.Run("the merge base with the local main", func(t *testing.T) {
		dir := initRepo(t)
		fork := commitIn(t, dir, "a.txt", "a")
		git(t, dir, "checkout", "-q", "-b", "work")
		commitIn(t, dir, "b.txt", "b")
		assert.Equal(t, fork, DefaultBase(dir, "HEAD"))
	})
	t.Run("origin's main wins over a local one", func(t *testing.T) {
		dir := initRepo(t)
		fork := commitIn(t, dir, "a.txt", "a")
		git(t, dir, "update-ref", "refs/remotes/origin/main", fork)
		commitIn(t, dir, "b.txt", "b") // local main moved on
		git(t, dir, "checkout", "-q", "-b", "work")
		commitIn(t, dir, "c.txt", "c")
		assert.Equal(t, fork, DefaultBase(dir, "HEAD"))
	})
	t.Run("a head sharing no history with it: the empty tree", func(t *testing.T) {
		dir := initRepo(t)
		commitIn(t, dir, "a.txt", "a")
		git(t, dir, "checkout", "-q", "--orphan", "other")
		commitIn(t, dir, "b.txt", "b")
		assert.Equal(t, EmptyTree, DefaultBase(dir, "HEAD"))
	})
	t.Run("not a repository: the empty tree, never an error", func(t *testing.T) {
		assert.Equal(t, EmptyTree, DefaultBase(t.TempDir(), "HEAD"))
	})
}

func TestResolveRange_TheEmptyTreeIsTheBaseOfARangeBeforeTheFirstCommit(t *testing.T) {
	dir := initRepo(t)
	head := commitIn(t, dir, "a.txt", "a")

	r, err := ResolveRange(dir, EmptyTree, "HEAD")
	require.NoError(t, err)
	assert.Equal(t, Range{Base: EmptyTree, Head: head}, r)
}
