package checkcache

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ErrMigrationPending means MigrateKeys met an older schema directory and this store was given
// no Rebuild to carry its results across (SetRebuild).
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

// SetRebuild gives the store the Rebuild that MigrateKeys runs on an older directory.
func (s *Store) SetRebuild(fn Rebuild) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rebuild = fn
}

// olderDir is the newest directory of schemaHistory that is older than the one this store
// speaks and present in the tip's tree, or "" when there is none or it has already been
// carried over (the commit that did it says so: see migrated).
func (s *Store) olderDir(tip string) (string, error) {
	listing, err := s.g.str("ls-tree", "--name-only", tip)
	if err != nil {
		return "", err
	}
	have := map[string]bool{}
	for _, name := range strings.Fields(listing) {
		have[name] = true
	}
	for i := len(schemaHistory) - 1; i >= 0; i-- {
		if schemaHistory[i] < s.schemaDir() && have[schemaHistory[i]] {
			if s.migrated(tip, schemaHistory[i]) {
				return "", nil
			}
			return schemaHistory[i], nil
		}
	}
	return "", nil
}

// rekeyMessage is the subject of the commit that carried dir into the current directory; the
// history of the ref is where a migration is recorded, so a second call finds it done.
func (s *Store) rekeyMessage(dir string) string {
	return fmt.Sprintf("checks: re-key %s into %s", dir, s.schemaDir())
}

func (s *Store) migrated(tip, dir string) bool {
	out, err := s.g.str("log", "--fixed-strings", "--grep="+s.rekeyMessage(dir), "--format=%H", "-n", "1", tip)
	return err == nil && out != ""
}

// ReasonAlreadyKeyed is why a rebuilt verdict is not added: the current directory already holds
// a verdict under its new key, and what is stored there is never overwritten.
const ReasonAlreadyKeyed = "already stored under the new key"

// MigrateKeys carries the verdicts of an older schema directory into the current one, once. It
// is never run implicitly: opening or writing a store of an older layout just starts the current
// directory empty, and the older one stays in the ref untouched. This is the explicit step
// that rebuilds the older verdicts' keys.
//
// The newest older directory present (schemaHistory) is read whole, whatever its schema was,
// and handed to the store's Rebuild, which recomputes each verdict's key from its stored run
// and the repository (never by judging again, never from a transcript). What it returns is
// added to the current directory as one commit on top of the tip, which still holds the older
// directories; every run is kept as history. Results the current directory already holds are
// never overwritten: a verdict whose new key is stored there stays as it is (and is counted as
// skipped), and a run it already has is not added again. The commit says which directory it
// carried (rekeyMessage), which is how a second call finds the work done and writes nothing.
//
// A writer that moves the ref meanwhile (a `run`, another migration) is retried against the new
// tip without rebuilding again: the rebuild reads the older directory, which nothing changes. A
// concurrent migration is waited for (lockMigration) and then found done.
//
// It reports whether it migrated. A ref with no older directory, one with no results, or one
// whose newest older directory is carried already, is left alone (false). With an older
// directory and no Rebuild it fails with ErrMigrationPending.
func (s *Store) MigrateKeys() (MigrationStats, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.migrateKeys()
}

func (s *Store) migrateKeys() (MigrationStats, bool, error) {
	// The common case, nothing older to carry over, takes no lock.
	if tip := s.tip(); tip == "" {
		return MigrationStats{}, false, nil
	} else if dir, err := s.olderDir(tip); err != nil || dir == "" {
		return MigrationStats{}, false, err
	}
	// Another process may be migrating this ref: wait for it, then look again.
	defer s.lockMigration()()
	var (
		lastErr error
		dir     string
		runs    []Run // the older directory's runs
		rebuilt []Run // and what the Rebuild made of them, once
		stats   MigrationStats
	)
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(10+attempt*20) * time.Millisecond)
		}
		_ = s.sync() // an unreachable remote is no reason not to migrate what is local
		tip := s.tip()
		if tip == "" {
			return MigrationStats{}, false, nil
		}
		cur, err := s.olderDir(tip)
		if err != nil || cur == "" {
			return MigrationStats{}, false, err
		}
		if rebuilt == nil || cur != dir {
			dir = cur
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
			if _, _, runs, err = s.readAll(old); err != nil {
				return MigrationStats{}, false, err
			}
			if rebuilt, stats, err = s.rebuild(runs); err != nil {
				return MigrationStats{}, false, err
			}
			if rebuilt == nil {
				rebuilt = []Run{}
			}
		}
		// What the current directory holds already is left as it is.
		sn, err := s.snapshotAt(tip)
		if err != nil {
			return MigrationStats{}, false, err
		}
		have, _, haveRuns, err := s.readAll(sn)
		if err != nil {
			return MigrationStats{}, false, err
		}
		hasRun := map[string]bool{}
		for _, r := range haveRuns {
			hasRun[r.ID] = true
		}
		original := map[string]Run{}
		for _, r := range runs {
			original[r.ID] = r
		}
		st := stats
		st.Reasons = map[string]int{}
		for k, v := range stats.Reasons {
			st.Reasons[k] = v
		}
		var add []Run
		for _, r := range rebuilt {
			if hasRun[r.ID] {
				continue
			}
			was := original[r.ID]
			for i, c := range r.Checks {
				if i >= len(was.Checks) || c.Fingerprint == was.Checks[i].Fingerprint {
					continue // not re-keyed
				}
				if _, there := have[r.CheckKey(c).ID()]; there {
					r.Checks = append([]Check(nil), r.Checks...)
					r.Checks[i] = was.Checks[i] // history only: its old key finds nothing in this directory
					st.Migrated--
					st.Skip(ReasonAlreadyKeyed)
				}
			}
			add = append(add, r)
		}
		files := map[string][]byte{}
		if len(add) > 0 {
			gs := GcStats{Records: len(add), SegsBefore: len(runs)}
			if files, err = s.compactedFiles(add, &gs); err != nil {
				return MigrationStats{}, false, err
			}
			if sn.HasManifest {
				delete(files, "MANIFEST.json") // the directory has its own
			}
		}
		commit, err := s.commit(tip, false, files, fmt.Sprintf("%s, %s", s.rekeyMessage(dir), st))
		if err != nil {
			return MigrationStats{}, false, err
		}
		if _, err := s.g.run(nil, nil, "update-ref", s.opt.Ref, commit, tip); err != nil {
			lastErr = err // a local writer moved the ref meanwhile
			continue
		}
		s.pushErr = s.push()
		return st, true, nil
	}
	return MigrationStats{}, false, fmt.Errorf("checkcache: key migration gave up after %d attempts: %w", maxAttempts, lastErr)
}
