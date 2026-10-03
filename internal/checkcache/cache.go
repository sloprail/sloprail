// Package checkcache is where what file-guards concluded about commits is kept, so that no
// one asks a judge the same question twice.
//
// The unit it stores is a RUN: one rule evaluated once over one commit range, with its
// Checks (one per subject and kind, each with its Items) — a10n's check-results shape, which
// internal/checkstore speaks. What makes a check reusable is its KEY and nothing else: which
// session, agent, branch or range recorded it is provenance on the run, never part of a
// lookup. A finished pass with the same key is a cache hit; a stored fail is kept so a reader
// can say why it is red.
//
// This file is the whole contract the engine depends on: Lookup and Put. The in-memory
// implementation below is the reference and what the unit tests use; the git-ref store (an
// orphan branch of content-addressed zstd segments) implements the same interface.
package checkcache

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"sync"
)

// SchemaVersion is the key schema. It is part of every key, so a change to how
// fingerprints are derived never collides with an older result.
const SchemaVersion = "sr1"

// Key is what a check's result is a fact about.
type Key struct {
	// Rule is the rule's qualified name (<plugin>/file-guard/<name>).
	Rule string `json:"rule"`
	// RuleHash is the hash of the rule's definition (changeset.RuleHash): an edited
	// rubric, script or template never reads an older verdict.
	RuleHash string `json:"ruleHash"`
	// Kind names the check inside the rule: check[1]:judge:./rubric.md.j2.
	Kind string `json:"kind"`
	// Subject is the unit judged. Today the one subject of a rule's checks is "changeset".
	Subject string `json:"subject"`
	// Fingerprint covers EVERY input of the check — the subject's content, the model,
	// what `prepare` supplied — and never a commit SHA.
	Fingerprint string `json:"fingerprint"`
}

// ID is the key's stable identity: a hex sha256 of its parts, each separated so no two
// keys can be re-cut into one another.
func (k Key) ID() string {
	h := sha256.New()
	for _, part := range []string{SchemaVersion, k.Rule, k.RuleHash, k.Kind, k.Subject, k.Fingerprint} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Check statuses. Only a pass is a cache hit.
const (
	StatusPass        = "pass"
	StatusFail        = "fail"
	StatusSkip        = "skip"
	StatusError       = "error"
	StatusInterrupted = "interrupted"
)

// Item is one finding inside a check (a file a judge named, a citation a prerequisite saw).
type Item struct {
	Key      string         `json:"key,omitempty"`
	Passed   bool           `json:"passed"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// Check is one (subject, kind) of a run.
type Check struct {
	Subject string `json:"subject"`
	Kind    string `json:"kind"`
	Status  string `json:"status"`
	// Fingerprint is the cache key part; only a guard's verdict carries one (its steps inside
	// it do not), and what has none is not findable.
	Fingerprint string         `json:"fingerprint,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	Items       []Item         `json:"items,omitempty"`
}

// Run is one rule evaluated once over one commit range.
type Run struct {
	ID      string `json:"id"`
	RunAt   string `json:"run_at"` // fixed-width UTC, so stamps compare as strings
	BatchID string `json:"batch,omitempty"`
	// Rule is the rule's qualified name; RuleHash its definition's hash.
	Rule     string `json:"rule"`
	RuleHash string `json:"ruleHash"`
	BaseRef  string `json:"base_ref"`
	HeadRef  string `json:"head_ref"`
	// BaseTree and HeadTree are the tree ids of BaseRef and HeadRef when the run was made
	// (absent on a run recorded before they existed): equal trees are an equal net change,
	// so a range over the same two trees can read this run's verdict (Store.CachedByTrees).
	BaseTree string `json:"base_tree,omitempty"`
	HeadTree string `json:"head_tree,omitempty"`
	// ExitCode and Error record an ENGINE failure (git, a range that could not be read):
	// such a run passes nothing.
	ExitCode int            `json:"exit_code,omitempty"`
	Error    string         `json:"error,omitempty"`
	Complete bool           `json:"complete"` // every check it was going to run is stored
	Metadata map[string]any `json:"metadata,omitempty"`
	// Provenance: who ran it. Never part of a key.
	RepoID    string  `json:"repo_id,omitempty"`
	Branch    string  `json:"branch,omitempty"`
	SessionID string  `json:"session_id,omitempty"`
	AgentID   string  `json:"agent_id,omitempty"`
	Checks    []Check `json:"checks"`
}

// CheckKey is the key of one check of a run.
func (r Run) CheckKey(c Check) Key {
	return Key{Rule: r.Rule, RuleHash: r.RuleHash, Kind: c.Kind, Subject: c.Subject, Fingerprint: c.Fingerprint}
}

// Found is the stored check for a key, with the run it was recorded in.
type Found struct {
	Run   Run
	Check Check
}

// Newer reports whether a should be preferred over b when both hold a result for one key:
// the latest run wins, a tie broken deterministically.
func Newer(a, b Found) bool {
	if a.Run.RunAt != b.Run.RunAt {
		return a.Run.RunAt > b.Run.RunAt
	}
	return a.Run.ID > b.Run.ID
}

// Cache is the whole contract: look keys up, put runs in.
type Cache interface {
	// Lookup returns, for each key it has a pass or fail for, the check and its run, by
	// Key.ID(). A key with no result is absent. When several runs hold the key (two
	// writers), the latest wins. Only checks with a fingerprint are findable.
	Lookup(keys []Key) (map[string]Found, error)
	// Put stores runs. Writing a key that already has a result adds to it, never rewrites
	// it; a reader resolves duplicates as Lookup says.
	Put(runs []Run) error
	// Runs returns every run stored, each once (a run put again replaces the earlier copy),
	// newest first: the history a reader that lists (not looks up) works over. A run with no
	// findable check is a run too.
	Runs() ([]Run, error)
}

// Findable is whether a check can be looked up: it has a fingerprint and finished as a
// pass or a fail — or is a fail that was resolved as stale (a skip carrying metadata
// "staleFrom"), which is found so that, being the newest result of its key, it supersedes
// the fail it resolves: a reader sees a skip, and a skip is no hit.
func Findable(c Check) bool {
	if c.Fingerprint == "" {
		return false
	}
	if c.Status == StatusSkip {
		_, stale := c.Metadata["staleFrom"]
		return stale
	}
	return c.Status == StatusPass || c.Status == StatusFail
}

// Memory is the in-memory Cache.
type Memory struct {
	mu   sync.Mutex
	byID map[string]Found
	runs []Run
}

// NewMemory returns an empty in-memory cache.
func NewMemory() *Memory { return &Memory{byID: map[string]Found{}} }

func (m *Memory) Lookup(keys []Key) (map[string]Found, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]Found, len(keys))
	for _, k := range keys {
		if f, ok := m.byID[k.ID()]; ok {
			out[k.ID()] = f
		}
	}
	return out, nil
}

func (m *Memory) Put(runs []Run) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range runs {
		m.runs = append(m.runs, r)
		for _, c := range r.Checks {
			if !Findable(c) {
				continue
			}
			id := r.CheckKey(c).ID()
			f := Found{Run: r, Check: c}
			if prior, ok := m.byID[id]; ok && !Newer(f, prior) {
				continue
			}
			m.byID[id] = f
		}
	}
	return nil
}

func (m *Memory) Runs() ([]Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return newestFirst(m.runs), nil
}

// newestFirst keeps the last copy of each run id and sorts by RunAt, newest first.
func newestFirst(runs []Run) []Run {
	last := make(map[string]int, len(runs))
	for i, r := range runs {
		last[r.ID] = i
	}
	out := make([]Run, 0, len(last))
	for i, r := range runs {
		if last[r.ID] == i {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].RunAt > out[j].RunAt })
	return out
}
