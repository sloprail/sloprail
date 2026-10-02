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
