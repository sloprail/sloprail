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
	// RepoID is the repository's root-commit SHA: it survives worktrees and branches.
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
	// RuleHash is the rule's definition hash (changeset.RuleHash).
	RuleHash string
	BaseRef  string
	HeadRef  string
	// ExitCode and Error record an ENGINE failure — a git error, a range that could not be
	// computed. Such a run passes nothing: it is never read as an empty range.
	ExitCode int
	Error    string
	Metadata map[string]any
	// Complete says the run is already finished when it is recorded (an engine failure, or a
	// range where `match` selected nothing). Any other run is recorded RUNNING and finished
	// with FinishRun once every check is stored: a run that died half-way never reads as a pass.
	Complete bool
}

// CheckRecord is one check of a run.
type CheckRecord struct {
	Subject string
	// Kind names the check inside the rule: check[0]:script:./x.sh, check[1]:judge:./r.md.j2,
	// require:citation.
	Kind   string
	Status string
	// Fingerprint is the cache key part; "" is never cached.
	Fingerprint string
	Metadata    map[string]any
	Items       []CheckItem
}

// CheckItem is one finding inside a check.
type CheckItem struct {
	Key      string
	Passed   bool
	Metadata map[string]any
}

// CachedCheck is a stored check found by key.
type CachedCheck struct {
	Status   string
	Metadata map[string]any
	Run      CheckRun
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
	if s.closed {
		return "", ErrClosed
	}
	if s.readOnly {
		return "", errors.New("checkstore: this store is read-only")
	}
	run := &checkcache.Run{
		ID: newID("run"), RunAt: stamp(), BatchID: r.BatchID, Rule: r.CheckID, RuleHash: r.RuleHash,
		BaseRef: r.BaseRef, HeadRef: r.HeadRef, ExitCode: r.ExitCode, Error: r.Error, Complete: r.Complete,
		Metadata: r.Metadata, RepoID: r.RepoID, Branch: r.Branch, SessionID: r.SessionID, AgentID: r.AgentID,
	}
	s.runs = append(s.runs, run)
	s.byID[run.ID] = run
	return run.ID, nil
}

func (s *store) FinishRun(runID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	run, ok := s.byID[runID]
	if !ok {
		return "", fmt.Errorf("checkstore: record check %q: no run %q", c.Kind, runID)
	}
	check := checkcache.Check{Subject: c.Subject, Kind: c.Kind, Status: c.Status, Fingerprint: c.Fingerprint, Metadata: c.Metadata}
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

func (s *store) latest(rule, ruleHash, subject, kind, fingerprint string) (CachedCheck, bool, error) {
	if fingerprint == "" {
		return CachedCheck{}, false, nil
	}
	key := checkcache.Key{Rule: rule, RuleHash: ruleHash, Kind: kind, Subject: subject, Fingerprint: fingerprint}
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
	return CachedCheck{Status: best.Check.Status, Metadata: best.Check.Metadata, Run: CheckRun{
		CheckID: best.Run.Rule, RuleHash: best.Run.RuleHash, BaseRef: best.Run.BaseRef, HeadRef: best.Run.HeadRef,
		RunIdentity: RunIdentity{RepoID: best.Run.RepoID, Branch: best.Run.Branch, SessionID: best.Run.SessionID, AgentID: best.Run.AgentID},
	}}, true, nil
}

func (s *store) LatestCheck(rule, ruleHash, subject, kind, fingerprint string) (CachedCheck, bool, error) {
	return s.latest(rule, ruleHash, subject, kind, fingerprint)
}

func (s *store) CachedCheck(rule, ruleHash, subject, kind, fingerprint string) (CachedCheck, bool, error) {
	c, ok, err := s.latest(rule, ruleHash, subject, kind, fingerprint)
	if err != nil || !ok || c.Status != StatusPass {
		return CachedCheck{}, false, err
	}
	return c, true, nil
}
