package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"

	dispatchcore "github.com/sloprail/sloprail/internal/dispatch"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/filemod"
	"github.com/sloprail/sloprail/internal/grounding"
	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/sloprail/sloprail/internal/transcript"
)

// Cited changes: how a citation made at pre-tool reaches the Post event at Stop
// — and grounds there only the change it rode on.
//
// The Post events at Stop come from the tree difference, which knows nothing of
// the commands that made it. So a permitted pre-tool call whose cited change has
// a KNOWN result (an sr-file line the dry run resolved, or a module prediction
// with exact bytes) leaves a PENDING record: the citations, and the file's state
// before and after the change. Nothing is known yet about whether the call runs
// — it may fail, be denied by the user or another hook, or never run — so the
// next hook of the same session (the next pre-tool call, or the Stop) SETTLES
// it: kept when the file now holds exactly what the change produces, dropped
// otherwise. A citation of a change that never landed grounds nothing.
//
// At Stop, a path's kept changes, in order, are laid over its history from the
// session baseline to its current content. Their citations ride on the Post
// event; every stretch between them that no cited change made — an uncited
// write before the first, between two, or after the last — is handed to the
// rule as dispatch.UncitedChange, and a `citation` prerequisite holds only when
// its `when` waives each (see dispatch's checkCitation). So a status flip made
// with a plain edit after a cited ask keeps the ask's citation where `when`
// says a status flip needs none, and a rewrite made with the Write tool after
// a cited change does not ride on that change's citation.

// fileState is a file's content at one moment, or its absence.
type fileState struct {
	Exists  bool   `json:"exists"`
	Content string `json:"content,omitempty"`
}

// citedChange is one change a citation rode on: what it rested on, and the
// file before and after it. At orders changes across the stores a Stop merges
// (the session's own and its sub-agents').
type citedChange struct {
	Cites  []transcript.Citation `json:"cites"`
	Before fileState             `json:"before"`
	After  fileState             `json:"after"`
	At     int64                 `json:"at"`
}

// pendingChange is a cited change a permitted call is about to make, keyed by
// the path a Post event will name (Path) and the file it reads (Abs).
type pendingChange struct {
	Path   string      `json:"path"`
	Abs    string      `json:"abs"`
	Change citedChange `json:"change"`
}

// pendingChanges is, for each Pre file event this call produces that carries
// citations and whose result is KNOWN, the change it would make. An event whose
// result is unknown (a command the dry run could not compute) leaves nothing to
// tie a citation to, so it records nothing: a rule judging at Stop then sees the
// change uncited.
func pendingChanges(events []event.Event, root string, now int64) []pendingChange {
	var out []pendingChange
	for _, e := range events {
		cs := grounding.FromWire(e.Fields[grounding.FieldCitations])
		if len(cs) == 0 {
			continue
		}
		path, _ := e.Fields[filemod.FieldPath].(string)
		oldContent, _ := e.Fields[filemod.FieldOldContent].(string)
		newContent, _ := e.Fields[filemod.FieldNewContent].(string)
		ch := citedChange{Cites: cs, At: now}
		switch e.Kind {
		case filemod.KindPreCreate, filemod.KindPreUpdate:
			if !resultKnown(e) {
				continue
			}
			ch.Before = fileState{Exists: e.Kind == filemod.KindPreUpdate, Content: oldContent}
			ch.After = fileState{Exists: true, Content: newContent}
		case filemod.KindPreDelete:
			ch.Before = fileState{Exists: true, Content: oldContent}
		default:
			continue
		}
		if !ch.Before.Exists {
			ch.Before.Content = ""
		}
		abs := path
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(root, path)
		}
		out = append(out, pendingChange{Path: path, Abs: abs, Change: ch})
	}
	return out
}

// recordPending adds a permitted call's cited changes to the session's pending
// list, for the next hook to settle.
func recordPending(store sessionstate.Store, pending []pendingChange) error {
	if store == nil || len(pending) == 0 {
		return nil
	}
	return swapJSON(store, sessionstate.MetaCitedPending, func(all *[]pendingChange) {
		*all = append(*all, pending...)
	})
}

// settleCitedChanges keeps each pending change whose file now holds what it
// produces, and drops the rest. Run at the start of every hook that reads or
// records cited changes: by then the call that left them has run, or never
// will.
func settleCitedChanges(store sessionstate.Store) error {
	if store == nil {
		return nil
	}
	var landed []pendingChange
	err := swapJSON(store, sessionstate.MetaCitedPending, func(all *[]pendingChange) {
		landed = landedOf(*all)
		*all = nil
	})
	if err != nil || len(landed) == 0 {
		return err
	}
	return swapJSON(store, sessionstate.MetaCitations, func(all *map[string][]citedChange) {
		if *all == nil {
			*all = map[string][]citedChange{}
		}
		for _, p := range landed {
			(*all)[p.Path] = append((*all)[p.Path], p.Change)
		}
	})
}

// landedOf is the pending changes whose file holds exactly what they produce.
func landedOf(pending []pendingChange) []pendingChange {
	var out []pendingChange
	for _, p := range pending {
		if stateOf(p.Abs) == p.Change.After {
			out = append(out, p)
		}
	}
	return out
}

// stateOf is the file at abs as it stands now.
func stateOf(abs string) fileState {
	b, err := os.ReadFile(abs)
	if err != nil {
		return fileState{}
	}
	return fileState{Exists: true, Content: string(b)}
}

// citedChangesIn is every cited change the store kept, by path — plus, for a
// store only read and never settled (a sub-agent's, from its dispatcher's
// Stop), the pending changes that have landed.
func citedChangesIn(store sessionstate.Store, settled bool) map[string][]citedChange {
	all := map[string][]citedChange{}
	if store == nil {
		return all
	}
	if raw, ok, err := store.Meta(sessionstate.MetaCitations); err == nil && ok && raw != "" {
		_ = json.Unmarshal([]byte(raw), &all)
	}
	if !settled {
		var pending []pendingChange
		if raw, ok, err := store.Meta(sessionstate.MetaCitedPending); err == nil && ok && raw != "" {
			_ = json.Unmarshal([]byte(raw), &pending)
		}
		for _, p := range landedOf(pending) {
			all[p.Path] = append(all[p.Path], p.Change)
		}
	}
	return all
}

// attachCitedChanges sets `citations` on each Post file event to those of the
// cited changes that landed on its path this session — the session's own and
// those of the sub-agents it dispatched into the same tree (delegated) — and
// returns, by path, the parts of each change no citation rode on.
func attachCitedChanges(store sessionstate.Store, events []event.Event, delegated map[string][]citedChange) map[string][]dispatchcore.UncitedChange {
	all := citedChangesIn(store, true)
	for path, chs := range delegated {
		all[path] = append(all[path], chs...)
	}
	uncited := map[string][]dispatchcore.UncitedChange{}
	for i, e := range events {
		path, _ := e.Fields[filemod.FieldPath].(string)
		chs := all[path]
		if len(chs) == 0 {
			continue
		}
		var cs []transcript.Citation
		for _, ch := range chs {
			cs = append(cs, ch.Cites...)
		}
		events[i].Fields[grounding.FieldCitations] = grounding.ToWire(dedupe(cs))
		if gaps := uncitedParts(e, chs); len(gaps) > 0 {
			uncited[path] = gaps
		}
	}
	return uncited
}

// uncitedParts walks a Post event's path from its session baseline through its
// cited changes, in order, to its current content, and returns every stretch
// no cited change made.
func uncitedParts(e event.Event, changes []citedChange) []dispatchcore.UncitedChange {
	oldContent, _ := e.Fields[filemod.FieldOldContent].(string)
	newContent, _ := e.Fields[filemod.FieldNewContent].(string)
	state := fileState{Exists: e.Kind != filemod.KindPostCreate, Content: oldContent}
	current := fileState{Exists: e.Kind != filemod.KindPostDelete, Content: newContent}
	if !state.Exists {
		state.Content = ""
	}
	if !current.Exists {
		current.Content = ""
	}
	sorted := append([]citedChange(nil), changes...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].At < sorted[j].At })

	var gaps []dispatchcore.UncitedChange
	gap := func(from, to fileState) {
		if from != to {
			gaps = append(gaps, dispatchcore.UncitedChange{FromExists: from.Exists, From: from.Content, ToExists: to.Exists, To: to.Content})
		}
	}
	for _, ch := range sorted {
		gap(state, ch.Before)
		state = ch.After
	}
	gap(state, current)
	return gaps
}

// delegatedCitedChanges is what the sub-agents dispatched beneath the record at
// path kept, by path, in the stores they keep for the same working tree.
//
// A sub-agent is a session in its own right, so its pre-tool calls record cited
// changes in ITS store; a sub-agent sharing its dispatcher's tree changes files
// the dispatcher's cycle also sees. Each sub-agent's store is found by its own
// identity under this cycle's working directory, and read only if it already
// exists: a sub-agent isolated in its own worktree keeps its store under that
// worktree, and its files are not in this tree either. None of this is ever
// written — a dispatcher never opens a store its sub-agent did not.
func delegatedCitedChanges(p HookPayload, path string) map[string][]citedChange {
	out := map[string][]citedChange{}
	if path == "" {
		return out
	}
	subs, err := transcript.DescendantSubagentPaths(path)
	if err != nil {
		return out
	}
	for _, sub := range subs {
		id, err := stableID(HookPayload{Cwd: p.Cwd, AgentTranscriptPath: sub})
		if err != nil {
			continue
		}
		db, err := sessionDBPath(p.Cwd, id)
		if err != nil {
			continue
		}
		if _, err := os.Stat(db); err != nil {
			continue
		}
		store, err := sessionstate.Open(db)
		if err != nil {
			continue
		}
		for k, chs := range citedChangesIn(store, false) {
			out[k] = append(out[k], chs...)
		}
		store.Close()
	}
	return out
}

// swapJSON applies change to the JSON value stored under key, retrying while
// another writer changes it underneath.
func swapJSON[T any](store sessionstate.Store, key string, change func(*T)) error {
	for attempt := 0; attempt < 5; attempt++ {
		old, _, err := store.Meta(key)
		if err != nil {
			return err
		}
		var v T
		if old != "" {
			_ = json.Unmarshal([]byte(old), &v)
		}
		change(&v)
		raw, err := json.Marshal(v)
		if err != nil {
			return err
		}
		ok, err := store.SwapMeta(key, old, string(raw))
		if err != nil || ok {
			return err
		}
	}
	return errors.New("cited changes not recorded: the record kept changing underneath")
}

// nowNano is the ordering stamp a cited change carries.
func nowNano() int64 { return time.Now().UnixNano() }
