package sessionstate

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func openTemp(t *testing.T) Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s
}

func TestRanges_AnAutomaticTrackingOnlyAddsWhatIsNotThere(t *testing.T) {
	s := openTemp(t)
	require.NoError(t, s.TrackRange(TrackedRange{SessionID: "s", Folder: "/r", Head: "feat", Base: "aaa", AddedBy: RangeAuto}))
	require.NoError(t, s.TrackRange(TrackedRange{SessionID: "s", Folder: "/r", Head: "feat", Base: "bbb", AddedBy: RangeAuto}))
	rs, err := s.Ranges("s")
	require.NoError(t, err)
	require.Len(t, rs, 1)
	assert.Equal(t, "aaa", rs[0].Base, "an automatic tracking never moves a base")
	assert.True(t, rs[0].Tracked())
}

func TestRanges_TheAgentMovesABaseAndDropsARangeWithAReason(t *testing.T) {
	s := openTemp(t)
	require.NoError(t, s.TrackRange(TrackedRange{SessionID: "s", Folder: "/r", Head: "feat", Base: "aaa"}))
	require.NoError(t, s.TrackRange(TrackedRange{SessionID: "s", Folder: "/r", Head: "feat", Base: "bbb", AddedBy: RangeAgent}))
	require.Error(t, s.UntrackRange("s", "/r", "feat", "", "", ""), "a reason is required")
	require.NoError(t, s.UntrackRange("s", "/r", "feat", "the branch was abandoned", "", "t0"))
	// Brought back by the engine? No: what the agent dropped stays dropped.
	require.NoError(t, s.TrackRange(TrackedRange{SessionID: "s", Folder: "/r", Head: "feat", HeadSHA: "t0", Base: "ccc", AddedBy: RangeAuto}))

	rs, err := s.Ranges("s")
	require.NoError(t, err)
	require.Len(t, rs, 1)
	assert.Equal(t, "bbb", rs[0].Base)
	assert.False(t, rs[0].Tracked())
	assert.Equal(t, "the branch was abandoned", rs[0].UntrackedReason)

	// The agent can track it again.
	require.NoError(t, s.TrackRange(TrackedRange{SessionID: "s", Folder: "/r", Head: "feat", Base: "ddd", AddedBy: RangeAgent}))
	rs, _ = s.Ranges("s")
	assert.True(t, rs[0].Tracked())
	assert.Equal(t, "ddd", rs[0].Base)
}

func TestRanges_AnUntrackOfAnUnknownRangeIsRecordedSoAutoDoesNotBringItBack(t *testing.T) {
	s := openTemp(t)
	require.NoError(t, s.UntrackRange("s", "/r", "main", "not mine", "", "t0"))
	require.NoError(t, s.TrackRange(TrackedRange{SessionID: "s", Folder: "/r", Head: "main", HeadSHA: "t0", Base: "aaa", AddedBy: RangeAuto}))
	rs, _ := s.Ranges("s")
	require.Len(t, rs, 1)
	assert.False(t, rs[0].Tracked())
}

// sr:proves session/untracked-range-returns-when-tip-moves
func TestRanges_AnUntrackedRangeIsTrackedAgainWhenItsTipMoves(t *testing.T) {
	s := openTemp(t)
	require.NoError(t, s.TrackRange(TrackedRange{SessionID: "s", Folder: "/r", Head: "feat", HeadSHA: "t1", Base: "aaa", AddedBy: RangeAuto}))
	require.NoError(t, s.UntrackRange("s", "/r", "feat", "dead", "", "t1"))

	// The tip has not moved: the untracking stands.
	require.NoError(t, s.TrackRange(TrackedRange{SessionID: "s", Folder: "/r", Head: "feat", HeadSHA: "t1", AddedBy: RangeAuto}))
	rs, _ := s.Ranges("s")
	require.Len(t, rs, 1)
	assert.False(t, rs[0].Tracked())
	assert.Equal(t, "t1", rs[0].AbandonedTip)

	// New commits: judged again, base kept.
	require.NoError(t, s.TrackRange(TrackedRange{SessionID: "s", Folder: "/r", Head: "feat", HeadSHA: "t2", AddedBy: RangeAuto}))
	rs, _ = s.Ranges("s")
	assert.True(t, rs[0].Tracked(), "a moved tip must re-track the range")
	assert.Equal(t, "aaa", rs[0].Base)
	assert.Equal(t, "t2", rs[0].HeadSHA)
	assert.Empty(t, rs[0].AbandonedTip)
}

// A store written before session_refs became the tracked-ranges table (user_version 5: no
// base, added_by or untracked_reason) is migrated by the engine on open: its rows stay, as
// tracked ranges with no base yet (the reader computes it), and a ref an older engine had
// abandoned reads as untracked.
func TestRanges_AStoreFromBeforeTrackedRangesIsMigratedAndItsRowsStayTracked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	old, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	files, err := migrationFiles()
	require.NoError(t, err)
	require.Greater(t, len(files), 5)
	for _, f := range files[:5] {
		body, err := migrationFS.ReadFile("migrations/" + f)
		require.NoError(t, err)
		_, err = old.Exec(string(body))
		require.NoError(t, err, f)
	}
	_, err = old.Exec(`PRAGMA user_version = 5`)
	require.NoError(t, err)
	folder := t.TempDir() // a folder that exists: a row of one that is gone is pruned on open
	_, err = old.Exec(`INSERT INTO session_refs (session_id, folder, ref, first_tip, tip, agent_id, abandoned_tip) VALUES
		('s', ?, 'refs/heads/live', 'a1', 'a2', '', ''),
		('s', ?, 'refs/heads/dropped', 'b1', 'b2', 'sub', 'b2')`, folder, folder)
	require.NoError(t, err)
	require.NoError(t, old.Close())

	s, err := Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	rs, err := s.Ranges("s")
	require.NoError(t, err)
	require.Len(t, rs, 2)
	byHead := map[string]TrackedRange{}
	for _, r := range rs {
		byHead[r.Head] = r
	}
	live, dropped := byHead["refs/heads/live"], byHead["refs/heads/dropped"]
	assert.True(t, live.Tracked(), "a live row stays a tracked range")
	assert.False(t, dropped.Tracked(), "a ref an older engine had abandoned reads as untracked")
	assert.Equal(t, "abandoned by an older engine", dropped.UntrackedReason)
	assert.Equal(t, "sub", dropped.AgentID)
	assert.Equal(t, "a2", live.HeadSHA)
	assert.Equal(t, RangeAuto, live.AddedBy)
	assert.Equal(t, "", live.Base, "the base of an older row is computed when it is first read")

	require.NoError(t, s.SetRangeBase("s", folder, "refs/heads/live", "m0"))
	rs, err = s.Ranges("s")
	require.NoError(t, err)
	for _, r := range rs {
		if r.Head == "refs/heads/live" {
			assert.Equal(t, "m0", r.Base)
		}
	}
}

func TestRanges_AnUntrackWithNoTipIsRefusedSoItIsNeverPermanent(t *testing.T) {
	s := openTemp(t)
	require.Error(t, s.UntrackRange("s", "/r", "feat", "dead", "", ""), "an empty tip would make the untrack permanent")
	rs, err := s.Ranges("s")
	require.NoError(t, err)
	assert.Empty(t, rs)
}
