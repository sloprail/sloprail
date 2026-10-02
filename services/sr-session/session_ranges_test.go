package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/module/modules"
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

// A Stop whose session cannot be identified (no transcript on the payload) is refused, naming
// what is missing and how to recover; it is never read as "no ranges".
func TestVerifyTrackedRanges_AnUnidentifiableSessionIsRefusedNotPassed(t *testing.T) {
	proj := initRepo(t)
	writeFileGuardYAML(t, proj, "g", "match: path == \"x.md\"\nchecks:\n  - script: ./c.sh\n",
		map[string]string{"c.sh": "#!/bin/sh\nexit 0\n"})
	reg, err := modules.Registry()
	require.NoError(t, err)

	got := verifyTrackedRanges(&cobra.Command{}, HookPayload{Cwd: proj}, reg, nil)
	require.NotEmpty(t, got, "a Stop that cannot name its session passed unchecked")
	assert.Contains(t, got[0], "cannot be identified")
	assert.Contains(t, got[0], "transcript_path")
	assert.Contains(t, got[0], "To recover")
}

// corruptRegistry stages a session whose registry exists but cannot be read, and returns the
// payload that names it.
func corruptRegistry(t *testing.T, proj string) HookPayload {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	record := filepath.Join(t.TempDir(), "sess-corrupt.jsonl")
	require.NoError(t, os.WriteFile(record, []byte(`{"type":"user","uuid":"origin","parentUuid":null,"message":{"role":"user","content":"hi"}}`+"\n"), 0o644))
	p := HookPayload{Cwd: proj, SessionID: "sess-corrupt", TranscriptPath: record}
	rs, err := resolveRootSession(p)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(rs.Path), 0o755))
	require.NoError(t, os.WriteFile(rs.Path, []byte("this is not a registry\x00\xff{{{"), 0o644))
	return p
}

// An unreadable folder registry is an error, never "this session registered no folders".
func TestSessionFolders_AnUnreadableRegistryIsAnError(t *testing.T) {
	p := corruptRegistry(t, initRepo(t))
	_, err := sessionFolders(p)
	require.Error(t, err)
	_, err = foldersTargeted(HookPayload{Cwd: p.Cwd, SessionID: p.SessionID, TranscriptPath: p.TranscriptPath,
		ToolName: "Bash", ToolInput: []byte(`{"command":"git -C /tmp commit -m x"}`)})
	assert.Error(t, err, "a Bash call's folders cannot be told from an unreadable registry")
}

// The Stop does not exit early on "no rules here" while the folder registry is unreadable: it
// falls through to the steps that refuse.
func TestDispatchStop_AnUnreadableFolderRegistryDoesNotEndTheStopEarly(t *testing.T) {
	proj := initRepo(t) // no rules in the working directory
	p := corruptRegistry(t, proj)
	reg, err := modules.Registry()
	require.NoError(t, err)
	store := openStore(t)
	t.Chdir(proj)
	cmd := &cobra.Command{}
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetOut(&bytes.Buffer{})
	got := dispatchNatureStop(cmd, p, reg, hookScope{}, store)
	assert.NotEmpty(t, got, "an unreadable registry was read as 'no other folder has rules'")
}

// A range whose session record or id cannot be resolved is refused, not verified with an empty
// identity (its script checks would read "no registry" as a pass).
func TestVerifyRange_AnUnresolvableRecordOrIdIsRefused(t *testing.T) {
	proj := initRepo(t)
	writeFileGuardYAML(t, proj, "g", "match: path == \"x.md\"\nchecks:\n  - script: ./c.sh\n",
		map[string]string{"c.sh": "#!/bin/sh\nexit 0\n"})
	base := runGit(t, proj, "rev-parse", "HEAD")
	require.NoError(t, os.WriteFile(filepath.Join(proj, "x.md"), []byte("x"), 0o644))
	runGit(t, proj, "add", "x.md")
	runGit(t, proj, "commit", "-m", "work")
	tip := runGit(t, proj, "rev-parse", "HEAD")
	reg, err := modules.Registry()
	require.NoError(t, err)

	r := sessionstate.TrackedRange{Folder: proj, Head: "main", HeadSHA: tip, Base: base}
	quiet := &cobra.Command{}
	got := verifyRange(&cobra.Command{}, HookPayload{Cwd: proj}, reg, nil, quiet, r)
	assert.Contains(t, got, "cannot be resolved")
	assert.Contains(t, got, "refusing")
}

// A detached range is skipped only when a tracked branch's range (base..live tip) really holds
// its commit; one older than the branch's base, or past the stored tip's staleness, is verified.
func TestCoveredByBranch_OnlyWhenTheBranchRangeContainsTheCommit(t *testing.T) {
	proj := initRepo(t)
	commit := func(name string) string {
		require.NoError(t, os.WriteFile(filepath.Join(proj, name), []byte(name), 0o644))
		runGit(t, proj, "add", name)
		runGit(t, proj, "commit", "-m", name)
		return runGit(t, proj, "rev-parse", "HEAD")
	}
	first := commit("0.md")
	c1 := commit("a.md")
	c2 := commit("b.md")

	detached := func(sha string) sessionstate.TrackedRange {
		return sessionstate.TrackedRange{Folder: proj, Head: sha, HeadSHA: sha, Base: first}
	}
	branch := func(base, stored string) []sessionstate.TrackedRange {
		return []sessionstate.TrackedRange{{Folder: proj, Head: "main", HeadSHA: stored, Base: base}}
	}
	assert.True(t, coveredByBranch(detached(c2), branch(first, c2)), "inside base..tip")
	assert.False(t, coveredByBranch(detached(c1), branch(c1, c2)), "at the branch's base: before its range")
	assert.False(t, coveredByBranch(detached(first), branch(c1, c2)), "older than the branch's base")
	// The stored tip is stale: the live branch has moved on and now holds c2.
	assert.True(t, coveredByBranch(detached(c2), branch(first, c1)), "the live tip, not the stored one, decides")
}
