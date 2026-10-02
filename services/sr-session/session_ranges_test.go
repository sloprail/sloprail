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
// range moves to the root's folder so its commits are still verified; a deleted branch's range stays, pinned at its last tip.
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
	// The deleted branch is not dropped: its range is pinned at the last tip on the root.
	assert.Equal(t, map[string]string{"kept": filepath.Clean(proj), tip: filepath.Clean(proj)}, tracked)
	assert.Equal(t, tip, runGit(t, proj, "rev-parse", "refs/sloprail/pins/"+tip), "the tip must be pinned against gc")
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

// A range whose folder is gone and whose branch is gone, with no root folder to move it to, is not
// silently untracked: it stays, and the Stop refuses it until it is verified or untracked with a reason.
func TestDropRemoved_NoHomeKeepsTheRangeTracked(t *testing.T) {
	reg := openStore(t)
	r := sessionstate.TrackedRange{SessionID: "s1", Folder: filepath.Join(t.TempDir(), "gone"), Head: "b", HeadSHA: "abc", Base: "def"}
	require.NoError(t, reg.TrackRange(r))
	dropRemoved(reg, "s1", r)
	ranges, err := reg.Ranges("s1")
	require.NoError(t, err)
	require.Len(t, ranges, 1)
	assert.True(t, ranges[0].Tracked())
}

// A branch recreated at a commit the session made (the original reset away) is tracked though it
// has no commit since registration, and a branch at the folder's registered commit is not.
func TestTrackMissing_ABranchRecreatedAtATipTheSessionMadeIsTracked(t *testing.T) {
	proj := initRepo(t)
	writeFileGuardYAML(t, proj, "g", "match: path == \"x.md\"\nchecks:\n  - script: ./c.sh\n",
		map[string]string{"c.sh": "#!/bin/sh\nexit 0\n"})
	base := runGit(t, proj, "rev-parse", "HEAD")
	reg := openStore(t)
	rs := rootSession{ID: "s1", Cwd: proj}
	_, err := reg.RegisterFolder(sessionstate.Folder{SessionID: rs.ID, Path: proj, Role: sessionstate.FolderRoot, GitRoot: proj, BaseRef: base})
	require.NoError(t, err)
	p := HookPayload{}

	runGit(t, proj, "switch", "-c", "side")
	require.NoError(t, os.WriteFile(filepath.Join(proj, "x.md"), []byte("x"), 0o644))
	runGit(t, proj, "add", "x.md")
	runGit(t, proj, "commit", "-m", "work")
	made := runGit(t, proj, "rev-parse", "HEAD")
	trackMissing(reg, rs, p)

	runGit(t, proj, "reset", "--hard", "main")
	runGit(t, proj, "branch", "copy", made)
	runGit(t, proj, "branch", "idle", base)
	trackMissing(reg, rs, p)

	ranges, err := reg.Ranges(rs.ID)
	require.NoError(t, err)
	tracked := map[string]string{}
	for _, r := range ranges {
		if r.Tracked() {
			tracked[r.Head] = r.HeadSHA
		}
	}
	assert.Equal(t, made, tracked["copy"], "a branch at the session's own tip must be tracked")
	assert.NotContains(t, tracked, "idle", "a branch at the registered commit holds nothing of the session's")
}

// An untracked range is tracked again at the next hook once its branch's tip moves.
func TestTrackMissing_AnUntrackedRangeIsTrackedAgainWhenItsTipMoves(t *testing.T) {
	proj := initRepo(t)
	writeFileGuardYAML(t, proj, "g", "match: path == \"x.md\"\nchecks:\n  - script: ./c.sh\n",
		map[string]string{"c.sh": "#!/bin/sh\nexit 0\n"})
	base := runGit(t, proj, "rev-parse", "HEAD")
	reg := openStore(t)
	rs := rootSession{ID: "s1", Cwd: proj}
	_, err := reg.RegisterFolder(sessionstate.Folder{SessionID: rs.ID, Path: proj, Role: sessionstate.FolderRoot, GitRoot: proj, BaseRef: base})
	require.NoError(t, err)
	p := HookPayload{}

	runGit(t, proj, "switch", "-c", "feature")
	require.NoError(t, os.WriteFile(filepath.Join(proj, "x.md"), []byte("x"), 0o644))
	runGit(t, proj, "add", "x.md")
	runGit(t, proj, "commit", "-m", "work")
	trackMissing(reg, rs, p)
	tip := runGit(t, proj, "rev-parse", "HEAD")
	require.NoError(t, reg.UntrackRange(rs.ID, proj, "feature", "dead", "", tip))

	trackMissing(reg, rs, p)
	ranges, _ := reg.Ranges(rs.ID)
	for _, r := range ranges {
		if r.Head == "feature" {
			assert.False(t, r.Tracked(), "the tip did not move: the untracking stands")
		}
	}

	require.NoError(t, os.WriteFile(filepath.Join(proj, "x.md"), []byte("y"), 0o644))
	runGit(t, proj, "commit", "-am", "more")
	trackMissing(reg, rs, p)
	ranges, _ = reg.Ranges(rs.ID)
	for _, r := range ranges {
		if r.Head == "feature" {
			assert.True(t, r.Tracked(), "a moved tip must track the range again")
		}
	}
}

// trackedHeads is the heads the session tracks (not untracked) in the registry.
func trackedHeads(t *testing.T, reg sessionstate.Store, session string) []string {
	t.Helper()
	ranges, err := reg.Ranges(session)
	require.NoError(t, err)
	var heads []string
	for _, r := range ranges {
		if r.Tracked() {
			heads = append(heads, r.Head)
		}
	}
	return heads
}

// registered stages a root folder at proj's HEAD in a fresh registry.
func registered(t *testing.T, proj string) (sessionstate.Store, rootSession) {
	t.Helper()
	reg := openStore(t)
	rs := rootSession{ID: "s1", Cwd: proj}
	_, err := reg.RegisterFolder(sessionstate.Folder{SessionID: rs.ID, Path: proj, Role: sessionstate.FolderRoot, GitRoot: proj, BaseRef: runGit(t, proj, "rev-parse", "HEAD")})
	require.NoError(t, err)
	return reg, rs
}

// A branch the agent merely stands on (a colleague's, its tip made elsewhere) is not the
// session's: only a branch whose tip this clone committed is tracked.
func TestTrackMissing_ACheckedOutBranchTheSessionDidNotCommitOnIsNotTracked(t *testing.T) {
	proj := initRepo(t)
	writeFileGuardYAML(t, proj, "g", "match: path == \"x.md\"\nchecks:\n  - script: ./c.sh\n",
		map[string]string{"c.sh": "#!/bin/sh\nexit 0\n"})
	reg, rs := registered(t, proj)
	trackMissing(reg, rs, HookPayload{})

	// A commit made on a scratch branch, then given to a branch of someone else's: no commit was
	// made on "colleague" itself.
	runGit(t, proj, "switch", "-c", "scratch")
	require.NoError(t, os.WriteFile(filepath.Join(proj, "x.md"), []byte("x"), 0o644))
	runGit(t, proj, "add", "x.md")
	runGit(t, proj, "commit", "-m", "theirs")
	tip := runGit(t, proj, "rev-parse", "HEAD")
	runGit(t, proj, "switch", "main")
	runGit(t, proj, "branch", "colleague", tip)
	runGit(t, proj, "branch", "-D", "scratch")
	runGit(t, proj, "switch", "colleague")

	trackMissing(reg, rs, HookPayload{})
	assert.ElementsMatch(t, []string{"main"}, trackedHeads(t, reg, rs.ID), "a branch only checked out was tracked as the session's")
}

// A branch committed on and left within ONE call (no hook between the commit and the switch
// back) is still tracked at the next hook, because the commit is the session's.
func TestTrackMissing_ABranchCommittedOnAndLeftWithinOneCallIsTracked(t *testing.T) {
	proj := initRepo(t)
	writeFileGuardYAML(t, proj, "g", "match: path == \"x.md\"\nchecks:\n  - script: ./c.sh\n",
		map[string]string{"c.sh": "#!/bin/sh\nexit 0\n"})
	reg, rs := registered(t, proj)
	trackMissing(reg, rs, HookPayload{})

	runGit(t, proj, "switch", "-c", "quick")
	require.NoError(t, os.WriteFile(filepath.Join(proj, "x.md"), []byte("x"), 0o644))
	runGit(t, proj, "add", "x.md")
	runGit(t, proj, "commit", "-m", "work")
	runGit(t, proj, "switch", "main")

	trackMissing(reg, rs, HookPayload{})
	assert.ElementsMatch(t, []string{"main", "quick"}, trackedHeads(t, reg, rs.ID))
}

// Nothing is tracked in a folder where no file-guard loads, committed on or not.
func TestTrackMissing_NoFileGuardNoTracking(t *testing.T) {
	proj := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "a.md"), []byte("a"), 0o644))
	runGit(t, proj, "add", "a.md")
	runGit(t, proj, "commit", "-m", "init")
	reg, rs := registered(t, proj)

	runGit(t, proj, "switch", "-c", "feature")
	require.NoError(t, os.WriteFile(filepath.Join(proj, "x.md"), []byte("x"), 0o644))
	runGit(t, proj, "add", "x.md")
	runGit(t, proj, "commit", "-m", "work")
	trackMissing(reg, rs, HookPayload{})
	assert.NotContains(t, trackedHeads(t, reg, rs.ID), "feature")
}

// The Stop's tracking sees a branch committed on and left in the same call, and a sub-agent's
// Stop tracks its folders too even though it verifies nothing.
func TestTrackAtHook_RunsFromTheStopsAndTheSubagentStops(t *testing.T) {
	proj := initRepo(t)
	writeFileGuardYAML(t, proj, "g", "match: path == \"x.md\"\nchecks:\n  - script: ./c.sh\n",
		map[string]string{"c.sh": "#!/bin/sh\nexit 0\n"})
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	record := filepath.Join(t.TempDir(), "sess-hook.jsonl")
	require.NoError(t, os.WriteFile(record, []byte(`{"type":"user","uuid":"origin","parentUuid":null,"message":{"role":"user","content":"hi"}}`+"\n"), 0o644))
	p := HookPayload{Cwd: proj, SessionID: "sess-hook", TranscriptPath: record}
	rs, err := resolveRootSession(p)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(rs.Path), 0o755))
	reg, err := sessionstate.Open(rs.Path)
	require.NoError(t, err)
	_, err = reg.RegisterFolder(sessionstate.Folder{SessionID: rs.ID, Path: proj, Role: sessionstate.FolderRoot, GitRoot: proj, BaseRef: runGit(t, proj, "rev-parse", "HEAD")})
	require.NoError(t, err)
	reg.Close()

	runGit(t, proj, "switch", "-c", "quick")
	require.NoError(t, os.WriteFile(filepath.Join(proj, "x.md"), []byte("x"), 0o644))
	runGit(t, proj, "add", "x.md")
	runGit(t, proj, "commit", "-m", "work")
	runGit(t, proj, "switch", "main")

	trackAtHook(p)
	reg, err = sessionstate.Open(rs.Path)
	require.NoError(t, err)
	defer reg.Close()
	assert.Contains(t, trackedHeads(t, reg, rs.ID), "quick")
}

// The root's Stop verifies a range tracked in another folder (a sub-agent's worktree) even when
// the root's own tree declares no rule at all: it must not exit early on "no rules here".
func TestDispatchStop_ARootWithoutRulesStillVerifiesASubagentsRange(t *testing.T) {
	proj := initRepo(t) // no rule in the root's tree
	require.NoError(t, os.WriteFile(filepath.Join(proj, "a.md"), []byte("a"), 0o644))
	runGit(t, proj, "add", "a.md")
	runGit(t, proj, "commit", "-m", "init")
	base := runGit(t, proj, "rev-parse", "HEAD")
	wt := filepath.Join(t.TempDir(), "wt")
	runGit(t, proj, "worktree", "add", "-b", "sub", wt)
	writeFileGuardYAML(t, wt, "g", "match: path == \"x.md\"\nchecks:\n  - script: ./c.sh\n",
		map[string]string{"c.sh": "#!/bin/sh\nexit 0\n"})
	require.NoError(t, os.WriteFile(filepath.Join(wt, "x.md"), []byte("x"), 0o644))
	runGit(t, wt, "add", "x.md")
	runGit(t, wt, "commit", "-m", "the sub-agent's work")
	tip := runGit(t, wt, "rev-parse", "HEAD")

	t.Setenv("XDG_DATA_HOME", t.TempDir())
	record := filepath.Join(t.TempDir(), "sess-root.jsonl")
	require.NoError(t, os.WriteFile(record, []byte(`{"type":"user","uuid":"origin","parentUuid":null,"message":{"role":"user","content":"hi"}}`+"\n"), 0o644))
	p := HookPayload{Cwd: proj, SessionID: "sess-root", TranscriptPath: record}
	rs, err := resolveRootSession(p)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(rs.Path), 0o755))
	reg, err := sessionstate.Open(rs.Path)
	require.NoError(t, err)
	for _, f := range []sessionstate.Folder{
		{SessionID: rs.ID, Path: proj, Role: sessionstate.FolderRoot, GitRoot: proj, BaseRef: base},
		{SessionID: rs.ID, Path: wt, Role: sessionstate.FolderSubagentWorktree, GitRoot: wt, BaseRef: base, AgentID: "sub"},
	} {
		_, err := reg.RegisterFolder(f)
		require.NoError(t, err)
	}
	require.NoError(t, reg.TrackRange(sessionstate.TrackedRange{SessionID: rs.ID, Folder: wt, Head: "sub", HeadSHA: tip, Base: base, AgentID: "sub"}))
	reg.Close()

	modReg, err := modules.Registry()
	require.NoError(t, err)
	t.Chdir(proj)
	cmd := &cobra.Command{}
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetOut(&bytes.Buffer{})
	got := dispatchNatureStop(cmd, p, modReg, hookScope{}, openStore(t))
	assert.Contains(t, got, "not judged yet", "the sub-agent's tracked range went unverified at the root's Stop")
}
