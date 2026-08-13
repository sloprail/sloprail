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
