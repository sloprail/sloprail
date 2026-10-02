// Package checkcache is where what a judge concluded is kept, so that no one asks
// it the same question twice.
//
// A result is a FACT about its Key and nothing else: which session, agent, branch or
// commit range produced it is provenance (Result.Prov), never part of a lookup. A
// finished pass with the same key is a cache hit; a stored fail is kept so a reader can
// say why it is red, but it is never a hit for the judge.
//
// This file is the whole contract the engine depends on: Lookup and Put. The in-memory
// implementation below is the reference and what the unit tests use; the git-ref store
// (an orphan branch of content-addressed zstd segments) implements the same interface.
package checkcache

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

// SchemaVersion is the key schema. It is part of every key, so a change to how
// fingerprints are derived never collides with an older result.
const SchemaVersion = "sr1"

// Key is what a result is a fact about.
type Key struct {
	// Rule is the rule's qualified name (<plugin>/file-guard/<name>).
	Rule string `json:"rule"`
	// RuleHash is the hash of the rule's definition (changeset.RuleHash): an edited
	// rubric, script or template never reads an older verdict.
	RuleHash string `json:"ruleHash"`
	// Kind names the check inside the rule: check[1]:judge:./rubric.md.j2.
	Kind string `json:"kind"`
	// Subject is the unit judged. Today the one subject of a rule is "changeset".
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

// Result statuses. Only a pass is a cache hit.
const (
	StatusPass = "pass"
	StatusFail = "fail"
)

// Cite is a citation a result depended on, kept so a reader can see why it passed
// without holding the transcript.
type Cite struct {
	Quote string `json:"quote"`
	Path  string `json:"path,omitempty"`
	Line  int    `json:"line,omitempty"`
}

// Provenance says who produced a result and how. None of it is part of the key.
type Provenance struct {
	Model    string `json:"model,omitempty"`
	Prompt   string `json:"prompt,omitempty"` // sha256 of the rendered prompt
	Response string `json:"resp,omitempty"`   // sha256 of the judge's response
	At       string `json:"at,omitempty"`     // RFC 3339, UTC
	SR       string `json:"sr,omitempty"`     // the sloprail version that ran it
	Session  string `json:"session,omitempty"`
	Agent    string `json:"agent,omitempty"`
}

// Result is one judged key.
type Result struct {
	Key       Key        `json:"key"`
	Status    string     `json:"status"`
	Reasoning string     `json:"reasoning,omitempty"`
	Cites     []Cite     `json:"cites,omitempty"`
	Prov      Provenance `json:"prov"`
}

// Hit reports whether this result may be reused instead of asking the judge again.
func (r Result) Hit() bool { return r.Status == StatusPass }

// Cache is the whole contract: look keys up, put results in.
type Cache interface {
	// Lookup returns the stored result of each key it has, by Key.ID(). A key with no
	// result is simply absent from the map. When several results share a key (two
	// writers), the latest Prov.At wins, a tie broken by the larger Prov.Response.
	Lookup(keys []Key) (map[string]Result, error)
	// Put stores results. Writing a key that already has a result adds to it, never
	// rewrites it; a reader resolves duplicates as Lookup says.
	Put(results []Result) error
}

// Newer reports whether a should be preferred over b when both are results for one key.
func Newer(a, b Result) bool {
	if a.Prov.At != b.Prov.At {
		return a.Prov.At > b.Prov.At
	}
	return a.Prov.Response > b.Prov.Response
}

// Memory is the in-memory Cache.
type Memory struct {
	mu   sync.Mutex
	byID map[string]Result
}

// NewMemory returns an empty in-memory cache.
func NewMemory() *Memory { return &Memory{byID: map[string]Result{}} }

func (m *Memory) Lookup(keys []Key) (map[string]Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]Result, len(keys))
	for _, k := range keys {
		if r, ok := m.byID[k.ID()]; ok {
			out[k.ID()] = r
		}
	}
	return out, nil
}

func (m *Memory) Put(results []Result) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range results {
		id := r.Key.ID()
		if prior, ok := m.byID[id]; ok && !Newer(r, prior) {
			continue
		}
		m.byID[id] = r
	}
	return nil
}
