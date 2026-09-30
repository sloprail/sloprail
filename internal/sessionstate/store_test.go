package sessionstate

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
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

func TestOpen_ConcurrentWritersAcrossHandlesDoNotFail(t *testing.T) {
	// Two hooks firing in one cycle are two PROCESSES, which no in-process
	// connection limit reaches. Separate handles on one file are the closest
	// this can get to that without spawning binaries, and they contend the same
	// way: without a busy timeout the losing writer fails immediately with
	// SQLITE_BUSY, and a failed write exits non-zero, which a harness reads as
	// a refusal of the agent's work.
	path := filepath.Join(t.TempDir(), "state.db")

	const handles, writesPerHandle = 4, 25
	var wg sync.WaitGroup
	errs := make(chan error, handles*writesPerHandle)

	for h := range handles {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := Open(path)
			if err != nil {
				errs <- err
				return
			}
			defer s.Close()
			for i := range writesPerHandle {
				if err := s.SetState("g", fmt.Sprintf("h%d/k%d", h, i), "v"); err != nil {
					errs <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}

	// Every write landed — contention delayed writers rather than dropping
	// what they wrote.
	s, err := Open(path)
	require.NoError(t, err)
	defer s.Close()
	entries, err := s.ListState("g", "")
	require.NoError(t, err)
	assert.Len(t, entries, handles*writesPerHandle)
}

func TestOpen_UsesWAL(t *testing.T) {
	// The mode a losing writer's wait depends on. Asserted because it is set by
	// a pragma that fails silently into the previous mode if it cannot apply.
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := open(path)
	require.NoError(t, err)
	defer s.Close()

	var mode string
	require.NoError(t, s.db.QueryRow("PRAGMA journal_mode").Scan(&mode))
	assert.Equal(t, "wal", strings.ToLower(mode))
}

func TestOpen_RefusesASchemaFromANewerBinary(t *testing.T) {
	// An older binary cannot know which columns it is missing, so running
	// against a schema it does not understand would corrupt a session's record
	// rather than fail it. A sentinel because the caller's response — tell the
	// user to update — is a real decision, not a generic failure.
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := open(path)
	require.NoError(t, err)
	_, err = s.db.Exec("PRAGMA user_version = 99")
	require.NoError(t, err)
	require.NoError(t, s.Close())

	_, err = Open(path)
	assert.ErrorIs(t, err, ErrSchemaTooNew)
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

func TestMigration_DropsTheRetiredFileChecksTable(t *testing.T) {
	s := openTestStore(t)
	var n int
	require.NoError(t, s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = 'file_checks'`).Scan(&n))
	assert.Equal(t, 0, n, "file-guards judge commits now; their verdicts are internal/checkstore's")
}
