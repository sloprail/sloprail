package checkcache

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// legacyStore is a store whose results were filed by the previous build: in its schema
// directory, under sr1 keys, which held the rule's hash.
func legacyStore(t *testing.T, runs ...Run) *Store {
	t.Helper()
	s := newRepo(t, "")
	s.dir = legacySchemaDir
	s.keyID = func(r Run, c Check) string { return legacyID(r.CheckKey(c), r.RuleHash) }
	for _, r := range runs {
		require.NoError(t, s.Put([]Run{r}))
	}
	s.dir, s.keyID = "", nil
	return s
}

func twoRuleHashes() []Run {
	older := run("2026-01-01T00:00:00Z", judge("fp", StatusFail))
	older.RuleHash = "hash-1"
	newer := run("2026-02-01T00:00:00Z", judge("fp", StatusPass))
	newer.RuleHash = "hash-2"
	other := run("2026-01-15T00:00:00Z", judge("other", StatusPass))
	other.RuleHash = "hash-1"
	return []Run{older, newer, other}
}

// The same input judged under two rule hashes is two results in an old store and one after the
// migration: the newest. The new key (no rule hash) finds it; a second run, in this process or
// a fresh one, writes nothing.
func TestMigrateKeys_OldResultsUnderTwoRuleHashesBecomeTheNewest(t *testing.T) {
	s := legacyStore(t, twoRuleHashes()...)

	got, err := s.Lookup([]Key{key("fp"), key("other")})
	require.NoError(t, err)
	assert.Empty(t, got, "before the migration the new key finds nothing filed under an old one")

	done, err := s.MigrateKeys()
	require.NoError(t, err)
	assert.True(t, done)
	tip := s.tip()
	assert.Contains(t, git(t, s.opt.Dir, "ls-tree", "--name-only", tip), legacySchemaDir, "the old directory stays for this release")

	got, err = s.Lookup([]Key{key("fp"), key("other")})
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, StatusPass, got[key("fp").ID()].Check.Status, "the newest of the two rule hashes wins")
	assert.Equal(t, "hash-2", got[key("fp").ID()].Run.RuleHash)
	assert.Equal(t, StatusPass, got[key("other").ID()].Check.Status)
	runs, err := s.Runs()
	require.NoError(t, err)
	assert.Len(t, runs, 3, "every run stays as history")

	done, err = s.MigrateKeys()
	require.NoError(t, err)
	assert.False(t, done)
	assert.Equal(t, tip, s.tip(), "the second run writes nothing")

	fresh, err := Open(Options{Dir: s.opt.Dir})
	require.NoError(t, err)
	done, err = fresh.MigrateKeys()
	require.NoError(t, err)
	assert.False(t, done, "the migration is recorded in the store, not in the process")
	assert.Equal(t, tip, fresh.tip())
}

// A build that speaks the previous directory refuses the migrated ref rather than reading a
// store it would only half understand.
func TestMigrateKeys_AnOldBinaryRefusesTheNewDirectory(t *testing.T) {
	s := legacyStore(t, twoRuleHashes()...)
	_, err := s.MigrateKeys()
	require.NoError(t, err)

	old, err := Open(Options{Dir: s.opt.Dir})
	require.NoError(t, err)
	old.dir = legacySchemaDir
	_, err = old.Lookup([]Key{key("fp")})
	assert.ErrorIs(t, err, ErrFutureSchema)
}

// A write into an unmigrated store migrates first, so the old results are not hidden by the
// fresh directory the write would otherwise create.
func TestMigrateKeys_APutMigratesFirst(t *testing.T) {
	s := legacyStore(t, twoRuleHashes()...)
	require.NoError(t, s.Put([]Run{run("2026-03-01T00:00:00Z", judge("fresh", StatusPass))}))
	got, err := s.Lookup([]Key{key("fp"), key("fresh")})
	require.NoError(t, err)
	assert.Len(t, got, 2)
}

// Old segments are recognised by their old index keys, never called corrupt.
func TestMigrateKeys_LegacySegmentsAreNotCorrupt(t *testing.T) {
	s := legacyStore(t, twoRuleHashes()...)
	s.dir = legacySchemaDir
	runs, err := s.Runs()
	require.NoError(t, err)
	assert.Len(t, runs, 3)
	_, err = s.Gc()
	assert.NoError(t, err)
}

// Nothing stored, nothing to migrate; a store written by this build is already current.
func TestMigrateKeys_EmptyAndCurrentStoresAreNoOps(t *testing.T) {
	s := newRepo(t, "")
	done, err := s.MigrateKeys()
	require.NoError(t, err)
	assert.False(t, done)
	require.NoError(t, s.Put([]Run{run("2026-01-01T00:00:00Z", judge("fp", StatusPass))}))
	done, err = s.MigrateKeys()
	require.NoError(t, err)
	assert.False(t, done)
}
