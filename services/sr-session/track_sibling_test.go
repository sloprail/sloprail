package main

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// siblings registers n sub-agent worktrees of proj, each on its own branch, and returns their paths.
func siblings(t *testing.T, proj string, reg sessionstate.Store, rs rootSession, n int) []string {
	t.Helper()
	base := runGit(t, proj, "rev-parse", "HEAD")
	dirs := make([]string, n)
	for i := range dirs {
		dirs[i] = filepath.Join(t.TempDir(), fmt.Sprintf("sib%d", i))
		runGit(t, proj, "worktree", "add", "-q", "-b", fmt.Sprintf("worktree-agent-%d", i), dirs[i])
		_, err := reg.RegisterFolder(sessionstate.Folder{SessionID: rs.ID, Path: dirs[i], Role: sessionstate.FolderSubagentWorktree, GitRoot: dirs[i], BaseRef: base, AgentID: fmt.Sprintf("agent-%d", i)})
		require.NoError(t, err)
	}
	return dirs
}

func dumpRows(t *testing.T, reg sessionstate.Store, id string) {
	rows, _ := reg.Ranges(id)
	for _, r := range rows {
		t.Logf("ROW %s %s tracked=%v %s", r.Folder, r.Head, r.Tracked(), r.UntrackedReason)
	}
}

func trackedIn_(t *testing.T, reg sessionstate.Store, id, folder, head string) bool {
	t.Helper()
	rows, err := reg.Ranges(id)
	require.NoError(t, err)
	for _, r := range rows {
		if r.Tracked() && r.Head == head && realPath(r.Folder) == realPath(folder) {
			return true
		}
	}
	return false
}

// A branch the root moved and left (checked out nowhere) while N sibling worktrees can see it is
// answered for by the root's folder alone; and, once the root untracks it, a sibling never picks it
// up again. A branch a sibling made and left is answered for by the root, not by every sibling.
func TestTrackMissing_ABranchCheckedOutNowhereHasOneHomeFolder(t *testing.T) {
	const n = 5
	proj, reg, rs := ruledAndObserved(t, nil)
	dirs := siblings(t, proj, reg, rs, n)
	require.NoError(t, trackMissing(reg, rs, HookPayload{}))

	runGit(t, proj, "switch", "-c", "root-left")
	commitFile(t, proj, "r.md", "r")
	runGit(t, proj, "switch", "main")
	runGit(t, dirs[0], "switch", "-c", "sib-left")
	commitFile(t, dirs[0], "s.md", "s")
	runGit(t, dirs[0], "switch", "worktree-agent-0")
	require.NoError(t, trackMissing(reg, rs, HookPayload{}))
	require.NoError(t, trackMissing(reg, rs, HookPayload{}))

	assert.True(t, trackedIn_(t, reg, rs.ID, proj, "root-left"), "the root's own branch is tracked in the root's folder")
	assert.True(t, trackedIn_(t, reg, rs.ID, proj, "sib-left") || trackedIn_(t, reg, rs.ID, dirs[0], "sib-left"),
		"a branch the session worked on is tracked in some folder")
	for i, d := range dirs {
		assert.False(t, trackedIn_(t, reg, rs.ID, d, "root-left"), "sibling %d answers for the root's branch", i)
		if i > 0 {
			assert.False(t, trackedIn_(t, reg, rs.ID, d, "sib-left"), "sibling %d answers for another sibling's branch", i)
		}
	}

	// An untrack sticks: no sibling picks the branch up, and the root tracks it again only when it moves.
	require.NoError(t, reg.UntrackRange(rs.ID, proj, "root-left", "dead", "", runGit(t, proj, "rev-parse", "root-left")))
	require.NoError(t, trackMissing(reg, rs, HookPayload{}))
	assert.False(t, trackedIn_(t, reg, rs.ID, proj, "root-left"), "an unmoved untracked branch came back")
	runGit(t, proj, "switch", "root-left")
	commitFile(t, proj, "r2.md", "r2")
	runGit(t, proj, "switch", "main")
	require.NoError(t, trackMissing(reg, rs, HookPayload{}))
	assert.True(t, trackedIn_(t, reg, rs.ID, proj, "root-left"), "a moved branch is tracked again by its home")
	for i, d := range dirs {
		assert.False(t, trackedIn_(t, reg, rs.ID, d, "root-left"), "sibling %d picked the moved branch up", i)
	}
}

// A branch moved between worktrees: the folder that has it checked out now is its home.
func TestTrackMissing_ABranchMovedBetweenWorktreesIsAnsweredForWhereItIsCheckedOut(t *testing.T) {
	proj, reg, rs := ruledAndObserved(t, nil)
	dirs := siblings(t, proj, reg, rs, 2)
	require.NoError(t, trackMissing(reg, rs, HookPayload{}))
	runGit(t, dirs[0], "switch", "-c", "hop")
	commitFile(t, dirs[0], "h.md", "h")
	require.NoError(t, trackMissing(reg, rs, HookPayload{}))
	runGit(t, dirs[0], "switch", "worktree-agent-0")
	runGit(t, dirs[1], "switch", "hop")
	commitFile(t, dirs[1], "h2.md", "h2")
	require.NoError(t, trackMissing(reg, rs, HookPayload{}))
	assert.True(t, trackedIn_(t, reg, rs.ID, dirs[1], "hop") || trackedIn_(t, reg, rs.ID, proj, "hop"), "the branch is tracked in no folder")
	assert.True(t, trackedIn_(t, reg, rs.ID, dirs[0], "hop") || trackedIn_(t, reg, rs.ID, dirs[1], "hop") || trackedIn_(t, reg, rs.ID, proj, "hop"))
}

// Rows an older engine attached to every worktree: the home's row stays, a sibling's row that the
// home's covers is pruned once. Kept: the root's rows, explicit rows, a row whose home holds no
// covering tracked row, a row whose tip the home's does not hold.
func TestPruneSiblingAuto(t *testing.T) {
	proj, reg, rs := ruledAndObserved(t, nil)
	dirs := siblings(t, proj, reg, rs, 3)
	base := runGit(t, proj, "rev-parse", "HEAD")
	runGit(t, proj, "switch", "-c", "left")
	commitFile(t, proj, "l.md", "l")
	tip := runGit(t, proj, "rev-parse", "HEAD")
	runGit(t, proj, "switch", "-c", "other")
	commitFile(t, proj, "o.md", "o")
	otherTip := runGit(t, proj, "rev-parse", "HEAD")
	runGit(t, proj, "switch", "main")
	row := func(folder, head, sha, by string) sessionstate.TrackedRange {
		return sessionstate.TrackedRange{SessionID: rs.ID, Folder: resolved(t, folder), Head: head, HeadSHA: sha, Base: base, AddedBy: by}
	}
	for _, r := range []sessionstate.TrackedRange{
		row(proj, "left", tip, sessionstate.RangeAuto),
		row(dirs[0], "left", tip, sessionstate.RangeAuto),
		row(dirs[1], "left", tip, sessionstate.RangeAuto),
		row(dirs[2], "left", tip, sessionstate.RangeAgent),
		// "other": the home (root) holds no row; a sibling's row is not covered and stays.
		row(dirs[0], "other", otherTip, sessionstate.RangeAuto),
		// "left" at a tip the home's row does not hold: kept.
		row(dirs[2], "left2", otherTip, sessionstate.RangeAuto),
		row(proj, "left2", tip, sessionstate.RangeAuto),
	} {
		require.NoError(t, reg.TrackRange(r))
	}
	require.NoError(t, pruneSiblingAuto(reg, rs.ID))
	require.NoError(t, pruneSiblingAuto(reg, rs.ID)) // idempotent
	assert.True(t, trackedIn_(t, reg, rs.ID, proj, "left"), "the home's row was pruned")
	assert.False(t, trackedIn_(t, reg, rs.ID, dirs[0], "left"), "a covered sibling row was kept")
	assert.False(t, trackedIn_(t, reg, rs.ID, dirs[1], "left"), "a covered sibling row was kept")
	assert.True(t, trackedIn_(t, reg, rs.ID, dirs[2], "left"), "an explicit row was pruned")
	assert.True(t, trackedIn_(t, reg, rs.ID, dirs[0], "other"), "a row nothing else holds was pruned")
	assert.True(t, trackedIn_(t, reg, rs.ID, dirs[2], "left2"), "a row the home's tip does not hold was pruned")
	assert.True(t, trackedIn_(t, reg, rs.ID, proj, "left2"), "the root's row was pruned")
}
