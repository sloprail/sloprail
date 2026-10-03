package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/module"
	"github.com/sloprail/sloprail/internal/sessionstate"
)

// withOrigin gives proj an origin whose default branch is main, and returns the bare path.
func withOrigin(t *testing.T, proj string) string {
	t.Helper()
	bare := t.TempDir()
	runGit(t, bare, "init", "--bare", "--initial-branch=main")
	runGit(t, proj, "remote", "add", "origin", bare)
	runGit(t, proj, "push", "-q", "origin", "main")
	runGit(t, proj, "fetch", "-q", "origin")
	runGit(t, proj, "remote", "set-head", "origin", "main")
	return bare
}

func rangeTip(t *testing.T, reg sessionstate.Store, session, head string) string {
	t.Helper()
	rows, err := reg.Ranges(session)
	require.NoError(t, err)
	for _, r := range rows {
		if r.Head == head && r.Tracked() {
			return r.HeadSHA
		}
	}
	return ""
}

// A session commit pushed fast-forward to the remote default in the same command that made it
// is not "brought in": the tracked tip moves to it, so the range keeps the commit.
func TestTrackMissing_ASessionCommitPushedFastForwardIsStillTracked(t *testing.T) {
	proj, reg, rs := ruledAndObserved(t, func(proj string) { withOrigin(t, proj) })
	before := rangeTip(t, reg, rs.ID, "main")
	require.NotEmpty(t, before)

	sha := commitFile(t, proj, "y.md", "y")
	runGit(t, proj, "push", "-q", "origin", "main")
	runGit(t, proj, "fetch", "-q", "origin")
	require.NoError(t, trackMissing(reg, rs, HookPayload{}))
	require.NoError(t, trackFolders(reg, rs, HookPayload{}))
	assert.Equal(t, sha, rangeTip(t, reg, rs.ID, "main"), "the pushed session commit escaped the tracked range")
}

// Commits a pull brings in are in no range: the default base is the merge base with the remote
// default branch, so the tracked head standing on upstream's commit holds nothing to judge.
func TestTrackMissing_APulledUpstreamCommitIsAnEmptyRange(t *testing.T) {
	var bare string
	proj, reg, rs := ruledAndObserved(t, func(proj string) { bare = withOrigin(t, proj) })

	up := filepath.Join(t.TempDir(), "up")
	runGit(t, filepath.Dir(up), "clone", "-q", bare, up)
	require.NoError(t, os.WriteFile(filepath.Join(up, "u.md"), []byte("u"), 0o644))
	runGit(t, up, "add", ".")
	runGit(t, up, "-c", "user.name=up", "-c", "user.email=up@example.com", "commit", "-q", "-m", "upstream")
	runGit(t, up, "push", "-q", "origin", "main")
	runGit(t, proj, "fetch", "-q", "origin")
	require.NoError(t, trackMissing(reg, rs, HookPayload{})) // the hook that sees the fetch: the remote holds it now
	runGit(t, proj, "merge", "-q", "--ff-only", "origin/main")

	require.NoError(t, trackMissing(reg, rs, HookPayload{}))
	require.NoError(t, trackFolders(reg, rs, HookPayload{}))
	tip := runGit(t, proj, "rev-parse", "HEAD")
	assert.Equal(t, tip, autoBase(proj, tip, ""), "upstream's commit is in a range")
}

// A registry that cannot be built is an error the Stop refuses on, never "no file-guards".
func TestFolderHasFileGuards_ARegistryErrorFailsClosed(t *testing.T) {
	proj, reg, rs := ruledAndObserved(t, nil)
	old := loadRegistry
	loadRegistry = func() (*module.Registry, error) { return nil, errors.New("registry broken") }
	defer func() { loadRegistry = old }()

	_, err := folderHasFileGuards(proj, "")
	require.Error(t, err)

	runGit(t, proj, "switch", "-c", "work")
	commitFile(t, proj, "w.md", "w")
	runGit(t, proj, "switch", "main")
	err = trackMissing(reg, rs, HookPayload{})
	require.Error(t, err, "tracking skipped silently on a registry error")
	assert.Contains(t, err.Error(), "registry broken")
	assert.Error(t, trackCurrent(reg, rs.ID, proj, "", "", true))
}

// A folder first observed late only has its branch tips RECORDED: branches already carrying
// commits are not tracked at first sight (that tracked every old branch of a long-lived
// repository); one is tracked when its tip moves during the session. Commits made before the
// first observation are covered by CI.
func TestObserveFolder_ALateFolderRecordsTipsAndTracksOnlyWhatMoves(t *testing.T) {
	_, reg, rs := ruledAndObserved(t, nil)
	late := initRepo(t)
	writeFileGuardYAML(t, late, "g", "match: path == \"x.md\"\nchecks:\n  - script: ./c.sh\n",
		map[string]string{"c.sh": "#!/bin/sh\nexit 0\n"})
	runGit(t, late, "switch", "-c", "old")
	t.Setenv("GIT_COMMITTER_DATE", "2001-01-01T00:00:00")
	t.Setenv("GIT_AUTHOR_DATE", "2001-01-01T00:00:00")
	commitFile(t, late, "o.md", "o") // made long before the session began
	t.Setenv("GIT_COMMITTER_DATE", "")
	t.Setenv("GIT_AUTHOR_DATE", "")
	os.Unsetenv("GIT_COMMITTER_DATE")
	os.Unsetenv("GIT_AUTHOR_DATE")
	runGit(t, late, "switch", "main")
	runGit(t, late, "switch", "-c", "fresh")
	commitFile(t, late, "f.md", "f")
	runGit(t, late, "switch", "main")
	_, err := reg.RegisterFolder(sessionstate.Folder{SessionID: rs.ID, Path: late, Role: sessionstate.FolderAdHoc, GitRoot: late, BaseRef: runGit(t, late, "rev-parse", "HEAD")})
	require.NoError(t, err)

	require.NoError(t, trackMissing(reg, rs, HookPayload{}))
	got := map[string]bool{}
	rows, err := reg.Ranges(rs.ID)
	require.NoError(t, err)
	for _, r := range rows {
		if r.Folder == filepath.Clean(late) && r.Tracked() {
			got[r.Head] = true
		}
	}
	assert.False(t, got["old"], "a branch first seen with old commits was tracked at first sight")
	assert.False(t, got["fresh"], "a branch first seen in a late folder was tracked at first sight")

	runGit(t, late, "switch", "fresh")
	commitFile(t, late, "g.md", "g") // the tip moves during the session
	runGit(t, late, "switch", "main")
	require.NoError(t, trackMissing(reg, rs, HookPayload{}))
	got = map[string]bool{}
	rows, err = reg.Ranges(rs.ID)
	require.NoError(t, err)
	for _, r := range rows {
		if r.Folder == filepath.Clean(late) && r.Tracked() {
			got[r.Head] = true
		}
	}
	assert.True(t, got["fresh"], "a branch whose tip moved was not tracked")
	assert.False(t, got["old"])
}

// The root's hook observes the sub-agent's folders too.
func TestTrackMissing_TheRootObservesASubagentsFolder(t *testing.T) {
	proj, reg, rs := ruledAndObserved(t, nil)
	wt := filepath.Join(t.TempDir(), "wt")
	runGit(t, proj, "worktree", "add", "-b", "sub", wt)
	_, err := reg.RegisterFolder(sessionstate.Folder{SessionID: rs.ID, Path: wt, Role: sessionstate.FolderSubagentWorktree, GitRoot: wt, BaseRef: runGit(t, proj, "rev-parse", "HEAD"), AgentID: "sub1"})
	require.NoError(t, err)
	require.NoError(t, trackMissing(reg, rs, HookPayload{})) // baseline of the sub-agent's folder
	sha := commitFile(t, wt, "s.md", "s")
	require.NoError(t, trackMissing(reg, rs, HookPayload{})) // a ROOT hook (no AgentID)
	assert.Equal(t, sha, rangeTip(t, reg, rs.ID, "sub"))
}

// `refs track --base` cannot empty a range: the head or a descendant of it is refused.
func TestCheckTrackBase_RefusesABaseThatEmptiesTheRange(t *testing.T) {
	proj := initRepo(t)
	first := commitFile(t, proj, "a.md", "a")
	head := commitFile(t, proj, "b.md", "b")
	later := commitFile(t, proj, "c.md", "c")

	assert.NoError(t, checkTrackBase(proj, first, head))
	assert.Error(t, checkTrackBase(proj, head, head))
	assert.Error(t, checkTrackBase(proj, later, head), "a descendant of the head empties the range")
	assert.Error(t, checkTrackBase(proj, "no-such-rev", head))
}

// A sub-agent's own worktree is walked once by commitRequired, not once as the own tree and
// again as a registered folder.
func TestCommitRequired_AFolderIsOwedOnceNotTwice(t *testing.T) {
	proj := initRepo(t)
	writeFileGuardYAML(t, proj, "g", "match: path == \"x.md\"\nchecks:\n  - script: ./c.sh\n",
		map[string]string{"c.sh": "#!/bin/sh\nexit 0\n"})
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	record := filepath.Join(t.TempDir(), "sess.jsonl")
	require.NoError(t, os.WriteFile(record, []byte(`{"type":"user","uuid":"o","parentUuid":null,"message":{"role":"user","content":"hi"}}`+"\n"), 0o644))
	p := HookPayload{Cwd: proj, SessionID: "sess", TranscriptPath: record}
	rs, err := resolveRootSession(p)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(rs.Path), 0o755))
	reg, err := sessionstate.Open(rs.Path)
	require.NoError(t, err)
	_, err = reg.RegisterFolder(sessionstate.Folder{SessionID: rs.ID, Path: proj, Role: sessionstate.FolderAdHoc, GitRoot: proj, BaseRef: runGit(t, proj, "rev-parse", "HEAD")})
	require.NoError(t, err)
	reg.Close()
	require.NoError(t, os.WriteFile(filepath.Join(proj, "x.md"), []byte("x"), 0o644))

	modReg, err := loadRegistry()
	require.NoError(t, err)
	got := commitRequired(discard(), p, []declaration.FileGuard{{Name: "g", Match: `path == "x.md"`}}, nil, modReg)
	assert.Equal(t, 1, strings.Count(got, "x.md"), "the same file was owed twice:\n"+got)
}

// A detached HEAD on a commit some ref holds was checked out, not made: not tracked. A commit
// made on the detached HEAD (on no ref) is.
func TestCheckedOutOnly_ADetachedCheckoutIsNotTheSessionsButADetachedCommitIs(t *testing.T) {
	proj, _, _ := ruledAndObserved(t, nil)
	runGit(t, proj, "switch", "-c", "pr")
	pr := commitFile(t, proj, "p.md", "p")
	runGit(t, proj, "switch", "-q", "--detach", pr)
	assert.True(t, checkedOutOnly(proj, pr, nil), "another branch's commit was read as the session's")
	assert.False(t, checkedOutOnly(proj, pr, []sessionstate.TrackedRange{{HeadSHA: pr}}), "a tip the session recorded is its own")
	own := commitFile(t, proj, "q.md", "q")
	assert.False(t, checkedOutOnly(proj, own, nil), "a commit on no ref is the session's")
}

// A rule only on the default branch still makes an older branch's checkout worth tracking.
func TestFolderHasFileGuards_ARuleOnTheDefaultBranchCountsOnAnOlderCheckout(t *testing.T) {
	proj := initRepo(t)
	commitFile(t, proj, "seed.md", "seed")
	runGit(t, proj, "branch", "old")
	writeFileGuardYAML(t, proj, "g", "match: path == \"x.md\"\nchecks:\n  - script: ./c.sh\n",
		map[string]string{"c.sh": "#!/bin/sh\nexit 0\n"})
	runGit(t, proj, "switch", "-q", "old")
	has, err := folderHasFileGuards(proj, "")
	require.NoError(t, err)
	assert.True(t, has)
}

// A session commit whose author AND committer identities differ from the folder's, pushed in the
// command that made it, is still the session's: no identity decides what was pulled.
func TestTrackMissing_AForgedIdentityCommitPushedInOneCommandIsStillTracked(t *testing.T) {
	proj, reg, rs := ruledAndObserved(t, func(proj string) { withOrigin(t, proj) })
	require.NotEmpty(t, rangeTip(t, reg, rs.ID, "main"))

	require.NoError(t, os.WriteFile(filepath.Join(proj, "z.md"), []byte("z"), 0o644))
	runGit(t, proj, "add", ".")
	runGit(t, proj, "-c", "user.name=other", "-c", "user.email=other@example.com", "commit", "-q", "-m", "z")
	sha := runGit(t, proj, "rev-parse", "HEAD")
	runGit(t, proj, "push", "-q", "origin", "main")
	runGit(t, proj, "fetch", "-q", "origin")
	require.NoError(t, trackMissing(reg, rs, HookPayload{}))
	require.NoError(t, trackFolders(reg, rs, HookPayload{}))
	assert.Equal(t, sha, rangeTip(t, reg, rs.ID, "main"), "a commit under another identity escaped the tracked range")
}

// A commit made on a detached HEAD and pushed stands on a remote-tracking ref: it exists on a
// remote, where CI verifies it, so the local Stop does not track it.
func TestTrackMissing_ADetachedCommitHeldByARemoteTrackingRefIsNotTracked(t *testing.T) {
	proj, reg, rs := ruledAndObserved(t, func(proj string) { withOrigin(t, proj) })
	runGit(t, proj, "switch", "-q", "--detach")
	sha := commitFile(t, proj, "d.md", "d")
	runGit(t, proj, "push", "-q", "origin", "HEAD:main")
	runGit(t, proj, "fetch", "-q", "origin")
	require.NoError(t, trackMissing(reg, rs, HookPayload{}))
	require.NoError(t, trackFolders(reg, rs, HookPayload{}))
	assert.Empty(t, rangeTip(t, reg, rs.ID, sha), "a detached commit a remote-tracking ref holds was tracked")
}

// A detached checkout of a commit the remote holds may be tracked (over-tracking), but its range
// is empty: the base is the merge base with the remote default branch.
func TestTrackMissing_ADetachedCheckoutOfARemoteCommitIsAnEmptyRange(t *testing.T) {
	var bare string
	proj, reg, rs := ruledAndObserved(t, func(proj string) { bare = withOrigin(t, proj) })
	up := filepath.Join(t.TempDir(), "up")
	runGit(t, filepath.Dir(up), "clone", "-q", bare, up)
	require.NoError(t, os.WriteFile(filepath.Join(up, "u.md"), []byte("u"), 0o644))
	runGit(t, up, "add", ".")
	runGit(t, up, "-c", "user.name=up", "-c", "user.email=up@example.com", "commit", "-q", "-m", "upstream")
	runGit(t, up, "push", "-q", "origin", "main")
	runGit(t, proj, "fetch", "-q", "origin")
	require.NoError(t, trackMissing(reg, rs, HookPayload{})) // the hook that sees the fetch
	runGit(t, proj, "switch", "-q", "--detach", "origin/main")
	require.NoError(t, trackMissing(reg, rs, HookPayload{}))
	require.NoError(t, trackFolders(reg, rs, HookPayload{}))
	head := runGit(t, proj, "rev-parse", "HEAD")
	assert.Equal(t, head, autoBase(proj, head, ""), "someone else's commit is in a range")
}

// Rows the removed first-sight rule left (auto, never moved) are pruned once; a row that moved,
// or one the agent stated, is kept; a second hook prunes nothing more.
func TestPruneUnmovedAuto(t *testing.T) {
	_, reg, rs := ruledAndObserved(t, nil)
	proj := initRepo(t) // a folder met late: not the session's root
	commitFile(t, proj, "base.md", "base")
	_, err := reg.RegisterFolder(sessionstate.Folder{SessionID: rs.ID, Path: proj, Role: sessionstate.FolderAdHoc, GitRoot: proj, BaseRef: runGit(t, proj, "rev-parse", "HEAD")})
	require.NoError(t, err)
	for _, b := range []string{"stale", "moved", "stated"} {
		runGit(t, proj, "switch", "-q", "-c", b)
		commitFile(t, proj, b+".md", b)
	}
	runGit(t, proj, "switch", "-q", "main")
	sha := func(b string) string { return runGit(t, proj, "rev-parse", b) }
	for _, b := range []string{"stale", "moved"} {
		require.NoError(t, reg.TrackRange(sessionstate.TrackedRange{SessionID: rs.ID, Folder: proj, Head: b, HeadSHA: sha(b), Base: sha("main"), AddedBy: sessionstate.RangeAuto}))
	}
	require.NoError(t, reg.TrackRange(sessionstate.TrackedRange{SessionID: rs.ID, Folder: proj, Head: "stated", HeadSHA: sha("stated"), Base: sha("main"), AddedBy: sessionstate.RangeAgent}))
	runGit(t, proj, "switch", "-q", "moved")
	commitFile(t, proj, "more.md", "more") // the branch moves past its first tip
	runGit(t, proj, "switch", "-q", "main")
	require.NoError(t, reg.TrackRange(sessionstate.TrackedRange{SessionID: rs.ID, Folder: proj, Head: "moved", HeadSHA: sha("moved"), AddedBy: sessionstate.RangeAuto}))

	require.NoError(t, pruneUnmovedAuto(reg, rs.ID))
	require.NoError(t, pruneUnmovedAuto(reg, rs.ID)) // idempotent
	rows, err := reg.Ranges(rs.ID)
	require.NoError(t, err)
	tracked := map[string]bool{}
	reason := map[string]string{}
	for _, r := range rows {
		tracked[r.Head] = r.Tracked()
		reason[r.Head] = r.UntrackedReason
	}
	assert.False(t, tracked["stale"], "an auto row that never moved stayed tracked")
	assert.Equal(t, prunedReason, reason["stale"])
	assert.True(t, tracked["moved"], "a row that moved was pruned")
	assert.True(t, tracked["stated"], "an explicit row was pruned")
}

// N sub-agent worktrees of one repository, each on its own branch: every worktree answers for its
// own branch only. A branch visible in the shared ref namespace is not every folder's work (it was
// N x N rows, and the parent's Stop refused with hundreds of "not judged yet" lines).
func TestTrackMissing_AWorktreeTracksItsOwnBranchNotEveryVisibleOne(t *testing.T) {
	const n = 4
	proj, reg, rs := ruledAndObserved(t, nil)
	base := runGit(t, proj, "rev-parse", "HEAD")
	dirs := make([]string, n)
	for i := range dirs {
		dirs[i] = filepath.Join(t.TempDir(), fmt.Sprintf("wt%d", i))
		runGit(t, proj, "worktree", "add", "-q", "-b", fmt.Sprintf("worktree-agent-%d", i), dirs[i])
		_, err := reg.RegisterFolder(sessionstate.Folder{SessionID: rs.ID, Path: dirs[i], Role: sessionstate.FolderSubagentWorktree, GitRoot: dirs[i], BaseRef: base, AgentID: fmt.Sprintf("agent-%d", i)})
		require.NoError(t, err)
	}
	require.NoError(t, trackMissing(reg, rs, HookPayload{}))
	for i, d := range dirs {
		commitFile(t, d, fmt.Sprintf("f%d.md", i), "work")
	}
	require.NoError(t, trackMissing(reg, rs, HookPayload{}))
	require.NoError(t, trackMissing(reg, rs, HookPayload{}))
	rows, err := reg.Ranges(rs.ID)
	require.NoError(t, err)
	tracked := 0
	for _, r := range rows {
		if !r.Tracked() {
			continue
		}
		tracked++
		for i, d := range dirs {
			if filepath.Clean(r.Folder) == resolved(t, d) {
				assert.Equal(t, fmt.Sprintf("worktree-agent-%d", i), r.Head, "a worktree tracked a branch that is not its own")
				assert.Equal(t, fmt.Sprintf("agent-%d", i), r.AgentID)
			}
		}
	}
	assert.LessOrEqual(t, tracked, n+1, "N worktrees x N branches must yield N rows, not N x N (+ the root's)")
}

// Rows an older engine made for a branch checked out in another worktree are pruned once; the
// worktree standing on the branch keeps its own row, and an explicit row is kept.
func TestPruneForeignAuto(t *testing.T) {
	proj, reg, rs := ruledAndObserved(t, nil)
	base := runGit(t, proj, "rev-parse", "HEAD")
	a, b := filepath.Join(t.TempDir(), "a"), filepath.Join(t.TempDir(), "b")
	runGit(t, proj, "worktree", "add", "-q", "-b", "br-a", a)
	runGit(t, proj, "worktree", "add", "-q", "-b", "br-b", b)
	row := func(folder, head, by string) sessionstate.TrackedRange {
		return sessionstate.TrackedRange{SessionID: rs.ID, Folder: resolved(t, folder), Head: head, HeadSHA: base, Base: base, AddedBy: by}
	}
	for _, r := range []sessionstate.TrackedRange{
		row(a, "br-a", sessionstate.RangeAuto), row(a, "br-b", sessionstate.RangeAuto), row(a, "main", sessionstate.RangeAuto),
		row(b, "br-b", sessionstate.RangeAuto), row(b, "br-a", sessionstate.RangeAgent),
	} {
		require.NoError(t, reg.TrackRange(r))
	}
	require.NoError(t, pruneForeignAuto(reg, rs.ID))
	require.NoError(t, pruneForeignAuto(reg, rs.ID)) // idempotent
	rows, err := reg.Ranges(rs.ID)
	require.NoError(t, err)
	got := map[string]bool{}
	for _, r := range rows {
		got[filepath.Base(r.Folder)+":"+r.Head] = r.Tracked()
	}
	assert.True(t, got["a:br-a"], "a worktree's own branch was pruned")
	assert.False(t, got["a:br-b"], "another worktree's branch stayed tracked")
	assert.False(t, got["a:main"], "the main checkout's branch stayed tracked in a worktree")
	assert.True(t, got["b:br-b"])
	assert.True(t, got["b:br-a"], "an explicit row was pruned")
}

func resolved(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	require.NoError(t, err)
	return filepath.Clean(r)
}
