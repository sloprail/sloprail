package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/sessionstate"
)

// A tracked sub-agent folder that no longer exists gets no Stop of its own: the sub-agent's Stop
// skips the range (it has no folder to fix it in) and the root's Stop judges it. A range under a
// folder that exists stays the sub-agent's.
func TestJudgedAtStopOf_AGoneSubagentFolderIsTheParents(t *testing.T) {
	live := t.TempDir()
	gone := filepath.Join(t.TempDir(), "removed")
	inLive := sessionstate.TrackedRange{Folder: live, Head: "a", AgentID: "sub"}
	inGone := sessionstate.TrackedRange{Folder: gone, Head: "b", AgentID: "sub"}
	other := sessionstate.TrackedRange{Folder: live, Head: "c", AgentID: "other"}
	subStop, rootStop := HookPayload{AgentID: "sub"}, HookPayload{}

	assert.True(t, judgedAtStopOf(subStop, inLive), "its own range in a folder that exists")
	assert.False(t, judgedAtStopOf(subStop, inGone), "its own range in a folder that is gone goes to the parent")
	assert.False(t, judgedAtStopOf(subStop, other), "another agent's range")
	for _, r := range []sessionstate.TrackedRange{inLive, inGone, other} {
		assert.True(t, judgedAtStopOf(rootStop, r), "the root's Stop judges every range: %s", r.Head)
	}
}

// When a root folder of the repository exists, the removed worktree's range is reattributed to it
// (no agent id): the sub-agent's own Stop never selects it again, the root's does.
func TestUntrackGone_AMovedRangeBelongsToTheParentNotTheSubagent(t *testing.T) {
	proj := initRepo(t)
	writeFileGuardYAML(t, proj, "g", "match: path == \"x.md\"\nchecks:\n  - script: ./c.sh\n",
		map[string]string{"c.sh": "#!/bin/sh\nexit 0\n"})
	repoID, err := gitrepo.RootCommit(proj)
	require.NoError(t, err)
	wt := filepath.Join(t.TempDir(), "wt")
	runGit(t, proj, "worktree", "add", "-b", "kept", wt)
	tip := runGit(t, proj, "rev-parse", "HEAD")
	reg := openStore(t)
	for _, f := range []sessionstate.Folder{
		{SessionID: "s1", Path: proj, Role: sessionstate.FolderRoot, GitRoot: proj, RepoID: repoID},
		{SessionID: "s1", Path: wt, Role: sessionstate.FolderSubagentWorktree, GitRoot: wt, RepoID: repoID, AgentID: "sub"},
	} {
		_, err := reg.RegisterFolder(f)
		require.NoError(t, err)
	}
	require.NoError(t, reg.TrackRange(sessionstate.TrackedRange{SessionID: "s1", Folder: wt, Head: "kept", HeadSHA: tip, Base: tip, AgentID: "sub"}))
	runGit(t, proj, "worktree", "remove", "--force", wt)

	ranges, err := reg.Ranges("s1")
	require.NoError(t, err)
	untrackGone(reg, "s1", ranges)
	ranges, err = reg.Ranges("s1")
	require.NoError(t, err)
	var moved *sessionstate.TrackedRange
	for i := range ranges {
		if ranges[i].Tracked() {
			moved = &ranges[i]
		}
	}
	require.NotNil(t, moved)
	assert.Equal(t, filepath.Clean(proj), moved.Folder)
	assert.Empty(t, moved.AgentID, "the range is the parent's now")
	assert.False(t, judgedAtStopOf(HookPayload{AgentID: "sub"}, *moved))
	assert.True(t, judgedAtStopOf(HookPayload{}, *moved))
}
