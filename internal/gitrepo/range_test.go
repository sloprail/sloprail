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

	r, err := ResolveRange(dir, ruleDir, ruleDir, "", "")
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

	r, err := ResolveRange(dir, ruleDir, ruleDir, "", "")
	require.NoError(t, err)
	assert.Equal(t, beforeEdit, r.Base)
	assert.False(t, r.Empty(), "the commit that edited the rule is itself in the range")
}

// A root commit has no parent: its base is the empty tree, so the whole of it is
// judged — not nothing.
func TestResolveRange_ARuleAddedInTheRootCommitIsJudgedFromTheEmptyTree(t *testing.T) {
	dir := initRepo(t)
	root := commitIn(t, dir, ruleDir+"/file-guard.yaml", "v1")

	r, err := ResolveRange(dir, ruleDir, ruleDir, "", "")
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

	r, err := ResolveRange(dir, ruleDir, ruleDir, passed, "")
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

	r, err := ResolveRange(dir, ruleDir, ruleDir, head, "")
	require.NoError(t, err)
	assert.True(t, r.Empty())
}

// a10n #1: the watermark was a name or per attempt and never checked for
// reachability, so an amend or a rebase silently shrank the diff. An unreachable
// watermark is re-anchored at its merge base with HEAD: what survives stays approved,
// what was rewritten is judged again.
func TestResolveRange_AmendedAwayWatermarkIsReanchoredAtItsMergeBase(t *testing.T) {
	dir := initRepo(t)
	commitIn(t, dir, ruleDir+"/file-guard.yaml", "v1")
	before := commit(t, dir, "a.go", "x")
	passed := commit(t, dir, "b.go", "y")
	git(t, dir, "commit", "--amend", "-m", "amended")

	r, err := ResolveRange(dir, ruleDir, ruleDir, passed, "")
	require.NoError(t, err)
	assert.Equal(t, before, r.Base, "the amended-away head's merge base with HEAD: the amended commit is inside the range")
	assert.Equal(t, FromWatermark, r.Origin)
	assert.Equal(t, passed, r.DroppedWatermark)
}

func TestResolveRange_ASoftResetWatermarkIsReanchoredAtItsMergeBase(t *testing.T) {
	dir := initRepo(t)
	commitIn(t, dir, ruleDir+"/file-guard.yaml", "v1")
	before := commit(t, dir, "a.go", "x")
	commit(t, dir, "b.go", "y")
	passed := commit(t, dir, "c.go", "z")
	git(t, dir, "reset", "--soft", before)
	git(t, dir, "commit", "-m", "squashed b and c")

	r, err := ResolveRange(dir, ruleDir, ruleDir, passed, "")
	require.NoError(t, err)
	assert.Equal(t, before, r.Base)
	assert.Equal(t, passed, r.DroppedWatermark)
}

func TestResolveRange_RebasedAwayWatermarkIsReanchoredAtItsMergeBase(t *testing.T) {
	dir := initRepo(t)
	fork := commitIn(t, dir, ".sloprail/lib/seed.sh", "s") // the rule root exists at session start
	git(t, dir, "checkout", "-b", "feature")
	commitIn(t, dir, ruleDir+"/file-guard.yaml", "v1")
	passed := commit(t, dir, "a.go", "x")
	git(t, dir, "checkout", "main")
	commit(t, dir, "main.txt", "m")
	git(t, dir, "checkout", "feature")
	git(t, dir, "rebase", "main")
	require.NotEqual(t, passed, git(t, dir, "rev-parse", "HEAD"))

	r, err := ResolveRange(dir, ruleDir, ruleDir, passed, "")
	require.NoError(t, err)
	assert.Equal(t, fork, r.Base, "the point the old line and the rebased one still share: main's new commit and the rebased commits are inside the range")
	assert.Equal(t, FromWatermark, r.Origin)
}

func TestResolveRange_GarbageCollectedWatermarkIsUnreachableNotAnError(t *testing.T) {
	dir := initRepo(t)
	commitIn(t, dir, ruleDir+"/file-guard.yaml", "v1")
	commit(t, dir, "a.go", "x")

	r, err := ResolveRange(dir, ruleDir, ruleDir, "0123456789012345678901234567890123456789", "")
	require.NoError(t, err)
	assert.Equal(t, FromFloor, r.Origin)
}

func TestResolveRange_NoCommitsIsItsOwnOutcome(t *testing.T) {
	dir := initRepo(t)
	_, err := ResolveRange(dir, ruleDir, ruleDir, "", "")
	assert.ErrorIs(t, err, ErrNoCommits)
}

// a10n #2: a git error was read as "no change". Here it is an error and no Range.
func TestResolveRange_AGitErrorFailsClosed(t *testing.T) {
	dir := initRepo(t)
	commitIn(t, dir, ruleDir+"/file-guard.yaml", "v1")
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("garbage\n"), 0o644))

	r, err := ResolveRange(dir, ruleDir, ruleDir, "", "")
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNoCommits)
	assert.NotErrorIs(t, err, ErrNoSessionStart)
	assert.Equal(t, Range{}, r)
}

func TestResolveRange_ANonRepositoryIsAnError(t *testing.T) {
	_, err := ResolveRange(t.TempDir(), ruleDir, ruleDir, "", "")
	assert.ErrorIs(t, err, ErrNotARepository)
}

func TestResolveRange_UncommittedRuleFallsToTheSessionStart(t *testing.T) {
	dir := initRepo(t)
	start := commit(t, dir, "a.go", "x")
	head := commit(t, dir, "b.go", "y")

	r, err := ResolveRange(dir, ruleDir, ruleDir, "", start)
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

	r, err := ResolveRange(dir, "", "", "", start)
	require.NoError(t, err)
	assert.Equal(t, start, r.Base)
	assert.Equal(t, FromSessionStart, r.Origin)
}

// Without a watermark, a rule that existed at session start judges from the EARLIER
// of its floor and the session start; one that did not exist then judges from its
// floor alone (history before the commit that added it is grandfathered).
func TestResolveRange_ARuleAddedMidSessionUsesItsFloorOnly(t *testing.T) {
	dir := initRepo(t)
	start := commit(t, dir, "a.go", "x")
	commit(t, dir, "bad1.go", "m")
	commit(t, dir, "bad2.go", "m")
	before := commit(t, dir, "bad3.go", "m")
	commitIn(t, dir, ruleDir+"/file-guard.yaml", "v1")
	head := commit(t, dir, "b.go", "y")

	r, err := ResolveRange(dir, ruleDir, ruleDir, "", start)
	require.NoError(t, err)
	assert.Equal(t, before, r.Base, "the parent of the add commit: earlier violations are grandfathered")
	assert.Equal(t, FromFloor, r.Origin)
	assert.Equal(t, head, r.Head)

	// Edited later in the session: the floor is the later commit's parent.
	commitIn(t, dir, ruleDir+"/file-guard.yaml", "v2")
	commit(t, dir, "c.go", "z")
	r, err = ResolveRange(dir, ruleDir, ruleDir, "", start)
	require.NoError(t, err)
	assert.Equal(t, head, r.Base)
}

func TestResolveRange_ARuleThatExistedAtSessionStartKeepsTheEarlierOfFloorAndStart(t *testing.T) {
	dir := initRepo(t)
	commitIn(t, dir, ruleDir+"/file-guard.yaml", "v1")
	start := commit(t, dir, "a.go", "x")
	commit(t, dir, "violation.go", "bad")
	commitIn(t, dir, ruleDir+"/file-guard.yaml", "v2")

	r, err := ResolveRange(dir, ruleDir, ruleDir, "", start)
	require.NoError(t, err)
	assert.Equal(t, start, r.Base, "touching the rule must not skip the violation")
	assert.Equal(t, FromSessionStart, r.Origin)

	// A session that began after the rule's last edit: the floor is earlier.
	late := commit(t, dir, "c.go", "z")
	r, err = ResolveRange(dir, ruleDir, ruleDir, "", late)
	require.NoError(t, err)
	assert.Equal(t, FromFloor, r.Origin)
}

func TestResolveRange_ARuleDeletedAndReAddedInTheSessionStaysStrict(t *testing.T) {
	dir := initRepo(t)
	commitIn(t, dir, ruleDir+"/file-guard.yaml", "v1")
	start := commit(t, dir, "a.go", "x")
	commit(t, dir, "violation.go", "bad")
	git(t, dir, "rm", "-rq", ruleDir)
	git(t, dir, "commit", "-m", "delete the rule")
	commitIn(t, dir, ruleDir+"/file-guard.yaml", "v1")

	r, err := ResolveRange(dir, ruleDir, ruleDir, "", start)
	require.NoError(t, err)
	assert.Equal(t, start, r.Base)
	assert.Equal(t, FromSessionStart, r.Origin)
}

func TestResolveRange_AnUnbornSessionStartFailsClosedToTheStrictRange(t *testing.T) {
	dir := initRepo(t)
	commit(t, dir, "a.go", "x")
	commitIn(t, dir, ruleDir+"/file-guard.yaml", "v1")
	r, err := ResolveRange(dir, ruleDir, ruleDir, "", EmptyTree)
	require.NoError(t, err)
	assert.Equal(t, EmptyTree, r.Base)
}

func TestResolveRange_ARuleEditedAfterTheSessionBeganDoesNotSkipTheWorkBeforeTheEdit(t *testing.T) {
	dir := initRepo(t)
	commitIn(t, dir, ruleDir+"/file-guard.yaml", "v1")
	start := commit(t, dir, "a.go", "x")
	commit(t, dir, "violation.go", "bad")
	commitIn(t, dir, ".sloprail/lib/shared.sh", "edit")

	r, err := ResolveRange(dir, ".sloprail", ".sloprail", "", start)
	require.NoError(t, err)
	assert.Equal(t, start, r.Base, "the violation sits inside the range")
}

// An unreachable session start is re-anchored at its merge base with HEAD, never
// replaced by the floor: a later commit touching the rule's folder puts the floor AFTER
// in-session commits, which would then never be judged. Each way of rewriting it: an
// amend, a soft reset that recommits, and a rebase onto new upstream work. The
// violation sits between the rewrite and the commit that moves the floor.
func TestResolveRange_ARewrittenSessionStartIsReanchoredAtItsMergeBase(t *testing.T) {
	violationInRange := func(t *testing.T, dir, start, want string) {
		t.Helper()
		commit(t, dir, "violation.go", "bad")
		commitIn(t, dir, ".sloprail/lib/shared.sh", "touched") // the floor moves past the violation
		floorBase := git(t, dir, "rev-parse", "HEAD~1")

		r, err := ResolveRange(dir, ".sloprail", ".sloprail", "", start)
		require.NoError(t, err)
		require.NotEqual(t, floorBase, want, "premise: the floor is after the violation")
		assert.Equal(t, want, r.Base, "the base is before the rewritten session's work, so the violation is judged")
		assert.Equal(t, FromSessionStart, r.Origin)
	}

	t.Run("amend", func(t *testing.T) {
		dir := initRepo(t)
		before := commitIn(t, dir, ".sloprail/lib/seed.sh", "s") // the rule root exists at session start
		start := commit(t, dir, "b.go", "y")
		git(t, dir, "commit", "--amend", "-m", "amended")
		violationInRange(t, dir, start, before)
	})
	t.Run("soft reset", func(t *testing.T) {
		dir := initRepo(t)
		before := commitIn(t, dir, ".sloprail/lib/seed.sh", "s") // the rule root exists at session start
		commit(t, dir, "b.go", "y")
		start := commit(t, dir, "c.go", "z")
		git(t, dir, "reset", "--soft", before)
		git(t, dir, "commit", "-m", "squashed")
		violationInRange(t, dir, start, before)
	})
	t.Run("rebase", func(t *testing.T) {
		dir := initRepo(t)
		fork := commitIn(t, dir, ".sloprail/lib/seed.sh", "s") // the rule root exists at session start
		git(t, dir, "checkout", "-b", "feature")
		start := commit(t, dir, "b.go", "y")
		git(t, dir, "checkout", "main")
		commit(t, dir, "main.txt", "m")
		git(t, dir, "checkout", "feature")
		git(t, dir, "rebase", "main")
		violationInRange(t, dir, start, fork)
	})
}

// A session start git no longer has (gc'd), or that shares no history with HEAD (the root
// commit was amended, so it is a different root), is anchored at the EMPTY TREE: the whole
// history is judged, the root commit's own content included, never guessed away.
func TestResolveRange_ASessionStartGitHasNoMergeBaseForFallsToTheEmptyTree(t *testing.T) {
	dir := initRepo(t)
	commit(t, dir, "a.go", "x")
	commit(t, dir, "b.go", "y")

	r, err := ResolveRange(dir, ".sloprail", ".sloprail", "", "0123456789012345678901234567890123456789")
	require.NoError(t, err)
	assert.Equal(t, EmptyTree, r.Base)
	assert.Equal(t, FromSessionStart, r.Origin)

	// Unrelated history: a second root, with no merge base with HEAD.
	git(t, dir, "checkout", "-q", "--orphan", "other")
	git(t, dir, "rm", "-rfq", ".")
	other := commit(t, dir, "o.txt", "o")
	git(t, dir, "checkout", "-q", "main")
	r, err = ResolveRange(dir, ".sloprail", ".sloprail", "", other)
	require.NoError(t, err)
	assert.Equal(t, EmptyTree, r.Base)
}

// The root commit itself amended to carry a violation: the session start (the old root)
// is unreachable and shares no history with the new one, so the range starts at the empty
// tree and the amended root's content is inside it.
func TestResolveRange_AnAmendedRootCommitIsJudgedFromTheEmptyTree(t *testing.T) {
	dir := initRepo(t)
	start := commit(t, dir, "a.go", "x")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "violation.go"), []byte("bad"), 0o644))
	git(t, dir, "add", "violation.go")
	git(t, dir, "commit", "--amend", "--no-edit")

	r, err := ResolveRange(dir, "", "", "", start)
	require.NoError(t, err)
	assert.Equal(t, EmptyTree, r.Base)
	assert.False(t, r.Empty())
}

func TestResolveRange_TheWatermarkOutranksBoth(t *testing.T) {
	dir := initRepo(t)
	start := commit(t, dir, "a.go", "x")
	commitIn(t, dir, ruleDir+"/file-guard.yaml", "v1")
	passed := commit(t, dir, "b.go", "y")
	commit(t, dir, "c.go", "z")

	r, err := ResolveRange(dir, ruleDir, ruleDir, passed, start)
	require.NoError(t, err)
	assert.Equal(t, passed, r.Base)
}

func TestResolveRange_AnAmendedAwayWatermarkFallsThroughToTheSessionStartWhenThereIsNoFloor(t *testing.T) {
	dir := initRepo(t)
	start := commit(t, dir, "a.go", "x")
	passed := commit(t, dir, "b.go", "y")
	git(t, dir, "commit", "--amend", "-m", "amended")

	r, err := ResolveRange(dir, "", "", passed, start)
	require.NoError(t, err)
	assert.Equal(t, start, r.Base)
	assert.Equal(t, passed, r.DroppedWatermark)
}

func TestResolveRange_NoFloorAndNoSessionStartFailsClosed(t *testing.T) {
	dir := initRepo(t)
	commit(t, dir, "a.go", "x")
	r, err := ResolveRange(dir, ruleDir, ruleDir, "", "")
	assert.ErrorIs(t, err, ErrNoSessionStart)
	assert.Equal(t, Range{}, r)
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

// "New" is the rule's OWN folder being absent at session start, not the shared
// .sloprail root: another rule or a lib already there does not make this rule old.
func TestResolveRange_ARuleAddedBesideExistingSloprailFilesIsStillNew(t *testing.T) {
	dir := initRepo(t)
	commitIn(t, dir, ".sloprail/lib/seed.sh", "s")
	start := commit(t, dir, "a.go", "x")
	commit(t, dir, "early-violation.go", "bad")
	before := commit(t, dir, "b.go", "y")
	commitIn(t, dir, ruleDir+"/file-guard.yaml", "v1")

	r, err := ResolveRange(dir, ".sloprail", ruleDir, "", start)
	require.NoError(t, err)
	assert.Equal(t, before, r.Base)
	assert.Equal(t, FromFloor, r.Origin)
}
