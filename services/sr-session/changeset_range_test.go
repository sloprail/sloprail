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
	require.NoError(t, state.SetMeta(sessionstate.MetaBaselineCommit, start))

	r, err := resolveRuleRange(repo, plugin, "h", nil, state)
	require.NoError(t, err)
	assert.Equal(t, start, r.Base, "a baseline recorded as the work began is the floor")
	assert.Equal(t, gitrepo.FromSessionStart, r.Origin)

	require.NoError(t, state.SetMeta(sessionstate.MetaBaselineAtStop, "1"))
	_, err = resolveRuleRange(repo, plugin, "h", nil, state)
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

	r, err := resolveRuleRange(repo, g, "h", nil, nil)
	require.NoError(t, err)
	assert.Equal(t, gitrepo.FromFloor, r.Origin)
	assert.Equal(t, runGit(t, repo, "rev-parse", ruleCommit+"^"), r.Base)

	// Another file under .sloprail, outside the rule's folder.
	require.NoError(t, os.MkdirAll(filepath.Join(repo, ".sloprail", "lib"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repo, ".sloprail", "lib", "shared.sh"), []byte("x"), 0o644))
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-m", "edit the lib")
	lib := runGit(t, repo, "rev-parse", "HEAD")
	r, err = resolveRuleRange(repo, g, "h", nil, nil)
	require.NoError(t, err)
	assert.Equal(t, ruleCommit, r.Base, "the parent of the lib edit")
	assert.Equal(t, lib, r.Head)

	// A commit outside .sloprail moves nothing.
	commitFile(t, repo, "later.txt", "later")
	r, err = resolveRuleRange(repo, g, "h", nil, nil)
	require.NoError(t, err)
	assert.Equal(t, ruleCommit, r.Base)
}
