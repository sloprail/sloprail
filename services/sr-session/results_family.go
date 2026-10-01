package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/checkstore"
	"github.com/sloprail/sloprail/internal/sessionpath"
	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/sloprail/sloprail/internal/transcript"
)

// ONE CHECK-RESULTS DATABASE PER SESSION FAMILY.
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
// them into the family's, once (importFamily), and leaves the old files where they are.

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

// familyChecksPath is where the family's check results are: beside the ROOT session's state
// database, whichever agent is asking.
func familyChecksPath(rs rootSession) string {
	return filepath.Join(filepath.Dir(rs.Path), "checks.db")
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

func importedKey(path string) string { return "checks_imported:" + path }

// importFamily brings the check results an older engine kept in each sub-agent's own database
// into the family's, once per database (remembered in the root's state), tagged with the agent.
// A sub-agent's database is found two ways: by the record of each agent dispatched under the
// root's record (its own session, in the tree it began in), and by the folders registered for
// the family's agents. Best effort: whatever cannot be read is left, and costs a judgement made
// again.
func importFamily(cmd *cobra.Command, store checkstore.Store, p HookPayload, rs rootSession) {
	if _, err := os.Stat(rs.Path); err != nil {
		return
	}
	reg, err := sessionstate.Open(rs.Path)
	if err != nil {
		return
	}
	defer reg.Close()
	own := store.Path()
	candidates := map[string]string{} // checks.db path -> agent id
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
				candidates[path] = agent
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
					candidates[path] = f.AgentID
				}
			}
		}
	}
	for path, agent := range candidates {
		if path == own {
			continue
		}
		if _, err := os.Stat(path); err != nil {
			continue
		}
		if done, _, _ := reg.Meta(importedKey(path)); done == "1" {
			continue
		}
		if _, err := checkstore.Import(store, path, agent); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: a sub-agent's check results were not imported (%s): %v\n", path, err)
			continue
		}
		_ = reg.SetMeta(importedKey(path), "1")
	}
}
