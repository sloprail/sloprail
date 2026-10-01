package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/sessionstate"
)

// A rule with no folder in the repository (a plugin's) is measured from the
// session's start. A baseline first taken at a sub-agent's OWN Stop is where its
// work ended, so it is no start at all: the range fails closed instead of being
// empty.
func TestResolveRuleRange_ABaselineTakenAtTheSubagentsStopIsNotAFloor(t *testing.T) {
	repo := initRepo(t)
	start := commitFile(t, repo, "a.txt", "one")
	commitFile(t, repo, "b.txt", "two")
	plugin := declaration.FileGuard{Name: "size", Dir: t.TempDir()} // outside the repo: no folder floor

	state := openStore(t)
	require.NoError(t, state.SetMeta(sessionstate.MetaSessionStart, start))
	require.NoError(t, state.SetMeta(sessionstate.MetaBaselineCommit, start))

	r, err := resolveRuleRange(repo, plugin, nil, state)
	require.NoError(t, err)
	assert.Equal(t, start, r.Base, "a baseline recorded as the work began is the floor")
	assert.Equal(t, gitrepo.FromSessionStart, r.Origin)

	require.NoError(t, state.SetMeta(sessionstate.MetaBaselineAtStop, "1"))
	_, err = resolveRuleRange(repo, plugin, nil, state)
	assert.ErrorIs(t, err, gitrepo.ErrNoSessionStart, "one taken at the Stop is where the work ended, not a floor")
}

// A rule's floor is the parent of the last commit touching its whole .sloprail
// root, not its own folder: a shared lib or another rule's edit moves it too.
func TestResolveRuleRange_TheFloorIsTheParentOfTheLastCommitTouchingTheSloprailRoot(t *testing.T) {
	repo := initRepo(t)
	commitFile(t, repo, "seed.txt", "seed")
	ruleDir := filepath.Join(repo, ".sloprail", "file-guard", "size")
	require.NoError(t, os.MkdirAll(ruleDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ruleDir, "check.sh"), []byte("#!/bin/sh\n"), 0o755))
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-m", "the rule")
	ruleCommit := runGit(t, repo, "rev-parse", "HEAD")
	g := declaration.FileGuard{Name: "size", Dir: ruleDir}

	r, err := resolveRuleRange(repo, g, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, gitrepo.FromFloor, r.Origin)
	assert.Equal(t, runGit(t, repo, "rev-parse", ruleCommit+"^"), r.Base)

	// Another file under .sloprail, outside the rule's folder.
	require.NoError(t, os.MkdirAll(filepath.Join(repo, ".sloprail", "lib"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repo, ".sloprail", "lib", "shared.sh"), []byte("x"), 0o644))
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-m", "edit the lib")
	lib := runGit(t, repo, "rev-parse", "HEAD")
	r, err = resolveRuleRange(repo, g, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, ruleCommit, r.Base, "the parent of the lib edit")
	assert.Equal(t, lib, r.Head)

	// A commit outside .sloprail moves nothing.
	commitFile(t, repo, "later.txt", "later")
	r, err = resolveRuleRange(repo, g, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, ruleCommit, r.Base)
}

// A session that recorded a baseline but never kept its first start (it began before that
// was recorded) fails closed: the baseline is re-taken whenever the tree leaves its
// history, so reading it as the start would reopen the floor hole.
func TestResolveRuleRange_ABaselineWithoutAKeptSessionStartFailsClosed(t *testing.T) {
	repo := initRepo(t)
	start := commitFile(t, repo, "a.txt", "one")
	plugin := declaration.FileGuard{Name: "size", Dir: t.TempDir()}
	state := openStore(t)
	require.NoError(t, state.SetMeta(sessionstate.MetaBaselineCommit, start))

	_, err := resolveRuleRange(repo, plugin, nil, state)
	assert.ErrorIs(t, err, errSessionStartNotKept)
}

// A session (a sub-agent's included) that began in a repository with no commit starts at
// git's empty tree: the commits it makes afterwards are judged, even when the baseline is
// first taken at a Stop that finds them already committed.
func TestResolveRuleRange_ASessionThatBeganUnbornStartsAtTheEmptyTree(t *testing.T) {
	repo := initRepo(t)
	plugin := declaration.FileGuard{Name: "size", Dir: t.TempDir()}
	state := openStore(t)

	_, err := ensureBaselineRecorded(state, repo) // the first tool call, before any commit
	require.NoError(t, err)
	commitFile(t, repo, "a.txt", "one")
	require.NoError(t, state.SetMeta(sessionstate.MetaBaselineAtStop, "1")) // a sub-agent's own Stop took the baseline

	r, err := resolveRuleRange(repo, plugin, nil, state)
	require.NoError(t, err)
	assert.Equal(t, gitrepo.EmptyTree, r.Base)
	assert.False(t, r.Empty())
}

// When the start cannot be recorded at the first tool call (git failed), which may go on to
// commit, the start fails closed to the empty tree instead of becoming that commit at Stop.
func TestRecordBaselineBeforeTool_AFailedRecordingFailsClosedToTheEmptyTree(t *testing.T) {
	repo := initRepo(t)
	commitFile(t, repo, "a.txt", "one")
	require.NoError(t, os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("garbage\n"), 0o644))
	state := openStore(t)

	recordBaselineBeforeTool(discard(), state, HookPayload{Cwd: repo})

	got, had, err := state.Meta(sessionstate.MetaSessionStart)
	require.NoError(t, err)
	require.True(t, had, "a start that could not be recorded is still recorded as unknown")
	assert.Equal(t, sessionstate.SessionStartUnborn, got)
}

// An agent that owns its tree is judged from the folder's own start. The agent store
// here still holds the PARENT session's start (old, before commits B and C that
// main gained since); the registered folder says its tree began at C. The folder wins,
// so B and C are not in the range.
func TestResolveRuleRangeIn_ASessionFoldersStartIsTheStartNotTheParents(t *testing.T) {
	repo := initRepo(t)
	parentStart := commitFile(t, repo, "a.txt", "A")
	commitFile(t, repo, "b.txt", "B")
	c := commitFile(t, repo, "c.txt", "C")
	d := commitFile(t, repo, "d.txt", "D")
	plugin := declaration.FileGuard{Name: "size", Dir: t.TempDir()} // no folder floor

	state := openStore(t)
	require.NoError(t, state.SetMeta(sessionstate.MetaSessionStart, parentStart))

	r, err := resolveRuleRange(repo, plugin, nil, state)
	require.NoError(t, err)
	assert.Equal(t, parentStart, r.Base, "premise: without a folder the recorded start is used")

	folder := &sessionstate.Folder{Path: repo, Role: sessionstate.FolderSubagentWorktree, BaseRef: c}
	r, err = resolveRuleRangeIn(repo, plugin, nil, state, folder)
	require.NoError(t, err)
	assert.Equal(t, c, r.Base)
	assert.Equal(t, d, r.Head)
	assert.Equal(t, gitrepo.FromSessionStart, r.Origin)
}

// A folder whose repository had no commit when work began starts at the empty tree,
// the same answer a root session that began unborn gets.
func TestResolveRuleRangeIn_AnUnbornFolderStartsAtTheEmptyTree(t *testing.T) {
	repo := initRepo(t)
	commitFile(t, repo, "a.txt", "A")
	plugin := declaration.FileGuard{Name: "size", Dir: t.TempDir()}

	folder := &sessionstate.Folder{Path: repo, Role: sessionstate.FolderSubagentWorktree, BaseRef: sessionstate.FolderBaseUnborn}
	r, err := resolveRuleRangeIn(repo, plugin, nil, nil, folder)
	require.NoError(t, err)
	assert.Equal(t, gitrepo.EmptyTree, r.Base)
}

// With no folder and no recorded start the range fails closed, as for the root.
func TestResolveRuleRangeIn_NoFolderAndNoStartFailsClosed(t *testing.T) {
	repo := initRepo(t)
	commitFile(t, repo, "a.txt", "A")
	plugin := declaration.FileGuard{Name: "size", Dir: t.TempDir()}

	_, err := resolveRuleRangeIn(repo, plugin, nil, openStore(t), nil)
	assert.ErrorIs(t, err, gitrepo.ErrNoSessionStart)
}

// A repository a sub-agent stood in (an ad-hoc folder) is judged from its HEAD when it
// was first stood in: what was committed there before is never judged.
func TestResolveRuleRangeIn_AnAdHocFolderStartsAtItsHeadWhenFirstTouched(t *testing.T) {
	repo := initRepo(t)
	commitFile(t, repo, "old.txt", "old")
	touched := commitFile(t, repo, "touched.txt", "when the sub-agent first stood here")
	after := commitFile(t, repo, "after.txt", "its own work")
	plugin := declaration.FileGuard{Name: "size", Dir: t.TempDir()}

	folder := &sessionstate.Folder{Path: repo, Role: sessionstate.FolderAdHoc, BaseRef: touched}
	r, err := resolveRuleRangeIn(repo, plugin, nil, nil, folder)
	require.NoError(t, err)
	assert.Equal(t, touched, r.Base)
	assert.Equal(t, after, r.Head)
}

// The session's own (root) folder does not supply a start: the root's range is unchanged.
func TestResolveRuleRangeIn_TheRootFolderDoesNotSupplyAStart(t *testing.T) {
	repo := initRepo(t)
	first := commitFile(t, repo, "a.txt", "A")
	commitFile(t, repo, "b.txt", "B")
	plugin := declaration.FileGuard{Name: "size", Dir: t.TempDir()}

	folder := &sessionstate.Folder{Path: repo, Role: sessionstate.FolderRoot, BaseRef: first}
	_, err := resolveRuleRangeIn(repo, plugin, nil, nil, folder)
	assert.ErrorIs(t, err, gitrepo.ErrNoSessionStart)
}
