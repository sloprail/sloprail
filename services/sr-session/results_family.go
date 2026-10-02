package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/sloprail/sloprail/internal/checkstore"
	"github.com/sloprail/sloprail/internal/sessionpath"
	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/sloprail/sloprail/internal/transcript"
)

// ONE CHECK-RESULTS DATABASE PER REPOSITORY, SCOPED TO THE SESSION FAMILY.
//
// What a file-guard concluded is keyed by the rule's version and a commit range: a statement
// about bytes, not about any one agent's working tree. So the root session's checks.db is the
// family's: the root and every sub-agent it dispatches write their runs there, each run
// carrying the agent_id that ran it, and everything that decides what a rule passed or refused
// (the range a rule starts from, whether a tip is already settled, the merge gate) reads that
// one store. A sub-agent's pass on a commit under a rule version is reused by the root, and its
// refusal is the root's to see.
//
// The per-agent state.db is a different thing and stays per agent (sessionpath.StateDB says
// why: it holds working-tree state, which is one agent's own).
//
// Sub-agents of an older engine kept their results in databases of their own: the root imports
// them into the family's (subagentLegacy), and leaves the old files where they are.

// familyResults is the family's check results with each rule's passed heads remembered for the
// length of one evaluation (any write drops it): the query every range and every owed tip asks
// again.
type familyResults struct {
	checkstore.Store

	mu    sync.Mutex
	heads map[string][]string
}

func newFamilyResults(s checkstore.Store) *familyResults {
	return &familyResults{Store: s, heads: map[string][]string{}}
}

// PendingLegacy forwards the opener's list of old files not yet imported in full.
func (f *familyResults) PendingLegacy() []checkstore.Legacy {
	if p, ok := f.Store.(checkstore.Pending); ok {
		return p.PendingLegacy()
	}
	return nil
}

// SiblingRunRefs is the other session families' runs of the same working tree, when the store
// under this one is the repository's (an unshared store has none).
func (f *familyResults) SiblingRunRefs(rule, folder string) (checkstore.RunRefs, error) {
	if s, ok := f.Store.(checkstore.SiblingRefs); ok {
		return s.SiblingRunRefs(rule, folder)
	}
	return checkstore.RunRefs{}, nil
}

// PassedHeads is the rule's passed heads, any agent's, remembered until something is written.
func (f *familyResults) PassedHeads(rule string) ([]string, error) {
	f.mu.Lock()
	if h, ok := f.heads[rule]; ok {
		f.mu.Unlock()
		return h, nil
	}
	f.mu.Unlock()
	heads, err := f.Store.PassedHeads(rule)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.heads[rule] = heads
	f.mu.Unlock()
	return heads, nil
}

func (f *familyResults) forget() {
	f.mu.Lock()
	f.heads = map[string][]string{}
	f.mu.Unlock()
}

func (f *familyResults) RecordRun(r checkstore.CheckRun) (string, error) {
	f.forget()
	return f.Store.RecordRun(r)
}

func (f *familyResults) FinishRun(runID string) error {
	f.forget()
	return f.Store.FinishRun(runID)
}

func (f *familyResults) RecordCheck(runID string, c checkstore.CheckRecord) (string, error) {
	f.forget()
	return f.Store.RecordCheck(runID, c)
}

func (f *familyResults) ResolveStale(rule, ruleHash, liveRunID string) (int, error) {
	f.forget()
	return f.Store.ResolveStale(rule, ruleHash, liveRunID)
}

// subagentLegacy is the check-results databases an older engine kept in each sub-agent's own
// directory, tagged with the agent, for the repository's database to import (checkstore
// .ImportLegacy: once per change of the old file, the old files left as they are). A
// sub-agent's database is found two ways: by the record of each agent dispatched under the
// root's record (its own session, in the tree it began in), and by the folders registered for
// the family's agents. Best effort: whatever cannot be read is left, and costs a judgement made
// again.
func subagentLegacy(p HookPayload, rs rootSession) []checkstore.Legacy {
	if _, err := os.Stat(rs.Path); err != nil {
		return nil
	}
	reg, err := sessionstate.Open(rs.Path)
	if err != nil {
		return nil
	}
	defer reg.Close()
	candidates := map[string]checkstore.Legacy{}
	if record, err := p.sessionRecord(); err == nil && record != "" {
		recs, _ := filepath.Glob(filepath.Join(strings.TrimSuffix(record, ".jsonl"), "subagents", "agent-*.jsonl"))
		for _, rec := range recs {
			agent := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(rec), "agent-"), ".jsonl")
			cwd, err := transcript.StartCwd(rec)
			if err != nil || cwd == "" {
				continue
			}
			id, err := sessionpath.StableIdentity(rec, cwd)
			if err != nil {
				continue
			}
			if path, err := sessionpath.ChecksDB(cwd, id.ID); err == nil {
				candidates[path] = checkstore.Legacy{Path: path, Family: rs.ID, Agent: agent, Folder: sessionpath.WorkspaceAnchor(cwd)}
			}
		}
	}
	if folders, err := reg.Folders(rs.ID); err == nil {
		for _, f := range folders {
			if f.AgentID == "" || f.Role == sessionstate.FolderRoot {
				continue
			}
			probe, err := sessionpath.ChecksDB(f.Path, "x")
			if err != nil {
				continue
			}
			paths, _ := filepath.Glob(filepath.Join(filepath.Dir(filepath.Dir(probe)), "*", "checks.db"))
			for _, path := range paths {
				if _, known := candidates[path]; !known {
					candidates[path] = checkstore.Legacy{Path: path, Family: rs.ID, Agent: f.AgentID, Folder: sessionpath.WorkspaceAnchor(f.Path)}
				}
			}
		}
	}
	out := make([]checkstore.Legacy, 0, len(candidates))
	for _, l := range candidates {
		out = append(out, l)
	}
	return out
}
