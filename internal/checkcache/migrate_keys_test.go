package checkcache

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// olderStore is a store whose results were filed by an older build: in that schema's
// directory, under its ids (sr1 held the rule's hash).
func olderStore(t *testing.T, dir, version string, runs ...Run) *Store {
	t.Helper()
	s := newRepo(t, "")
	require.NoError(t, PutAsOlder(Options{Dir: s.opt.Dir}, dir, version, runs...))
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

// rekey is a Rebuild that moves every pass to a new fingerprint ("new-"+old) and leaves the
// rest as it was, like the engine's rebuild does for what it can reconstruct.
func rekey(seen *[][]Run) Rebuild {
	return func(old []Run) ([]Run, MigrationStats, error) {
		if seen != nil {
			*seen = append(*seen, old)
		}
		var st MigrationStats
		out := make([]Run, 0, len(old))
		for _, r := range old {
			checks := append([]Check(nil), r.Checks...)
			for i, c := range checks {
				if c.Status != StatusPass {
					st.Skip("not a pass")
					continue
				}
				checks[i].Fingerprint = "new-" + c.Fingerprint
				st.Migrated++
			}
			r.Checks = checks
			out = append(out, r)
		}
		return out, st, nil
	}
}

// Whatever the older schema, its verdicts are found under the new key after the migration,
// and every run stays as history. A second call, in this process or a fresh one, writes nothing.
func TestMigrateKeys_AnyOlderDirectoryIsRebuiltIntoTheCurrentOne(t *testing.T) {
	for name, tc := range map[string]struct{ dir, version string }{
		"v2026-10-03 (sr1 keys, released)": {"v2026-10-03", "sr1"},
		"v2026-10-07 (sr2 keys)":           {"v2026-10-07", "sr2"},
	} {
		t.Run(name, func(t *testing.T) {
			s := olderStore(t, tc.dir, tc.version, twoRuleHashes()...)
			s.SetRebuild(rekey(nil))

			got, err := s.Lookup([]Key{key("new-fp"), key("new-other")})
			require.NoError(t, err)
			assert.Empty(t, got, "before the migration the new key finds nothing")

			stats, done, err := s.MigrateKeys()
			require.NoError(t, err)
			assert.True(t, done)
			assert.Equal(t, 2, stats.Migrated)
			assert.Equal(t, 1, stats.Skipped)
			assert.Equal(t, "migrated 2, skipped 1 (not a pass: 1)", stats.String())
			tip := s.tip()
			assert.Contains(t, git(t, s.opt.Dir, "ls-tree", "--name-only", tip), tc.dir, "the older directory stays")

			got, err = s.Lookup([]Key{key("new-fp"), key("new-other")})
			require.NoError(t, err)
			require.Len(t, got, 2)
			assert.Equal(t, StatusPass, got[key("new-fp").ID()].Check.Status)
			assert.Equal(t, "hash-2", got[key("new-fp").ID()].Run.RuleHash)
			runs, err := s.Runs()
			require.NoError(t, err)
			assert.Len(t, runs, 3, "every run stays as history")

			_, done, err = s.MigrateKeys()
			require.NoError(t, err)
			assert.False(t, done)
			assert.Equal(t, tip, s.tip(), "the second run writes nothing")

			fresh, err := Open(Options{Dir: s.opt.Dir})
			require.NoError(t, err)
			fresh.SetRebuild(rekey(nil))
			_, done, err = fresh.MigrateKeys()
			require.NoError(t, err)
			assert.False(t, done, "the migration is recorded in the store, not in the process")
			assert.Equal(t, tip, fresh.tip())
		})
	}
}

// The newest older directory is the source: v2026-10-07 already holds what v2026-10-03's
// migration carried, so a store with both is rebuilt from it alone.
func TestMigrateKeys_TheNewestOlderDirectoryIsTheSource(t *testing.T) {
	s := olderStore(t, "v2026-10-03", "sr1", run("2026-01-01T00:00:00Z", judge("in-03", StatusPass)))
	require.NoError(t, PutAsOlder(Options{Dir: s.opt.Dir}, "v2026-10-07", "sr2", run("2026-02-01T00:00:00Z", judge("in-07", StatusPass))))
	var seen [][]Run
	s.SetRebuild(rekey(&seen))

	_, done, err := s.MigrateKeys()
	require.NoError(t, err)
	assert.True(t, done)
	require.Len(t, seen, 1)
	require.Len(t, seen[0], 1)
	assert.Equal(t, "run_2026-02-01T00:00:00Z", seen[0][0].ID)
}

// A build that speaks an older directory refuses the migrated ref rather than reading a
// store it would only half understand.
func TestMigrateKeys_AnOldBinaryRefusesTheNewDirectory(t *testing.T) {
	for _, dir := range schemaHistory {
		t.Run(dir, func(t *testing.T) {
			s := olderStore(t, dir, "sr2", twoRuleHashes()...)
			s.SetRebuild(rekey(nil))
			_, done, err := s.MigrateKeys()
			require.NoError(t, err)
			require.True(t, done)

			old, err := Open(Options{Dir: s.opt.Dir})
			require.NoError(t, err)
			old.dir = dir
			_, err = old.Lookup([]Key{key("fp")})
			assert.ErrorIs(t, err, ErrFutureSchema)
		})
	}
}

// A write into an older store does not migrate it: the current directory starts beside the older
// one, which stays as it was, and nothing refuses (no Rebuild is needed to write). MigrateKeys is
// the explicit step, and still finds the older directory afterwards.
func TestMigrateKeys_APutStartsTheCurrentDirectoryAndLeavesTheOlderOneAlone(t *testing.T) {
	s := olderStore(t, "v2026-10-07", "sr2", twoRuleHashes()...)
	older := git(t, s.opt.Dir, "rev-parse", s.opt.Ref+":v2026-10-07")

	require.NoError(t, s.Put([]Run{run("2026-03-01T00:00:00Z", judge("fresh", StatusPass))}))
	assert.Equal(t, older, git(t, s.opt.Dir, "rev-parse", s.opt.Ref+":v2026-10-07"), "the older directory is untouched")
	got, err := s.Lookup([]Key{key("fp"), key("fresh")})
	require.NoError(t, err)
	assert.Len(t, got, 1, "only what the current directory holds is found: no fallback into the older one")
	assert.Contains(t, got, key("fresh").ID())

	_, _, err = s.MigrateKeys()
	assert.ErrorIs(t, err, ErrMigrationPending, "with nothing to rebuild the keys by, the explicit step says so")

	s.SetRebuild(rekey(nil))
	st, done, err := s.MigrateKeys()
	require.NoError(t, err)
	assert.True(t, done)
	assert.Equal(t, 2, st.Migrated)
	got, err = s.Lookup([]Key{key("new-fp"), key("new-other"), key("fresh")})
	require.NoError(t, err)
	assert.Len(t, got, 3, "the carried verdicts sit beside what the current directory already held")
}

// What the current directory already holds is never overwritten: a verdict whose new key is there
// stays as it is, and the carried one is history only, counted as skipped.
func TestMigrateKeys_NeverOverwritesAVerdictOfTheCurrentDirectory(t *testing.T) {
	s := olderStore(t, "v2026-10-07", "sr2", twoRuleHashes()...)
	// The current directory already judged the content the older pass was about, and failed it.
	require.NoError(t, s.Put([]Run{run("2026-03-01T00:00:00Z", judge("new-fp", StatusFail))}))
	s.SetRebuild(rekey(nil))

	st, done, err := s.MigrateKeys()
	require.NoError(t, err)
	assert.True(t, done)
	assert.Equal(t, 1, st.Migrated, "only new-other is carried")
	assert.Equal(t, 1, st.Reasons[ReasonAlreadyKeyed])
	got, err := s.Lookup([]Key{key("new-fp"), key("new-other")})
	require.NoError(t, err)
	assert.Equal(t, StatusFail, got[key("new-fp").ID()].Check.Status, "the verdict already there stands")
	assert.Equal(t, StatusPass, got[key("new-other").ID()].Check.Status)

	_, done, err = s.MigrateKeys()
	require.NoError(t, err)
	assert.False(t, done, "a second call does nothing")
}

// A compaction replaces the current directory only: the older layouts stay in the ref, so the
// explicit migration can still be asked for after one.
func TestGc_KeepsTheOlderDirectories(t *testing.T) {
	s := olderStore(t, "v2026-10-07", "sr2", twoRuleHashes()...)
	older := git(t, s.opt.Dir, "rev-parse", s.opt.Ref+":v2026-10-07")
	require.NoError(t, s.Put([]Run{run("2026-03-01T00:00:00Z", judge("fresh", StatusPass))}))
	_, err := s.Gc()
	require.NoError(t, err)
	assert.Equal(t, older, git(t, s.opt.Dir, "rev-parse", s.opt.Ref+":v2026-10-07"))
	got, err := s.Lookup([]Key{key("fresh")})
	require.NoError(t, err)
	assert.Len(t, got, 1)
}

// Old segments are recognised by their old index keys, never called corrupt.
func TestMigrateKeys_OlderSegmentsAreNotCorrupt(t *testing.T) {
	for dir, version := range map[string]string{"v2026-10-03": "sr1", "v2026-10-07": "sr2"} {
		s := olderStore(t, dir, version, twoRuleHashes()...)
		s.dir = dir
		runs, err := s.Runs()
		require.NoError(t, err)
		assert.Len(t, runs, 3, dir)
		_, err = s.Gc()
		assert.NoError(t, err, dir)
	}
}

// Nothing stored, nothing to migrate; a store written by this build is already current.
func TestMigrateKeys_EmptyAndCurrentStoresAreNoOps(t *testing.T) {
	s := newRepo(t, "")
	_, done, err := s.MigrateKeys()
	require.NoError(t, err)
	assert.False(t, done)
	require.NoError(t, s.Put([]Run{run("2026-01-01T00:00:00Z", judge("fp", StatusPass))}))
	_, done, err = s.MigrateKeys()
	require.NoError(t, err)
	assert.False(t, done)
}

// A future key change bumps SchemaDir and appends the old one: the history stays ordered and
// older than the current directory, or the migration would pick the wrong source.
func TestSchemaHistory_IsOrderedAndOlderThanTheCurrentDirectory(t *testing.T) {
	for i, d := range schemaHistory {
		assert.Less(t, d, SchemaDir)
		if i > 0 {
			assert.Less(t, schemaHistory[i-1], d)
		}
	}
}
