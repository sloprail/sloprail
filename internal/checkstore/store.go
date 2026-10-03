// Package checkstore holds what file-guards concluded about commits, in a10n's check-results
// shape: a CheckRun (one rule evaluated once over one commit range: base_ref, head_ref,
// exit_code, error, metadata) holds Checks (one per subject and kind: status pass | fail |
// skip | error | interrupted, fingerprint, metadata) and each Check holds CheckItems (a
// finding: a file a judge named, a prerequisite of `require:`). See schema.sql for what each
// row means here.
//
// A file-guard is commit-based, so only file-guards write here. Gates and contexts are about
// events and leave no rows; the session's own memory (baseline, guardrail state) stays in
// sessionstate.
//
// A run is recorded as the engine goes (RecordRun, RecordCheck, FinishRun) and kept in memory;
// Close writes the lot to the cache backend (internal/checkcache) as ONE segment, so a
// `sr check run` is one write and one push however many rules it judged. The backend is the
// only thing that knows where results live, and one cache serves the whole repository: a
// check result is a statement about a rule, a subject and an input, whichever session, agent
// or worktree recorded it. The identity on every run (repo, branch, session, agent) is
// provenance, never part of a lookup.
//
// What makes a check reusable is its key: (rule, rule hash, subject, kind, fingerprint). That
// is a10n's QueryChecks probe, and CachedCheck is a10n's CacheHit, extended to read a fail
// as well as a pass. The queries over runs (PassedHeads, RunRefs, ResolveStale, CheckStatus,
// Query) read the cache's run history as the check_runs, checks and check_items tables of
// schema.sql, built in memory for the call: nothing is ever written to a database.
package checkstore

import (
	"database/sql"
	"errors"
	"sync"

	"github.com/sloprail/sloprail/internal/checkcache"
)

// ErrClosed is returned by every method once the store has been closed.
var ErrClosed = errors.New("checkstore: store is closed")

// Store is the check results of a repository.
type Store interface {
	// RecordRun stores one rule's evaluation of one commit range and returns the
	// run's id. See CheckRun.
	RecordRun(r CheckRun) (string, error)
	// FinishRun marks a run recorded RUNNING (CheckRun.Complete false) complete.
	// Only a complete run can be a watermark.
	FinishRun(runID string) error
	// RecordCheck stores one check of a run — replacing the same (subject, kind)
	// of that run — with its findings, and returns the check's id.
	RecordCheck(runID string, c CheckRecord) (string, error)
	// CachedCheck finds a stored pass or fail of the rule at this definition (ruleHash) for
	// (subject, kind, fingerprint): the most recent one. A fail is returned like a pass — it
	// is terminal and is replayed, never re-judged until the input changes. An empty
	// fingerprint (a script) never hits. The rule and its hash are part of the backend's key.
	CachedCheck(rule, ruleHash, subject, kind, fingerprint string) (CachedCheck, bool, error)
	// CachedByTrees finds, for a key that missed, a stored pass or fail of the same rule
	// definition and subject from a complete run whose base and head trees equal these.
	CachedByTrees(rule, ruleHash, subject, kind, baseTree, headTree string) (CachedCheck, bool, error)
	// ResolveStale does nothing and returns 0: a stored fail is a fact about content that
	// other branches share, so it is never marked stale (see the implementation).
	ResolveStale(rule, ruleHash, liveRunID string) (int, error)
	// PassedHeads lists, newest first, the head_ref of each of the rule's runs, at any
	// rule hash, that passed: no engine error and no failing check. The
	// caller picks the first still reachable — that is the rule's watermark.
	PassedHeads(rule string) ([]string, error)
	// EffectiveRuns is the input of a10n's GetEffectiveBase, newest first: the range (base and
	// head) of each evaluation of the rule AT THIS DEFINITION (ruleHash) that passed as a whole.
	// One evaluation is every run of one batch over one head (a guard has one run per
	// subject), and it passed when each of them is complete, no engine failure and holds no
	// failing, erroring or interrupted check: one subject's FAIL keeps the evaluation from
	// advancing the base. The caller chains them: an evaluation covers its base..head, so it
	// advances a base only when that base lies inside it.
	EffectiveRuns(rule, ruleHash string) ([]RunRef, error)
	// RunRefs lists, for one rule, the runs that were refused and the runs that
	// passed, each with the commit range it judged and when it ran. See RunRefs.
	RunRefs(rule string) (RunRefs, error)
	// CheckStatus lists each rule's latest run and its checks. failingOnly keeps
	// only what is failing: a failed engine run, or a fail/error/interrupted check.
	// A non-empty rule keeps only that rule.
	CheckStatus(failingOnly bool, rule string) ([]CheckStatusRow, error)
	// Query runs a read-only SELECT over the check tables and returns its rows.
	Query(sql string) ([]map[string]any, error)
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

	viewMu   sync.Mutex
	viewDB   *sql.DB // a read-only store's one view, built on first use
	viewDone func()
}

// Open returns a store over a backend. readOnly: RecordRun refuses, Close writes nothing.
func Open(cache checkcache.Cache, readOnly bool) Store {
	return &store{cache: cache, readOnly: readOnly, byID: map[string]*checkcache.Run{}}
}

func (s *store) Close() error {
	// viewMu is taken before mu (as readView does), never inside it.
	s.viewMu.Lock()
	if s.viewDone != nil {
		s.viewDone()
		s.viewDB, s.viewDone = nil, nil
	}
	s.viewMu.Unlock()
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
