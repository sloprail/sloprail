package checkstore

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/sloprail/sloprail/internal/checkcache"
)

// Check statuses.
const (
	StatusPass        = checkcache.StatusPass
	StatusFail        = checkcache.StatusFail
	StatusSkip        = checkcache.StatusSkip
	StatusError       = checkcache.StatusError
	StatusInterrupted = checkcache.StatusInterrupted
)

// RunIdentity says whose run it is: provenance, never part of a lookup.
type RunIdentity struct {
	// RepoID is gitrepo.RepoID: the normalized remote and the initial commit (the git common dir
	// without a remote); it survives worktrees, branches and clones.
	RepoID    string
	Branch    string
	SessionID string
	// AgentID is the sub-agent that ran it, "" for the root session itself.
	AgentID string
}

// CheckRun is one rule evaluated once, over one commit range.
type CheckRun struct {
	RunIdentity
	// BatchID groups the runs of one `sr check run`.
	BatchID string
	// CheckID is the rule's qualified name (<plugin>/file-guard/<name>).
	CheckID string
	// RuleHash is the rule's definition hash (changeset.RuleHash): how a run is tied to the
	// rule definition it ran under. Left empty, Metadata["ruleHash"] is read instead.
	RuleHash string
	BaseRef  string
	HeadRef  string
	// BaseTree and HeadTree are the tree ids of BaseRef and HeadRef ("" when unknown).
	BaseTree string
	HeadTree string
	// ExitCode and Error record an ENGINE failure — a git error, a range that could not be
	// computed. Such a run passes nothing: it is never read as an empty range.
	ExitCode int
	Error    string
	// Metadata is {eventKind, baseOrigin, droppedWatermark, ...}; ruleHash may be here too
	// (see RuleHash).
	Metadata map[string]any
	// Complete says the run is already finished when it is recorded: an engine
	// failure, or a range where `match` selected nothing. Any other run is
	// recorded RUNNING and finished with FinishRun once every check is stored.
	//
	// The distinction is what keeps a run that died half-way (a crash, a kill, a
	// judge that never came back) from reading as a pass: a run with no checks
	// yet is indistinguishable from one with nothing to check, and only a
	// finished run is ever a watermark.
	Complete bool
}

// CheckRecord is one check of a run.
type CheckRecord struct {
	Subject string
	// Kind names the check inside the rule: check[0]:script:./x.sh, check[1]:judge:./r.md.j2,
	// require:citation.
	Kind   string
	Status string
	// Fingerprint is the cache key part; only a guard's verdict carries one.
	Fingerprint string
	// FilesPart and SubjectFingerprint are what Fingerprint was made of (checkcache.Check).
	FilesPart          []byte
	SubjectFingerprint string
	Metadata           map[string]any
	Items              []CheckItem
}

// CheckItem is one finding inside a check.
type CheckItem struct {
	Key      string
	Passed   bool
	Metadata map[string]any
}

// CachedCheck is a stored pass or fail found by fingerprint, with the run that recorded it.
type CachedCheck struct {
	Status   string
	Metadata map[string]any
	Run      CheckRun
}

// CheckStatusRow is one check of a rule's latest run — or, for a run that failed
// as an engine, the run itself (Kind empty, Status "error").
type CheckStatusRow struct {
	Rule        string         `json:"rule"`
	Subject     string         `json:"subject"`
	Kind        string         `json:"kind"`
	Status      string         `json:"status"`
	BaseRef     string         `json:"base_ref"`
	HeadRef     string         `json:"head_ref"`
	RunAt       string         `json:"run_at"`
	Fingerprint string         `json:"fingerprint,omitempty"`
	Error       string         `json:"error,omitempty"`
	Metadata    map[string]any `json:"metadata"`
}

// RunRef is one run's range and when it ran (RunAt is a fixed-width UTC stamp, so
// stamps from different stores compare as strings).
type RunRef struct {
	Base, Head, RunAt string
}

// RunRefs is a rule's refused and passed runs, at any rule hash. Failed holds a run
// that is an engine failure or holds a failing check (a failure that went stale is
// not one); Passed holds a COMPLETE run with no failing check and no engine error. A
// run with no recorded range is in neither.
type RunRefs struct {
	Failed, Passed []RunRef
}

func newID(prefix string) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err) // the system's randomness failing is not a state to carry on in
	}
	return prefix + "_" + hex.EncodeToString(b)
}

// stamp is a sortable UTC timestamp.
func stamp() string { return time.Now().UTC().Format("2006-01-02T15:04:05.000000000Z") }

func (s *store) RecordRun(r CheckRun) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writable(); err != nil {
		return "", err
	}
	ruleHash := r.RuleHash
	if h, ok := r.Metadata["ruleHash"].(string); ruleHash == "" && ok {
		ruleHash = h
	}
	run := &checkcache.Run{
		ID: newID("run"), RunAt: stamp(), BatchID: r.BatchID, Rule: r.CheckID, RuleHash: ruleHash,
		BaseRef: r.BaseRef, HeadRef: r.HeadRef, BaseTree: r.BaseTree, HeadTree: r.HeadTree, ExitCode: r.ExitCode, Error: r.Error, Complete: r.Complete,
		Metadata: r.Metadata, RepoID: r.RepoID, Branch: r.Branch, SessionID: r.SessionID, AgentID: r.AgentID,
	}
	s.runs = append(s.runs, run)
	s.byID[run.ID] = run
	return run.ID, nil
}

// FinishRun marks a run complete: every check it was going to run is stored.
func (s *store) FinishRun(runID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writable(); err != nil {
		return err
	}
	run, ok := s.byID[runID]
	if !ok {
		return fmt.Errorf("checkstore: finish run: no run %q", runID)
	}
	run.Complete = true
	return nil
}

func validStatus(st string) bool {
	switch st {
	case StatusPass, StatusFail, StatusSkip, StatusError, StatusInterrupted:
		return true
	}
	return false
}

func (s *store) RecordCheck(runID string, c CheckRecord) (string, error) {
	if !validStatus(c.Status) {
		return "", fmt.Errorf("checkstore: %q is not a check status", c.Status)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writable(); err != nil {
		return "", err
	}
	run, ok := s.byID[runID]
	if !ok {
		return "", fmt.Errorf("checkstore: record check %q: no run %q", c.Kind, runID)
	}
	check := checkcache.Check{Subject: c.Subject, Kind: c.Kind, Status: c.Status, Fingerprint: c.Fingerprint, FilesPart: c.FilesPart, SubjectFingerprint: c.SubjectFingerprint, Metadata: c.Metadata}
	for _, it := range c.Items {
		check.Items = append(check.Items, checkcache.Item{Key: it.Key, Passed: it.Passed, Metadata: it.Metadata})
	}
	for i, prev := range run.Checks {
		if prev.Subject == c.Subject && prev.Kind == c.Kind {
			run.Checks[i] = check
			return run.ID + "/" + c.Subject + "/" + c.Kind, nil
		}
	}
	run.Checks = append(run.Checks, check)
	return run.ID + "/" + c.Subject + "/" + c.Kind, nil
}

// CachedCheck is a10n's CacheHit on (rule, subject, kind, fingerprint), extended to
// read a fail as well as a pass: a fail is terminal, and replaying it is what keeps a judge
// from being asked again about input that has not changed.
func (s *store) CachedCheck(rule, subject, kind, fingerprint string) (CachedCheck, bool, error) {
	if fingerprint == "" {
		return CachedCheck{}, false, nil
	}
	if err := s.live(); err != nil {
		return CachedCheck{}, false, err
	}
	key := checkcache.Key{Rule: rule, Kind: kind, Subject: subject, Fingerprint: fingerprint}
	// What this process recorded first: it is newer than anything the backend holds.
	s.mu.Lock()
	var best *checkcache.Found
	for _, run := range s.runs {
		for _, c := range run.Checks {
			if !checkcache.Findable(c) || run.CheckKey(c) != key {
				continue
			}
			f := checkcache.Found{Run: *run, Check: c}
			if best == nil || checkcache.Newer(f, *best) {
				best = &f
			}
		}
	}
	s.mu.Unlock()
	if best == nil {
		found, err := s.cache.Lookup([]checkcache.Key{key})
		if err != nil {
			return CachedCheck{}, false, fmt.Errorf("checkstore: result lookup: %w", err)
		}
		f, ok := found[key.ID()]
		if !ok {
			return CachedCheck{}, false, nil
		}
		best = &f
	}
	if best.Check.Status != StatusPass && best.Check.Status != StatusFail {
		return CachedCheck{}, false, nil // a fail resolved as stale: its input is not in any range now
	}
	return CachedCheck{Status: best.Check.Status, Metadata: best.Check.Metadata, Run: CheckRun{
		CheckID: best.Run.Rule, RuleHash: best.Run.RuleHash, BaseRef: best.Run.BaseRef, HeadRef: best.Run.HeadRef,
		RunIdentity: RunIdentity{RepoID: best.Run.RepoID, Branch: best.Run.Branch, SessionID: best.Run.SessionID, AgentID: best.Run.AgentID},
	}}, true, nil
}

// CachedByTrees reads, when a key misses, the verdict of a COMPLETE run of the same rule
// (at whatever definition) whose base and head TREES equal these: identical trees are an identical
// net change, whichever commits (a squash of a judged branch) carry it. The newest such pass
// or fail wins; a run without trees, or with an engine error, never matches.
func (s *store) CachedByTrees(rule, subject, kind, baseTree, headTree string) (CachedCheck, bool, error) {
	if baseTree == "" || headTree == "" {
		return CachedCheck{}, false, nil
	}
	if err := s.live(); err != nil {
		return CachedCheck{}, false, err
	}
	var best *checkcache.Found
	consider := func(run checkcache.Run) {
		if !run.Complete || run.ExitCode != 0 || run.Error != "" || run.Rule != rule ||
			run.BaseTree != baseTree || run.HeadTree != headTree {
			return
		}
		for _, c := range run.Checks {
			if c.Subject != subject || c.Kind != kind || c.Fingerprint == "" || (c.Status != StatusPass && c.Status != StatusFail) {
				continue
			}
			f := checkcache.Found{Run: run, Check: c}
			if best == nil || checkcache.Newer(f, *best) {
				best = &f
			}
		}
	}
	s.mu.Lock()
	for _, run := range s.runs {
		consider(*run)
	}
	s.mu.Unlock()
	runs, err := s.cache.Runs()
	if err != nil {
		return CachedCheck{}, false, fmt.Errorf("checkstore: result lookup: %w", err)
	}
	for _, run := range runs {
		consider(run)
	}
	if best == nil {
		return CachedCheck{}, false, nil
	}
	return CachedCheck{Status: best.Check.Status, Metadata: best.Check.Metadata, Run: CheckRun{
		CheckID: best.Run.Rule, RuleHash: best.Run.RuleHash, BaseRef: best.Run.BaseRef, HeadRef: best.Run.HeadRef,
		BaseTree: best.Run.BaseTree, HeadTree: best.Run.HeadTree,
		RunIdentity: RunIdentity{RepoID: best.Run.RepoID, Branch: best.Run.Branch, SessionID: best.Run.SessionID, AgentID: best.Run.AgentID},
	}}, true, nil
}

// writable is the guard of every method that records: a closed store is ErrClosed, a
// read-only one refuses. The caller holds s.mu.
func (s *store) writable() error {
	if s.closed {
		return ErrClosed
	}
	if s.readOnly {
		return errors.New("checkstore: this store is read-only")
	}
	return nil
}

// live is the guard of every method that reads: ErrClosed once the store is closed.
func (s *store) live() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	return nil
}
