package gitrepo

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// defaultRef points origin/HEAD at origin/main, both at the given commit.
func defaultRef(t *testing.T, dir, sha string) {
	t.Helper()
	git(t, dir, "update-ref", "refs/remotes/origin/main", sha)
	git(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
}

func TestExcludeUpstream_RebasedOntoNewerMain(t *testing.T) {
	dir := initRepo(t)
	start := commit(t, dir, "a", "a")
	git(t, dir, "switch", "-q", "-c", "feat")
	own := commit(t, dir, "own", "own")
	// Upstream's commit arrives by fetch, never through this folder's HEAD reflog.
	up := git(t, dir, "commit-tree", start+"^{tree}", "-p", start, "-m", "upstream")
	defaultRef(t, dir, up)
	git(t, dir, "rebase", "-q", up)
	head := git(t, dir, "rev-parse", "HEAD")
	require.NotEqual(t, own, head)

	r, err := ExcludeUpstream(dir, Range{Base: start, Head: head, Origin: FromSessionStart})
	require.NoError(t, err)
	assert.Equal(t, up, r.Base, "the pulled commit is not the session's: the base moves past it")
}

func TestExcludeUpstream_ASessionCommitThatLandedIsKept(t *testing.T) {
	dir := initRepo(t)
	start := commit(t, dir, "a", "a")
	own := commit(t, dir, "own", "own")
	defaultRef(t, dir, own) // pushed by fast-forward
	r, err := ExcludeUpstream(dir, Range{Base: start, Head: own, Origin: FromSessionStart})
	require.NoError(t, err)
	assert.Equal(t, start, r.Base)
}

func TestExcludeUpstream_AFeatureBranchRemoteDoesNotHideWork(t *testing.T) {
	dir := initRepo(t)
	start := commit(t, dir, "a", "a")
	defaultRef(t, dir, start)
	own := commit(t, dir, "own", "own")
	git(t, dir, "update-ref", "refs/remotes/origin/feat", own)
	r, err := ExcludeUpstream(dir, Range{Base: start, Head: own, Origin: FromSessionStart})
	require.NoError(t, err)
	assert.Equal(t, start, r.Base)
}

func TestExcludeUpstream_NoDefaultRefLeavesTheRange(t *testing.T) {
	dir := initRepo(t)
	start := commit(t, dir, "a", "a")
	own := commit(t, dir, "own", "own")
	r, err := ExcludeUpstream(dir, Range{Base: start, Head: own})
	require.NoError(t, err)
	assert.Equal(t, start, r.Base)
}
