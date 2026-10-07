package checkcache

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ErrMigrationPending means the ref holds an older schema directory and no current one, and
// this store was given no Rebuild to carry the older results across (SetRebuild). A write
// then would hide the older results behind a fresh current directory forever, so it refuses.
var ErrMigrationPending = errors.New("checkcache: the ref holds results of an older schema that have not been migrated")

// MigrationStats says what a migration did: how many verdicts were re-keyed and how many
// were left behind, and why (a count per reason).
type MigrationStats struct {
	Migrated int
	Skipped  int
	Reasons  map[string]int
}

// Skip counts one verdict that could not be carried over, for a reason.
func (m *MigrationStats) Skip(reason string) {
	if m.Reasons == nil {
		m.Reasons = map[string]int{}
	}
	m.Skipped++
	m.Reasons[reason]++
}

// String is the one line a migration reports: "migrated N, skipped M (reason: n, ...)".
func (m MigrationStats) String() string {
	line := fmt.Sprintf("migrated %d, skipped %d", m.Migrated, m.Skipped)
	if m.Skipped == 0 {
		return line
	}
	reasons := make([]string, 0, len(m.Reasons))
	for r, n := range m.Reasons {
		reasons = append(reasons, fmt.Sprintf("%s: %d", r, n))
	}
	sort.Strings(reasons)
	return line + " (" + strings.Join(reasons, ", ") + ")"
}

// Rebuild is what carries an older directory's results into the current key schema. It is
// given every run of that directory and returns the runs to keep under the current schema:
// each run as history, and every verdict it could re-key carrying its NEW fingerprint (a check
// it leaves as it was stays in its run as history but is no longer found). It never judges
// anything: a new key is a pure function of the run's own record and the repository.
//
// It runs under the store's lock, so it must not call the store.
type Rebuild func(old []Run) ([]Run, MigrationStats, error)

// SetRebuild gives the store the Rebuild that MigrateKeys and Put run on an older directory.
func (s *Store) SetRebuild(fn Rebuild) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rebuild = fn
}

// olderDir is the newest directory of schemaHistory that is older than the one this store
// speaks and present in the tip's tree, or "" when the current directory is already there
// (the marker that a migration was done) or no older one is.
func (s *Store) olderDir(tip string) (string, error) {
	listing, err := s.g.str("ls-tree", "--name-only", tip)
	if err != nil {
		return "", err
	}
	have := map[string]bool{}
	for _, name := range strings.Fields(listing) {
		have[name] = true
	}
	if have[s.schemaDir()] {
		return "", nil
	}
	for i := len(schemaHistory) - 1; i >= 0; i-- {
		if schemaHistory[i] < s.schemaDir() && have[schemaHistory[i]] {
			return schemaHistory[i], nil
		}
	}
	return "", nil
}

// MigrateKeys carries the verdicts of an older schema directory into the current one, once.
//
// The newest older directory present (schemaHistory) is read whole, whatever its schema was,
// and handed to the store's Rebuild, which recomputes each verdict's key from its stored run
// and the repository (never by judging again, never from a transcript). What it returns goes
// in as the current directory, one commit on top of the tip, which still holds the older
// directories (a later Gc, which replaces the directory, drops them); every run is kept as
// history. A writer that moves the ref meanwhile is retried against the new tip, and a
// concurrent migration that got there first is seen as "the current directory exists" and left
// alone: the current directory is the marker, so a second call writes nothing.
//
// It reports whether it migrated. A ref with no older directory, one with no results, or a
// current directory already present, is left alone (false), so calling it on every open is
// cheap. With an older directory and no Rebuild it fails with ErrMigrationPending. Put calls it
// first: a result written into a fresh current directory beside an unmigrated older one would
// otherwise hide the older one forever.
func (s *Store) MigrateKeys() (MigrationStats, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.migrateKeys()
}

// sr:invariant cache/older-passes-not-rejudged
func (s *Store) migrateKeys() (MigrationStats, bool, error) {
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(10+attempt*20) * time.Millisecond)
			if err := s.sync(); err != nil {
				return MigrationStats{}, false, err
			}
		}
		tip := s.tip()
		if tip == "" {
			return MigrationStats{}, false, nil
		}
		dir, err := s.olderDir(tip)
		if err != nil || dir == "" {
			return MigrationStats{}, false, err
		}
		old, err := s.readSnapshotDir(tip, dir, false)
		if err != nil {
			return MigrationStats{}, false, err
		}
		if len(old.Segs) == 0 {
			return MigrationStats{}, false, nil
		}
		if s.rebuild == nil {
			return MigrationStats{}, false, fmt.Errorf("%w (%s)", ErrMigrationPending, dir)
		}
		_, _, runs, err := s.readAll(old)
		if err != nil {
			return MigrationStats{}, false, err
		}
		rebuilt, stats, err := s.rebuild(runs)
		if err != nil {
			return MigrationStats{}, false, err
		}
		st := GcStats{Records: len(rebuilt), SegsBefore: len(old.Segs)}
		files, err := s.compactedFiles(rebuilt, &st)
		if err != nil {
			return MigrationStats{}, false, err
		}
		commit, err := s.commit(tip, false, files, fmt.Sprintf("checks: re-key %s into %s, %s", dir, s.schemaDir(), stats))
		if err != nil {
			return MigrationStats{}, false, err
		}
		if _, err := s.g.run(nil, nil, "update-ref", s.opt.Ref, commit, tip); err != nil {
			lastErr = err // a local writer moved the ref meanwhile
			continue
		}
		s.pushErr = s.push()
		return stats, true, nil
	}
	return MigrationStats{}, false, fmt.Errorf("checkcache: key migration gave up after %d attempts: %w", maxAttempts, lastErr)
}
