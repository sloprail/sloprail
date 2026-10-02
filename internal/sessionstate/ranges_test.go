package sessionstate

import (
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
	require.NoError(t, s.UntrackRange("s", "/r", "feat", "the branch was abandoned", "", ""))
	// Brought back by the engine? No: what the agent dropped stays dropped.
	require.NoError(t, s.TrackRange(TrackedRange{SessionID: "s", Folder: "/r", Head: "feat", Base: "ccc", AddedBy: RangeAuto}))

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
	require.NoError(t, s.UntrackRange("s", "/r", "main", "not mine", "", ""))
	require.NoError(t, s.TrackRange(TrackedRange{SessionID: "s", Folder: "/r", Head: "main", Base: "aaa", AddedBy: RangeAuto}))
	rs, _ := s.Ranges("s")
	require.Len(t, rs, 1)
	assert.False(t, rs[0].Tracked())
}

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
