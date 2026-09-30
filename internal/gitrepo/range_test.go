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

const ruleDir = ".sloprail/file-guard/r"

// The floor is the PARENT of the last commit touching the rule's folder, so the
// commit that adds or changes a rule is judged by the rule itself.
func TestResolveRange_FloorIsTheParentOfTheLastCommitTouchingTheRuleFolder(t *testing.T) {
	dir := initRepo(t)
	before := commit(t, dir, "old.go", "x")
	commitIn(t, dir, ruleDir+"/file-guard.yaml", "match: '*.go'")
	head := commit(t, dir, "new.go", "y")

	r, err := ResolveRange(dir, ruleDir, "", "")
	require.NoError(t, err)
	assert.Equal(t, before, r.Base)
	assert.Equal(t, head, r.Head)
	assert.Equal(t, FromFloor, r.Origin)
	assert.False(t, r.Empty())
}

func TestResolveRange_TheFloorMovesWhenTheRuleIsEdited(t *testing.T) {
	// An edited rule applies from the commit that edits it, inclusive.
	dir := initRepo(t)
	commitIn(t, dir, ruleDir+"/file-guard.yaml", "v1")
	beforeEdit := commit(t, dir, "a.go", "x")
	commitIn(t, dir, ruleDir+"/check.sh", "v2")

	r, err := ResolveRange(dir, ruleDir, "", "")
	require.NoError(t, err)
	assert.Equal(t, beforeEdit, r.Base)
	assert.False(t, r.Empty(), "the commit that edited the rule is itself in the range")
}

// A root commit has no parent: its base is the empty tree, so the whole of it is
// judged — not nothing.
func TestResolveRange_ARuleAddedInTheRootCommitIsJudgedFromTheEmptyTree(t *testing.T) {
	dir := initRepo(t)
	root := commitIn(t, dir, ruleDir+"/file-guard.yaml", "v1")

	r, err := ResolveRange(dir, ruleDir, "", "")
	require.NoError(t, err)
	assert.Equal(t, EmptyTree, r.Base)
	assert.Equal(t, root, r.Head)
	assert.False(t, r.Empty())
	assert.Equal(t, FromFloor, r.Origin)

	cs, err := CommitsIn(dir, r.Base, r.Head)
	require.NoError(t, err)
	require.Len(t, cs, 1, "every commit head reaches, not a range that cannot be spelled")
	assert.Equal(t, root, cs[0].SHA)

	ds, err := Deltas(dir, r.Base, r.Head)
	require.NoError(t, err)
	require.Len(t, ds, 1)
	assert.Equal(t, byte('A'), ds[0].Status)
	got, err := BlobAt(dir, r.Base, "nothing")
	assert.Error(t, err)
	_ = got
}

func TestResolveRange_ReachableWatermarkWinsOverTheFloor(t *testing.T) {
	dir := initRepo(t)
	commitIn(t, dir, ruleDir+"/file-guard.yaml", "v1")
	passed := commit(t, dir, "a.go", "x")
	head := commit(t, dir, "b.go", "y")

	r, err := ResolveRange(dir, ruleDir, passed, "")
	require.NoError(t, err)
	assert.Equal(t, passed, r.Base)
	assert.Equal(t, head, r.Head)
	assert.Equal(t, FromWatermark, r.Origin)
	assert.Empty(t, r.DroppedWatermark)
}

func TestResolveRange_WatermarkAtHeadIsAnEmptyRange(t *testing.T) {
	dir := initRepo(t)
	commitIn(t, dir, ruleDir+"/file-guard.yaml", "v1")
	head := commit(t, dir, "a.go", "x")

	r, err := ResolveRange(dir, ruleDir, head, "")
	require.NoError(t, err)
	assert.True(t, r.Empty())
}

// a10n #1: the watermark was a name or per attempt and never checked for
// reachability, so an amend or a rebase silently shrank the diff.
func TestResolveRange_AmendedAwayWatermarkFallsBackToTheFloor(t *testing.T) {
	dir := initRepo(t)
	commitIn(t, dir, ruleDir+"/file-guard.yaml", "v1")
	commit(t, dir, "a.go", "x")
	passed := git(t, dir, "rev-parse", "HEAD")
	git(t, dir, "commit", "--amend", "-m", "amended")

	r, err := ResolveRange(dir, ruleDir, passed, "")
	require.NoError(t, err)
	assert.Equal(t, EmptyTree, r.Base, "the amended-away head is no longer an ancestor, so the floor is used (here the rule was the root commit)")
	assert.Equal(t, FromFloor, r.Origin)
	assert.Equal(t, passed, r.DroppedWatermark)
}

func TestResolveRange_RebasedAwayWatermarkFallsBackToTheFloor(t *testing.T) {
	dir := initRepo(t)
	commit(t, dir, "base.txt", "0")
	git(t, dir, "checkout", "-b", "feature")
	floor := commitIn(t, dir, ruleDir+"/file-guard.yaml", "v1")
	passed := commit(t, dir, "a.go", "x")
	git(t, dir, "checkout", "main")
	commit(t, dir, "main.txt", "m")
	git(t, dir, "checkout", "feature")
	git(t, dir, "rebase", "main")
	require.NotEqual(t, passed, git(t, dir, "rev-parse", "HEAD"))

	r, err := ResolveRange(dir, ruleDir, passed, "")
	require.NoError(t, err)
	assert.NotEqual(t, passed, r.Base)
	assert.NotEqual(t, floor, r.Base)
	assert.Equal(t, git(t, dir, "rev-parse", git(t, dir, "log", "-1", "--format=%H", "--", ruleDir)+"^"), r.Base,
		"the floor is the parent of the REBASED rule commit")
}

func TestResolveRange_GarbageCollectedWatermarkIsUnreachableNotAnError(t *testing.T) {
	dir := initRepo(t)
	commitIn(t, dir, ruleDir+"/file-guard.yaml", "v1")
	commit(t, dir, "a.go", "x")

	r, err := ResolveRange(dir, ruleDir, "0123456789012345678901234567890123456789", "")
	require.NoError(t, err)
	assert.Equal(t, FromFloor, r.Origin)
}

func TestResolveRange_NoCommitsIsItsOwnOutcome(t *testing.T) {
	dir := initRepo(t)
	_, err := ResolveRange(dir, ruleDir, "", "")
	assert.ErrorIs(t, err, ErrNoCommits)
}

// a10n #2: a git error was read as "no change". Here it is an error and no Range.
func TestResolveRange_AGitErrorFailsClosed(t *testing.T) {
	dir := initRepo(t)
	commitIn(t, dir, ruleDir+"/file-guard.yaml", "v1")
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("garbage\n"), 0o644))

	r, err := ResolveRange(dir, ruleDir, "", "")
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNoCommits)
	assert.NotErrorIs(t, err, ErrNoSessionStart)
	assert.Equal(t, Range{}, r)
}

func TestResolveRange_ANonRepositoryIsAnError(t *testing.T) {
	_, err := ResolveRange(t.TempDir(), ruleDir, "", "")
	assert.ErrorIs(t, err, ErrNotARepository)
}

func TestResolveRange_UncommittedRuleFallsToTheSessionStart(t *testing.T) {
	dir := initRepo(t)
	start := commit(t, dir, "a.go", "x")
	head := commit(t, dir, "b.go", "y")

	r, err := ResolveRange(dir, ruleDir, "", start)
	require.NoError(t, err)
	assert.Equal(t, start, r.Base)
	assert.Equal(t, head, r.Head)
	assert.Equal(t, FromSessionStart, r.Origin)
}

func TestResolveRange_ARuleOutsideTheRepoHasNoFolderAndUsesTheSessionStart(t *testing.T) {
	// A plugin's rule lives in the plugin cache: its folder is "".
	dir := initRepo(t)
	commitIn(t, dir, ruleDir+"/file-guard.yaml", "unrelated")
	start := commit(t, dir, "a.go", "x")

	r, err := ResolveRange(dir, "", "", start)
	require.NoError(t, err)
	assert.Equal(t, start, r.Base)
	assert.Equal(t, FromSessionStart, r.Origin)
}

func TestResolveRange_TheFolderFloorOutranksTheSessionStart(t *testing.T) {
	dir := initRepo(t)
	start := commit(t, dir, "a.go", "x")
	mid := commit(t, dir, "mid.go", "m")
	commitIn(t, dir, ruleDir+"/file-guard.yaml", "v1")
	commit(t, dir, "b.go", "y")

	r, err := ResolveRange(dir, ruleDir, "", start)
	require.NoError(t, err)
	assert.Equal(t, mid, r.Base, "the parent of the rule's commit, not the session's start")
	assert.Equal(t, FromFloor, r.Origin)
}

func TestResolveRange_TheWatermarkOutranksBoth(t *testing.T) {
	dir := initRepo(t)
	start := commit(t, dir, "a.go", "x")
	commitIn(t, dir, ruleDir+"/file-guard.yaml", "v1")
	passed := commit(t, dir, "b.go", "y")
	commit(t, dir, "c.go", "z")

	r, err := ResolveRange(dir, ruleDir, passed, start)
	require.NoError(t, err)
	assert.Equal(t, passed, r.Base)
}

func TestResolveRange_AnAmendedAwayWatermarkFallsThroughToTheSessionStartWhenThereIsNoFloor(t *testing.T) {
	dir := initRepo(t)
	start := commit(t, dir, "a.go", "x")
	passed := commit(t, dir, "b.go", "y")
	git(t, dir, "commit", "--amend", "-m", "amended")

	r, err := ResolveRange(dir, "", passed, start)
	require.NoError(t, err)
	assert.Equal(t, start, r.Base)
	assert.Equal(t, passed, r.DroppedWatermark)
}

func TestResolveRange_NoFloorAndNoSessionStartFailsClosed(t *testing.T) {
	dir := initRepo(t)
	commit(t, dir, "a.go", "x")
	r, err := ResolveRange(dir, ruleDir, "", "")
	assert.ErrorIs(t, err, ErrNoSessionStart)
	assert.Equal(t, Range{}, r)
}

func TestResolveRange_AnUnreachableSessionStartFailsClosed(t *testing.T) {
	dir := initRepo(t)
	commit(t, dir, "a.go", "x")
	start := commit(t, dir, "b.go", "y")
	git(t, dir, "commit", "--amend", "-m", "amended")

	r, err := ResolveRange(dir, ruleDir, "", start)
	assert.ErrorIs(t, err, ErrSessionStartUnreachable)
	assert.Equal(t, Range{}, r)

	_, err = ResolveRange(dir, ruleDir, "", "0123456789012345678901234567890123456789")
	assert.ErrorIs(t, err, ErrSessionStartUnreachable, "a commit git has never heard of is unreachable too")
}

func TestRootCommit_IsTheFirstCommitWhateverTheBranch(t *testing.T) {
	dir := initRepo(t)
	root := commit(t, dir, "a.go", "x")
	commit(t, dir, "b.go", "y")
	git(t, dir, "checkout", "-qb", "feature")
	commit(t, dir, "c.go", "z")

	got, err := RootCommit(dir)
	require.NoError(t, err)
	assert.Equal(t, root, got)
}

func TestRootCommit_NoCommitsAndNotARepository(t *testing.T) {
	_, err := RootCommit(initRepo(t))
	assert.Error(t, err)
	_, err = RootCommit(t.TempDir())
	assert.Error(t, err)
}
