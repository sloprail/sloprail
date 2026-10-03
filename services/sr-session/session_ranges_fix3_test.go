package main

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/sessionstate"
)

// A long branch name is a branch, not a commit.
func TestIsCommitHead_ALongBranchNameIsNotASha(t *testing.T) {
	proj := initRepo(t)
	commitFile(t, proj, "a.txt", "a")
	long := "feature/" + strings.Repeat("x", 37) // 45 chars
	require.Len(t, long, 45)
	runGit(t, proj, "branch", long)
	sha := runGit(t, proj, "rev-parse", "HEAD")
	assert.False(t, isCommitHead(proj, long))
	assert.True(t, isCommitHead(proj, sha))
	assert.Equal(t, "refs/heads/"+long, headRef(proj, long))
	assert.Equal(t, sha, headRef(proj, sha))
	rev, note := headRevision(sessionstate.TrackedRange{Folder: proj, Head: long, HeadSHA: sha})
	assert.Equal(t, "refs/heads/"+long, rev)
	assert.Empty(t, note)
	// a hex-named branch is still a branch
	runGit(t, proj, "branch", strings.Repeat("a", 40))
	assert.False(t, isCommitHead(proj, strings.Repeat("a", 40)))
}

// An unreadable registry refuses with the file and the recovery; an absent one never blocks.
func TestSessionFolders_UnreadableNamesFileAndRecovery_AbsentIsFine(t *testing.T) {
	proj := initRepo(t)
	p := corruptRegistry(t, proj)
	rs, err := resolveRootSession(p)
	require.NoError(t, err)
	_, err = sessionFolders(p)
	require.Error(t, err)
	assert.Contains(t, err.Error(), rs.Path)
	assert.Contains(t, err.Error(), "to recover")

	require.NoError(t, os.Remove(rs.Path))
	folders, err := sessionFolders(p)
	require.NoError(t, err, "an absent registry must not block")
	assert.Empty(t, folders)
}

// A detached HEAD whose commit a remote-tracking ref holds is checked out, not the session's
// work; one no ref holds is the session's.
func TestCheckedOutOnly_RemoteTrackingRefHoldingTheCommitCountsUnheldDoesNot(t *testing.T) {
	proj := initRepo(t)
	commitFile(t, proj, "a.txt", "a")
	withOrigin(t, proj)
	runGit(t, proj, "checkout", "-q", "--detach")
	commitFile(t, proj, "b.txt", "b")
	sha := runGit(t, proj, "rev-parse", "HEAD")
	assert.False(t, checkedOutOnly(proj, sha, nil), "a detached commit on no ref was read as checked out")
	runGit(t, proj, "update-ref", "refs/remotes/origin/other", sha)
	assert.True(t, checkedOutOnly(proj, sha, nil), "a commit a remote-tracking ref holds was read as the session's")
}
