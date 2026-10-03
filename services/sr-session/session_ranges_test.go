package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
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
	assert.True(t, coveredByBranch(detached(c2), branch(first, c2), newCoverMemo()), "inside base..tip")
	assert.False(t, coveredByBranch(detached(c1), branch(c1, c2), newCoverMemo()), "at the branch's base: before its range")
	assert.False(t, coveredByBranch(detached(first), branch(c1, c2), newCoverMemo()), "older than the branch's base")
	// The stored tip is stale: the live branch has moved on and now holds c2.
	assert.True(t, coveredByBranch(detached(c2), branch(first, c1), newCoverMemo()), "the live tip, not the stored one, decides")
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

// A branch the agent merely stands on (a colleague's, its tip made elsewhere) whose tip moved
// during the session (here: handed a commit) is tracked whoever authored the commits. We accept
// that over-tracking: the agent can undo it with `sr-session refs untrack --reason`, whereas
// missing a commit would let it escape the Stop.
func TestTrackMissing_ACheckedOutBranchWhoseTipMovedIsTrackedEvenIfTheSessionDidNotMakeTheCommits(t *testing.T) {
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
	assert.ElementsMatch(t, []string{"main", "colleague"}, trackedHeads(t, reg, rs.ID), "a checked-out branch whose tip moved must be tracked (over-tracking is accepted; untrack --reason undoes it)")
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
	trackMissing(reg, rs, p) // the hook that registers the folder observes its branches first
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

func TestAutoBase_NoRemoteDefaultBranchNeverMakesAnEmptyRange(t *testing.T) {
	proj := initRepo(t) // no origin: a local main is not the remote default
	require.NoError(t, os.WriteFile(filepath.Join(proj, "f.txt"), []byte("f"), 0o644))
	runGit(t, proj, "add", "-A")
	runGit(t, proj, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "c")
	sha := strings.TrimSpace(runGit(t, proj, "rev-parse", "HEAD"))
	require.NoError(t, os.WriteFile(filepath.Join(proj, "g.txt"), []byte("g"), 0o644))
	runGit(t, proj, "add", "-A")
	runGit(t, proj, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "d")
	started := sha
	sha = strings.TrimSpace(runGit(t, proj, "rev-parse", "HEAD"))

	assert.Equal(t, started, autoBase(proj, sha, started), "the session's recorded base stands in")
	assert.Equal(t, gitrepo.EmptyTree, autoBase(proj, sha, ""), "nothing recorded: the widest range, never base==head")
}

// A session's feature branch fast-forward-pushed to origin/main (then fetched) is an EMPTY range:
// the base is always the merge base with the remote default branch, whoever made the commits. The
// local Stop is early feedback; CI verifies a push event's before..after, so it covers this push.
func TestTrackCurrent_FeatureBranchFastForwardedToMainIsAnEmptyRangeCIOnPushCoversIt(t *testing.T) {
	bare := t.TempDir()
	runGit(t, bare, "init", "--bare", "--initial-branch=main")
	proj := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "a.txt"), []byte("a"), 0o644))
	runGit(t, proj, "add", "-A")
	runGit(t, proj, "commit", "-q", "-m", "a")
	started := runGit(t, proj, "rev-parse", "HEAD")
	runGit(t, proj, "remote", "add", "origin", bare)
	runGit(t, proj, "push", "-q", "origin", "main")
	runGit(t, proj, "fetch", "-q", "origin")
	runGit(t, proj, "remote", "set-head", "origin", "main")

	reg := openStore(t)
	folder := sessionstate.Folder{SessionID: "s1", Path: proj, Role: sessionstate.FolderRoot, GitRoot: proj, BaseRef: started, Branch: "main"}
	_, err := reg.RegisterFolder(folder)
	require.NoError(t, err)
	_, err = observeFolder(reg, proj, folder, "main", started) // the session's first hook: the baseline

	runGit(t, proj, "switch", "-q", "-c", "feat")
	require.NoError(t, os.WriteFile(filepath.Join(proj, "b.txt"), []byte("b"), 0o644))
	runGit(t, proj, "add", "-A")
	runGit(t, proj, "commit", "-q", "-m", "session work")
	sha := runGit(t, proj, "rev-parse", "HEAD")
	runGit(t, proj, "push", "-q", "origin", "HEAD:refs/heads/main")
	runGit(t, proj, "fetch", "-q", "origin")

	_, err = observeFolder(reg, proj, folder, "feat", sha) // a later hook sees the branch's tip move
	require.NoError(t, err)
	require.NoError(t, trackCurrent(reg, "s1", proj, "", started, false))
	rows, err := reg.Ranges("s1")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, sha, rows[0].HeadSHA)
	assert.Equal(t, sha, rows[0].Base, "the base is the merge base with the default branch: what landed is not in the range")
	assert.Equal(t, sha, autoBase(proj, sha, started))
}

// ruled stages a repo with a file-guard and a registered root folder whose first hook has run
// (the baseline observation), the state every observation test starts from.
func ruledAndObserved(t *testing.T, prepare func(proj string)) (string, sessionstate.Store, rootSession) {
	t.Helper()
	proj := initRepo(t)
	writeFileGuardYAML(t, proj, "g", "match: path == \"x.md\"\nchecks:\n  - script: ./c.sh\n",
		map[string]string{"c.sh": "#!/bin/sh\nexit 0\n"})
	if prepare != nil {
		prepare(proj)
	}
	reg, rs := registered(t, proj)
	require.NoError(t, trackMissing(reg, rs, HookPayload{}))
	return proj, reg, rs
}

// A branch whose tip moved by a MERGE (no commit made by the agent on it) and was left in the same
// call is tracked; a branch that merely exists with unlanded commits and never moved is not.
func TestTrackMissing_AMergedBranchIsTracked(t *testing.T) {
	proj, reg, rs := ruledAndObserved(t, func(proj string) {
		runGit(t, proj, "switch", "-c", "other")
		commitFile(t, proj, "o.md", "o")
		runGit(t, proj, "switch", "main")
		runGit(t, proj, "branch", "work")
	})
	runGit(t, proj, "switch", "work")
	runGit(t, proj, "merge", "--no-ff", "other", "-m", "merge other")
	runGit(t, proj, "switch", "main")
	require.NoError(t, trackMissing(reg, rs, HookPayload{}))
	heads := trackedHeads(t, reg, rs.ID)
	assert.Contains(t, heads, "work")
	assert.NotContains(t, heads, "other", "a branch that never moved in the session is not the session's")
}

// A rebase moves the tip without a "commit" line anywhere.
func TestTrackMissing_ARebasedBranchIsTracked(t *testing.T) {
	proj, reg, rs := ruledAndObserved(t, func(proj string) {
		runGit(t, proj, "switch", "-c", "work")
		commitFile(t, proj, "w.md", "w")
		runGit(t, proj, "switch", "main")
		commitFile(t, proj, "m.md", "m")
	})
	runGit(t, proj, "switch", "work")
	runGit(t, proj, "rebase", "main")
	runGit(t, proj, "switch", "main")
	require.NoError(t, trackMissing(reg, rs, HookPayload{}))
	assert.Contains(t, trackedHeads(t, reg, rs.ID), "work")
}

// git am applies a patch as commits.
func TestTrackMissing_AnAppliedPatchIsTracked(t *testing.T) {
	var patch string
	proj, reg, rs := ruledAndObserved(t, func(proj string) {
		runGit(t, proj, "switch", "-c", "src")
		commitFile(t, proj, "p.md", "p")
		patch = filepath.Join(t.TempDir(), "p.patch")
		require.NoError(t, os.WriteFile(patch, []byte(runGit(t, proj, "format-patch", "--stdout", "-1")+"\n"), 0o644))
		runGit(t, proj, "switch", "main")
		runGit(t, proj, "branch", "work")
		runGit(t, proj, "branch", "-D", "src")
	})
	runGit(t, proj, "switch", "work")
	runGit(t, proj, "am", patch)
	runGit(t, proj, "switch", "main")
	require.NoError(t, trackMissing(reg, rs, HookPayload{}))
	assert.Contains(t, trackedHeads(t, reg, rs.ID), "work")
}

// No reflog exists at all: the detection never reads one.
func TestTrackMissing_WithTheReflogDisabledACommittedBranchIsStillTracked(t *testing.T) {
	proj, reg, rs := ruledAndObserved(t, func(proj string) {
		runGit(t, proj, "config", "core.logAllRefUpdates", "false")
	})
	runGit(t, proj, "switch", "-c", "quick")
	commitFile(t, proj, "x.md", "x")
	runGit(t, proj, "switch", "main")
	require.NoError(t, os.RemoveAll(filepath.Join(proj, ".git", "logs")))
	require.NoError(t, trackMissing(reg, rs, HookPayload{}))
	assert.Contains(t, trackedHeads(t, reg, rs.ID), "quick")
}

// A git error is never "not tracked": the observation returns it, and the Stop refuses on it.
func TestTrackMissing_AGitErrorIsReturnedNotSwallowed(t *testing.T) {
	proj, reg, rs := ruledAndObserved(t, nil)
	require.NoError(t, os.WriteFile(filepath.Join(proj, ".git", "packed-refs"), []byte("garbage\n"), 0o644))
	err := trackMissing(reg, rs, HookPayload{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), proj)
}

// The original case, kept beside the one with a session commit ahead of the recorded start: the
// folder is still at the commit it was registered at (started == head).
func TestAutoBase_NoRemoteDefaultBranchStartedAtHeadNeverMakesAnEmptyRange(t *testing.T) {
	proj := initRepo(t) // no origin: a local main is not the remote default
	require.NoError(t, os.WriteFile(filepath.Join(proj, "f.txt"), []byte("f"), 0o644))
	runGit(t, proj, "add", "-A")
	runGit(t, proj, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "c")
	sha := strings.TrimSpace(runGit(t, proj, "rev-parse", "HEAD"))
	started := sha

	assert.Equal(t, started, autoBase(proj, sha, started), "the session's recorded base stands in")
	assert.Equal(t, gitrepo.EmptyTree, autoBase(proj, sha, ""), "nothing recorded: the widest range, never base==head")
}

// The default base is the merge base with the remote default branch, always; an explicit base
// (an agent's `refs track --base`, a row that names one) is used exactly as given, and an
// automatic row's stored base never outlives a newer merge base.
func TestAutoBase_IsTheMergeBaseWithTheRemoteDefaultAndExplicitBasesStand(t *testing.T) {
	bare := t.TempDir()
	runGit(t, bare, "init", "--bare", "--initial-branch=main")
	proj := initRepo(t)
	first := commitFile(t, proj, "a.md", "a")
	runGit(t, proj, "remote", "add", "origin", bare)
	runGit(t, proj, "push", "-q", "origin", "main")
	runGit(t, proj, "fetch", "-q", "origin")
	runGit(t, proj, "remote", "set-head", "origin", "main")
	runGit(t, proj, "switch", "-q", "-c", "feat")
	own := commitFile(t, proj, "b.md", "b")

	assert.Equal(t, first, autoBase(proj, own, "somewhere-else"), "the registered start never overrides the merge base")

	// Pushed fast-forward and fetched: the head is on the default branch, so the range is empty.
	runGit(t, proj, "push", "-q", "origin", "HEAD:refs/heads/main")
	runGit(t, proj, "fetch", "-q", "origin")
	assert.Equal(t, own, autoBase(proj, own, first), "work the default branch holds is not in the range")

	// An explicit base is as given; the guard still refuses one at or past the head.
	assert.NoError(t, checkTrackBase(proj, first, own))
	assert.Error(t, checkTrackBase(proj, own, own))
}

// A moved range keeps the base the agent chose: moving a worktree's range must not re-derive it.
func TestUntrackGone_AMovedRangeKeepsAnExplicitBase(t *testing.T) {
	proj := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "a.md"), []byte("a"), 0o644))
	runGit(t, proj, "add", "a.md")
	runGit(t, proj, "commit", "-m", "init")
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
	require.NoError(t, reg.TrackRange(sessionstate.TrackedRange{SessionID: "s1", Folder: wt, Head: "kept", HeadSHA: tip, Base: tip, AddedBy: sessionstate.RangeAgent, AgentID: "sub"}))
	runGit(t, proj, "worktree", "remove", "--force", wt)

	ranges, err := reg.Ranges("s1")
	require.NoError(t, err)
	untrackGone(reg, "s1", ranges)

	ranges, err = reg.Ranges("s1")
	require.NoError(t, err)
	var moved *sessionstate.TrackedRange
	for i, r := range ranges {
		if r.Tracked() && r.Head == "kept" {
			moved = &ranges[i]
		}
	}
	require.NotNil(t, moved)
	assert.Equal(t, filepath.Clean(proj), moved.Folder)
	assert.Equal(t, sessionstate.RangeAgent, moved.AddedBy, "an explicit base must not be re-tracked as automatic")
	assert.Equal(t, tip, moved.Base)
}

// A file-guard that fails to load judged nothing: the Stop refuses naming it, as `sr-checks verify`
// does, instead of passing because no sound guard was left.
func TestVerifyRange_ABrokenFileGuardRefusesNamingIt(t *testing.T) {
	proj := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "a.md"), []byte("a"), 0o644))
	runGit(t, proj, "add", "a.md")
	runGit(t, proj, "commit", "-m", "init")
	base := runGit(t, proj, "rev-parse", "HEAD")
	writeFileGuardYAML(t, proj, "broken", "match: [unterminated\nchecks:\n  - script: ./c.sh\n",
		map[string]string{"c.sh": "#!/bin/sh\nexit 0\n"})
	require.NoError(t, os.WriteFile(filepath.Join(proj, "x.md"), []byte("x"), 0o644))
	runGit(t, proj, "add", "-A")
	runGit(t, proj, "commit", "-m", "work")
	head := runGit(t, proj, "rev-parse", "--abbrev-ref", "HEAD")
	modReg, err := modules.Registry()
	require.NoError(t, err)
	cmd := &cobra.Command{}
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetOut(&bytes.Buffer{})

	got := verifyRange(cmd, HookPayload{Cwd: proj}, modReg, openStore(t), cmd, sessionstate.TrackedRange{
		SessionID: "s1", Folder: proj, Head: head, Base: base, AddedBy: sessionstate.RangeAgent,
	})
	assert.Contains(t, got, "could not be loaded")
	assert.Contains(t, got, "broken")
}
