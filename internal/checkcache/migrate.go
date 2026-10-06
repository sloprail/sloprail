package checkcache

import (
	"fmt"
	"strings"
	"time"
)

// MigrateKeys carries the results of the previous schema directory (legacySchemaDir, whose
// records were keyed under sr1: the key held the rule's hash) into the current one, once.
//
// Every record is read from the old directory and recomputed to its current id, which has no
// rule hash: the verdicts of one input reached under several rule definitions collapse to the
// newest (by Newer), and every run is kept as history. Each record's parts come from the
// stored run itself, never guessed. The new directory goes in as one commit on top of the
// tip, which still holds the old directory (a later Gc, which replaces the directory, drops
// it); a writer that moves the ref meanwhile is retried against the new tip, and a concurrent
// migration that got there first is seen as "the new directory exists" and left alone.
//
// It reports whether it migrated. A ref with no old directory, an old one with no results, or
// a current directory already present, is left alone (false), so calling it on every open is
// cheap and a second call writes nothing. Put calls it first: a result written into a fresh
// current directory beside an unmigrated old one would otherwise hide the old one forever.
func (s *Store) MigrateKeys() (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.migrateKeys()
}

func (s *Store) migrateKeys() (bool, error) {
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(10+attempt*20) * time.Millisecond)
			if err := s.sync(); err != nil {
				return false, err
			}
		}
		tip := s.tip()
		if tip == "" {
			return false, nil
		}
		dirs, err := s.g.str("ls-tree", "--name-only", tip)
		if err != nil {
			return false, err
		}
		var haveCur, haveOld bool
		for _, name := range strings.Fields(dirs) {
			haveCur = haveCur || name == s.schemaDir()
			haveOld = haveOld || name == legacySchemaDir
		}
		if haveCur || !haveOld || s.schemaDir() == legacySchemaDir {
			return false, nil
		}
		old, err := s.readSnapshotDir(tip, legacySchemaDir, false)
		if err != nil {
			return false, err
		}
		if len(old.Segs) == 0 {
			return false, nil
		}
		recs, total, runs, err := s.readAll(old)
		if err != nil {
			return false, err
		}
		// Every run is kept as history; encodeSegment indexes the newest record per new id.
		st := GcStats{Records: len(recs), Duplicates: total - len(recs), SegsBefore: len(old.Segs)}
		files, err := s.compactedFiles(runs, &st)
		if err != nil {
			return false, err
		}
		commit, err := s.commit(tip, false, files, fmt.Sprintf("checks: re-key %d results to %s", len(recs), SchemaVersion))
		if err != nil {
			return false, err
		}
		if _, err := s.g.run(nil, nil, "update-ref", s.opt.Ref, commit, tip); err != nil {
			lastErr = err // a local writer moved the ref meanwhile
			continue
		}
		s.pushErr = s.push()
		return true, nil
	}
	return false, fmt.Errorf("checkcache: key migration gave up after %d attempts: %w", maxAttempts, lastErr)
}
