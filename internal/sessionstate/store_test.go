package sessionstate

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// openTestStore returns a migrated store over an in-memory database — no temp
// files, no cleanup beyond closing.
func openTestStore(t *testing.T) *store {
	t.Helper()
	s, err := open(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpen_CreatesParentDirectories(t *testing.T) {
	// The session directory does not exist before the first hook runs, so
	// creating it is the store's job rather than a precondition on the caller.
	path := filepath.Join(t.TempDir(), "sessions", "abc", "state.db")
	s, err := Open(path)
	require.NoError(t, err)
	defer s.Close()

	require.NoError(t, s.SetMeta(MetaBaselineCommit, "deadbeef"))
	assert.FileExists(t, path)
}

func TestOpen_ReopensExistingDatabase(t *testing.T) {
	// A hook is a fresh process every time. What one wrote must be there for
	// the next, which is the whole reason this is a database and not a map.
	path := filepath.Join(t.TempDir(), "state.db")

	first, err := Open(path)
	require.NoError(t, err)
	require.NoError(t, first.SetMeta(MetaBaselineCommit, "c0ffee"))
	require.NoError(t, first.Close())

	second, err := Open(path)
	require.NoError(t, err)
	defer second.Close()

	value, found, err := second.Meta(MetaBaselineCommit)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "c0ffee", value)
}

func TestOpen_MigrationIsIdempotent(t *testing.T) {
	// Every hook run opens the database and migrates it. Re-applying a
	// migration already applied would fail on the first CREATE TABLE.
	path := filepath.Join(t.TempDir(), "state.db")
	for range 3 {
		s, err := Open(path)
		require.NoError(t, err)
		require.NoError(t, s.Close())
	}
}

func TestStore_ClosedStoreReportsItself(t *testing.T) {
	s := openTestStore(t)
	require.NoError(t, s.Close())

	err := s.SetMeta("k", "v")
	assert.ErrorIs(t, err, ErrClosed)

	_, _, err = s.Meta("k")
	assert.ErrorIs(t, err, ErrClosed)

	_, err = s.ListState("g", "")
	assert.ErrorIs(t, err, ErrClosed)
}

func TestStore_CloseIsIdempotent(t *testing.T) {
	s := openTestStore(t)
	require.NoError(t, s.Close())
	assert.NoError(t, s.Close())
}
