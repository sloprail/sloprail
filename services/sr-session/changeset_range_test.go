package main

import (
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
