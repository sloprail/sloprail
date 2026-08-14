package sessionstate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMeta_AbsentKeyIsNotAnError(t *testing.T) {
	s := openTestStore(t)

	value, found, err := s.Meta(MetaBaselineCommit)
	require.NoError(t, err)
	assert.False(t, found)
	assert.Empty(t, value)
}

func TestSetMeta_RoundTrips(t *testing.T) {
	s := openTestStore(t)
	require.NoError(t, s.SetMeta(MetaBaselineCommit, "abc123"))

	value, found, err := s.Meta(MetaBaselineCommit)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "abc123", value)
}

func TestSetMeta_Replaces(t *testing.T) {
	// The baseline moves when a cycle takes a new measurement — the ordinary
	// case, not a conflict.
	s := openTestStore(t)
	require.NoError(t, s.SetMeta(MetaBaselineCommit, "first"))
	require.NoError(t, s.SetMeta(MetaBaselineCommit, "second"))

	value, _, err := s.Meta(MetaBaselineCommit)
	require.NoError(t, err)
	assert.Equal(t, "second", value)
}

func TestSetMeta_KeysAreIndependent(t *testing.T) {
	// The branch is kept beside the commit so a cycle can notice the agent
	// switched lines of history; writing one must not disturb the other.
	s := openTestStore(t)
	require.NoError(t, s.SetMeta(MetaBaselineCommit, "abc123"))
	require.NoError(t, s.SetMeta(MetaBaselineBranch, "main"))
	require.NoError(t, s.SetMeta(MetaTranscriptRead, "42"))

	commit, _, err := s.Meta(MetaBaselineCommit)
	require.NoError(t, err)
	branch, _, err := s.Meta(MetaBaselineBranch)
	require.NoError(t, err)
	read, _, err := s.Meta(MetaTranscriptRead)
	require.NoError(t, err)

	assert.Equal(t, "abc123", commit)
	assert.Equal(t, "main", branch)
	assert.Equal(t, "42", read)
}

func TestSetMeta_EmptyValueIsStoredNotAbsent(t *testing.T) {
	// "Written as empty" and "never written" are different answers, and a
	// caller deciding whether to take a baseline depends on the difference.
	s := openTestStore(t)
	require.NoError(t, s.SetMeta("k", ""))

	value, found, err := s.Meta("k")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Empty(t, value)
}

func TestSwapMeta_WritesWhileTheStoredValueIsTheOneTheCallerRead(t *testing.T) {
	// The ordinary case: nothing moved underneath, so the write lands.
	s := openTestStore(t)
	require.NoError(t, s.SetMeta("k", "a"))

	ok, err := s.SwapMeta("k", "a", "b")
	require.NoError(t, err)
	assert.True(t, ok)

	value, _, err := s.Meta("k")
	require.NoError(t, err)
	assert.Equal(t, "b", value)
}

func TestSwapMeta_RefusesWhenTheValueMovedUnderneath(t *testing.T) {
	// The whole reason this exists. A caller read "a", decided on it, and by the
	// time it writes the stored value is someone else's — the decision was made
	// against a value that is no longer there, so the write must not land and the
	// caller must be told rather than silently clobbering.
	s := openTestStore(t)
	require.NoError(t, s.SetMeta("k", "moved-on"))

	ok, err := s.SwapMeta("k", "a", "b")
	require.NoError(t, err)
	assert.False(t, ok)

	value, _, err := s.Meta("k")
	require.NoError(t, err)
	assert.Equal(t, "moved-on", value, "the loser must leave the winner's value alone")
}

func TestSwapMeta_AnAbsentKeyIsMatchedByAnEmptyOld(t *testing.T) {
	// The first write of a key goes through the same call rather than needing a
	// look-then-write, which would be the very race this closes.
	s := openTestStore(t)

	ok, err := s.SwapMeta("k", "", "first")
	require.NoError(t, err)
	assert.True(t, ok)

	value, found, err := s.Meta("k")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "first", value)
}

func TestSwapMeta_AnAbsentKeyDoesNotSatisfyACallerExpectingAValue(t *testing.T) {
	// The asymmetry worth pinning, and the one an INSERT ... ON CONFLICT gets
	// wrong on its own: the comparison only runs on the conflict path, so a
	// caller expecting "a" would INSERT against a row that is not there and
	// report success — a swap that matched nothing.
	s := openTestStore(t)

	ok, err := s.SwapMeta("k", "a", "b")
	require.NoError(t, err)
	assert.False(t, ok)

	_, found, err := s.Meta("k")
	require.NoError(t, err)
	assert.False(t, found, "a refused swap must not create the key")
}

func TestSwapMeta_AnEmptyStoredValueIsNotAnAbsentOne(t *testing.T) {
	// A key written as empty is a value, and this session's bookkeeping writes one
	// deliberately: clearing the read position stores "". A caller that read ""
	// from a present key and one writing a key for the first time are both matched
	// by an empty old, which is what makes the clear-then-record sequence work.
	s := openTestStore(t)
	require.NoError(t, s.SetMeta("k", ""))

	ok, err := s.SwapMeta("k", "", "recorded")
	require.NoError(t, err)
	assert.True(t, ok)

	value, _, err := s.Meta("k")
	require.NoError(t, err)
	assert.Equal(t, "recorded", value)
}
