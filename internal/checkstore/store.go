// Package checkstore holds what file-guards concluded about commits, in a10n's check-results
// shape: a CheckRun (one rule evaluated once over one commit range: base_ref, head_ref,
// exit_code, error, metadata) holds Checks (one per subject and kind: status pass | fail |
// skip | error | interrupted, fingerprint, metadata) and each Check holds CheckItems (a
// finding: a file a judge named, a prerequisite of `require:`).
//
// A run is recorded as the engine goes (RecordRun, RecordCheck, FinishRun) and kept in memory;
// Close writes the lot to the cache backend (internal/checkcache) as ONE segment, so a
// `sr check run` is one write and one push however many rules it judged. The backend is the
// only thing that knows where results live.
//
// What makes a check reusable is its key: (rule, rule hash, subject, kind, fingerprint). That
// is a10n's QueryChecks probe, and CachedCheck is a10n's CacheHit: the fingerprint matches and
// the status is pass. Which session, agent, branch or range recorded a run is provenance
// (RunIdentity), never part of a lookup.
package checkstore

import (
	"errors"
	"sync"

	"github.com/sloprail/sloprail/internal/checkcache"
)

// ErrClosed is returned by every method once the store has been closed.
var ErrClosed = errors.New("checkstore: store is closed")

// Store records runs and finds their checks again.
type Store interface {
	// RecordRun starts a run and returns its id. See CheckRun.
	RecordRun(r CheckRun) (string, error)
	// FinishRun marks a run recorded RUNNING (CheckRun.Complete false) complete.
	FinishRun(runID string) error
	// RecordCheck adds one check to a run — replacing the same (subject, kind) of that
	// run — and returns the check's id.
	RecordCheck(runID string, c CheckRecord) (string, error)
	// CachedCheck is a10n's CacheHit: a stored check of the rule at this definition for
	// (subject, kind, fingerprint) whose status is pass. An empty fingerprint never hits.
	CachedCheck(rule, ruleHash, subject, kind, fingerprint string) (CachedCheck, bool, error)
	// LatestCheck is the same lookup without the pass condition: the latest stored pass or
	// fail, for a reader that reports a fail (`sr check verify`).
	LatestCheck(rule, ruleHash, subject, kind, fingerprint string) (CachedCheck, bool, error)
	// Close writes what was recorded to the backend (one segment) and releases the store. A
	// store opened read-only writes nothing.
	Close() error
}

type store struct {
	cache    checkcache.Cache
	readOnly bool

	mu     sync.Mutex
	runs   []*checkcache.Run
	byID   map[string]*checkcache.Run
	closed bool
}

// Open returns a store over a backend. readOnly: RecordRun refuses, Close writes nothing.
func Open(cache checkcache.Cache, readOnly bool) Store {
	return &store{cache: cache, readOnly: readOnly, byID: map[string]*checkcache.Run{}}
}

func (s *store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.readOnly || len(s.runs) == 0 {
		return nil
	}
	runs := make([]checkcache.Run, 0, len(s.runs))
	for _, r := range s.runs {
		runs = append(runs, *r) // a run still RUNNING stays so: it reads as interrupted, never as a pass
	}
	return s.cache.Put(runs)
}
