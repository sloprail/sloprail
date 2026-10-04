package main

import (
	"os"
	"testing"

	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tests in this package that cover the engine's automatic ref tracking (and the Stop
// verification of what it tracked) run with SR_AUTO_WATCH_GIT_REFS on. The one test of the
// default clears it for itself.
func TestMain(m *testing.M) {
	_ = os.Setenv(autoWatchEnv, "1")
	os.Exit(m.Run())
}

// With SR_AUTO_WATCH_GIT_REFS unset the engine tracks nothing on its own: not the checked-out
// branch, not a branch the session committed on. What the agent adds with `refs track` is
// still a tracked row.
func TestAutoWatch_UnsetTracksNothingAutomatically(t *testing.T) {
	t.Setenv(autoWatchEnv, "")
	proj, reg, rs := ruledAndObserved(t, nil)
	require.False(t, autoWatchGitRefs())

	runGit(t, proj, "switch", "-c", "feature")
	commitFile(t, proj, "f.md", "f")
	require.NoError(t, trackMissing(reg, rs, HookPayload{}))
	require.NoError(t, trackFolders(reg, rs, HookPayload{}))
	require.NoError(t, ensureTracked(reg, rs.ID, proj, "", "HEAD"))

	rows, err := reg.Ranges(rs.ID)
	require.NoError(t, err)
	for _, r := range rows {
		assert.False(t, r.Tracked() && r.AddedBy == sessionstate.RangeAuto, "auto-tracked %s", r.Head)
	}
	assert.False(t, trackedIn_(t, reg, rs.ID, proj, "feature"), "the branch the session committed on is not auto-watched")

	t.Setenv(autoWatchEnv, "1")
	require.NoError(t, trackMissing(reg, rs, HookPayload{}))
	assert.True(t, trackedIn_(t, reg, rs.ID, proj, "feature"), "the same state, with the variable set, is auto-watched")
}
