package main

import (
	"errors"
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

// Commits another identity landed upstream and the session only pulled are brought in: the
// tracked tip stays where the session left it.
func TestTrackMissing_APulledUpstreamCommitIsBroughtIn(t *testing.T) {
	var bare string
	proj, reg, rs := ruledAndObserved(t, func(proj string) { bare = withOrigin(t, proj) })
	before := rangeTip(t, reg, rs.ID, "main")

	up := filepath.Join(t.TempDir(), "up")
	runGit(t, filepath.Dir(up), "clone", "-q", bare, up)
	require.NoError(t, os.WriteFile(filepath.Join(up, "u.md"), []byte("u"), 0o644))
	runGit(t, up, "add", ".")
	runGit(t, up, "-c", "user.name=up", "-c", "user.email=up@example.com", "commit", "-q", "-m", "upstream")
	runGit(t, up, "push", "-q", "origin", "main")
	runGit(t, proj, "pull", "-q", "--ff-only", "origin", "main")

	require.NoError(t, trackMissing(reg, rs, HookPayload{}))
	require.NoError(t, trackFolders(reg, rs, HookPayload{}))
	assert.Equal(t, before, rangeTip(t, reg, rs.ID, "main"), "upstream's commit became the session's tip")
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

// A folder first observed late: its non-checked-out branches carrying commits made since the
// session began are tracked on that first observation; older ones are not.
func TestObserveFolder_ALateFolderTracksItsRecentlyCommittedBranches(t *testing.T) {
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
	assert.False(t, got["old"], "a branch last committed on before the session began is not the session's")
	assert.True(t, got["fresh"], "a branch committed on after the session began, in a late folder, was not tracked")
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
