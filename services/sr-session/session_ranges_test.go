package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/sessionstate"
)

// A branch the agent committed on and then left is still tracked: the hook that runs after the
// commit registers the branch, so the Stop (which sees only the branch it is on) still verifies it.
func TestTrackMissing_ABranchCommittedOnAndLeftStaysTracked(t *testing.T) {
	proj := initRepo(t)
	writeFileGuardYAML(t, proj, "g", "match: path == \"x.md\"\nchecks:\n  - script: ./c.sh\n",
		map[string]string{"c.sh": "#!/bin/sh\nexit 0\n"})
	base := runGit(t, proj, "rev-parse", "HEAD")

	reg := openStore(t)
	rs := rootSession{ID: "s1", Cwd: proj}
	_, err := reg.RegisterFolder(sessionstate.Folder{SessionID: rs.ID, Path: proj, Role: sessionstate.FolderRoot, GitRoot: proj, BaseRef: base})
	require.NoError(t, err)
	p := HookPayload{}

	trackMissing(reg, rs, p)
	ranges, err := reg.Ranges(rs.ID)
	require.NoError(t, err)
	require.Len(t, ranges, 1)
	assert.Equal(t, "main", ranges[0].Head)

	runGit(t, proj, "switch", "-c", "feature")
	require.NoError(t, os.WriteFile(filepath.Join(proj, "x.md"), []byte("x"), 0o644))
	runGit(t, proj, "add", "x.md")
	runGit(t, proj, "commit", "-m", "work")
	trackMissing(reg, rs, p) // the next hook, still on feature
	runGit(t, proj, "switch", "main")
	trackMissing(reg, rs, p)

	ranges, err = reg.Ranges(rs.ID)
	require.NoError(t, err)
	var heads []string
	for _, r := range ranges {
		if r.Tracked() {
			heads = append(heads, r.Head)
		}
	}
	assert.ElementsMatch(t, []string{"main", "feature"}, heads, "the branch committed on must stay tracked after the agent leaves it")
}

// A removed worktree (a finished sub-agent) must not drop a range whose branch still exists: the
// range moves to the root's folder so its commits are still verified; a deleted branch is untracked.
func TestUntrackGone_AStillExistingBranchMovesToTheRoot(t *testing.T) {
	proj := initRepo(t)
	writeFileGuardYAML(t, proj, "g", "match: path == \"x.md\"\nchecks:\n  - script: ./c.sh\n",
		map[string]string{"c.sh": "#!/bin/sh\nexit 0\n"})
	repoID, err := gitrepo.RootCommit(proj)
	require.NoError(t, err)
	wt := filepath.Join(t.TempDir(), "wt")
	runGit(t, proj, "worktree", "add", "-b", "kept", wt)
	runGit(t, proj, "branch", "doomed")
	tip := runGit(t, proj, "rev-parse", "HEAD")

	reg := openStore(t)
	for _, f := range []sessionstate.Folder{
		{SessionID: "s1", Path: proj, Role: sessionstate.FolderRoot, GitRoot: proj, RepoID: repoID},
		{SessionID: "s1", Path: wt, Role: sessionstate.FolderSubagentWorktree, GitRoot: wt, RepoID: repoID, AgentID: "sub"},
	} {
		_, err := reg.RegisterFolder(f)
		require.NoError(t, err)
	}
	for _, head := range []string{"kept", "doomed"} {
		require.NoError(t, reg.TrackRange(sessionstate.TrackedRange{SessionID: "s1", Folder: wt, Head: head, HeadSHA: tip, Base: tip, AgentID: "sub"}))
	}
	runGit(t, proj, "worktree", "remove", "--force", wt)
	runGit(t, proj, "branch", "-D", "doomed")

	ranges, err := reg.Ranges("s1")
	require.NoError(t, err)
	untrackGone(reg, "s1", ranges)

	ranges, err = reg.Ranges("s1")
	require.NoError(t, err)
	tracked := map[string]string{}
	for _, r := range ranges {
		if r.Tracked() {
			tracked[r.Head] = r.Folder
		}
	}
	assert.Equal(t, map[string]string{"kept": filepath.Clean(proj)}, tracked)
}
