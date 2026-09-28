package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/sloprail/sloprail/internal/commandmod"
	"github.com/sloprail/sloprail/internal/declaration"
	dispatchcore "github.com/sloprail/sloprail/internal/dispatch"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/filemod"
	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/grounding"
	"github.com/sloprail/sloprail/internal/guardrail"
	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/sloprail/sloprail/internal/transcript"
)

// Cited changes: how a citation made at pre-tool reaches the Post event at Stop
// — and grounds there only the change it rode on.
//
// The Post events at Stop come from the tree difference against the session
// baseline, which knows nothing of the commands that made it. So each path
// keeps a HISTORY of points, which Stop lays over that difference:
//
//   - a CITED point for each cited change that landed. A permitted pre-tool call
//     whose cited change has a KNOWN result (an sr-file line the dry run
//     resolved, or a prediction with exact bytes) leaves a PENDING point; the
//     next hook of the same session settles it — kept when the file holds
//     exactly what the change produces, dropped when the call failed, was
//     denied or never ran. A cited `sr-file write` states the whole file
//     (Whole), so it grounds everything before it.
//   - a FOREIGN point at the first hook of each of the agent's cycles, for each
//     file that is not as the agent left it at its last Stop — the user edited
//     it between turns, switched branches, or (at the session's first hook) it
//     was already dirty. Changes the agent never made are never charged to it.
//
// At Stop each path's history goes to its rules (dispatch.FileHistory), and a
// `citation` prerequisite holds only when every part of the change the agent
// made that no cited change in its pools made is one its `when` waives.
//
// States are kept by content hash; each content is stored once, under its own
// key (contentKey), so the history itself stays small.

// historyState is a file's content at one moment, by hash, or its absence.
type historyState struct {
	Exists bool   `json:"exists"`
	Hash   string `json:"hash,omitempty"`
}

// historyPoint is one step of a path's history. See dispatch.HistoryPoint.
type historyPoint struct {
	Foreign bool                  `json:"foreign,omitempty"`
	From    *historyState         `json:"from,omitempty"`
	FromAt  int64                 `json:"fromAt,omitempty"`
	Cites   []transcript.Citation `json:"cites,omitempty"`
	Whole   bool                  `json:"whole,omitempty"`
	Before  historyState          `json:"before"`
	After   historyState          `json:"after"`
	At      int64                 `json:"at"`
}

// pendingChange is a cited change a permitted call is about to make, keyed by
// the path a Post event will name (Path) and the file it reads (Abs).
type pendingChange struct {
	Path  string       `json:"path"`
	Abs   string       `json:"abs"`
	Point historyPoint `json:"point"`
}

// cycleMeta is where the session's current cycle stands: State is "" before
// the session's first hook, "open" once a cycle's first hook ran, "ended" once
// its Stop ran; End is how the agent left each file it had changed, at that
// Stop. Outlives is set, for the rest of the session, once the agent started
// work that can outlive the call that started it (see launchesBackground):
// from then on, what changes between its Stop and its next hook may be its own.
type cycleMeta struct {
	State     string                  `json:"state,omitempty"`
	StartedAt int64                   `json:"startedAt,omitempty"`
	EndedAt   int64                   `json:"endedAt,omitempty"`
	End       map[string]historyState `json:"end,omitempty"`
	Outlives  bool                    `json:"outlives,omitempty"`
}

// citedPath reports whether a rule requiring a citation cares about the file at
// path, as it now stands. Only such files are snapshotted: a file no citation
// rule selects needs no history, and its content need not be stored. nil
// selects every path.
type citedPath func(path string, exists bool, content string) bool

// snapshotMax bounds the files a foreign point is recorded for: a file this
// large is not one a citation rule is about, and reading every dirty file at
// every cycle start must stay cheap.
const snapshotMax = 1 << 20

const contentKeyPrefix = "cited_content:"

func contentKey(hash string) string { return contentKeyPrefix + hash }

func hashOf(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// putState stores content once under its hash and returns the state naming it.
func putState(store sessionstate.Store, exists bool, content string) historyState {
	if !exists {
		return historyState{}
	}
	h := hashOf(content)
	if store != nil {
		if _, ok, err := store.Meta(contentKey(h)); err == nil && !ok {
			_ = store.SetMeta(contentKey(h), content)
		}
	}
	return historyState{Exists: true, Hash: h}
}

// fileState is the file at abs as it stands now, and its content.
func fileState(abs string) (historyState, string) {
	b, err := os.ReadFile(abs)
	if err != nil {
		return historyState{}, ""
	}
	return historyState{Exists: true, Hash: hashOf(string(b))}, string(b)
}

// pendingChanges is, for each Pre file event this call produces that carries
// citations and whose result is KNOWN, the change it would make; wholes names
// the paths an sr-file write in the line stated in full. An event whose result
// is unknown (a command the dry run could not compute) leaves nothing to tie a
// citation to, so it records nothing: a rule judging at Stop then sees the
// change uncited.
func pendingChanges(store sessionstate.Store, events []event.Event, root string, wholes map[string]bool, now int64) []pendingChange {
	var out []pendingChange
	for _, e := range events {
		cs := grounding.FromWire(e.Fields[grounding.FieldCitations])
		if len(cs) == 0 {
			continue
		}
		path, _ := e.Fields[filemod.FieldPath].(string)
		oldContent, _ := e.Fields[filemod.FieldOldContent].(string)
		newContent, _ := e.Fields[filemod.FieldNewContent].(string)
		pt := historyPoint{Cites: cs, At: now}
		switch e.Kind {
		case filemod.KindPreCreate, filemod.KindPreUpdate:
			if !resultKnown(e) {
				continue
			}
			pt.Before = putState(store, e.Kind == filemod.KindPreUpdate, oldContent)
			pt.After = putState(store, true, newContent)
			pt.Whole = wholes[path]
		case filemod.KindPreDelete:
			pt.Before = putState(store, true, oldContent)
			pt.Whole = true // a delete states the file's whole outcome
		default:
			continue
		}
		abs := path
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(root, path)
		}
		out = append(out, pendingChange{Path: path, Abs: abs, Point: pt})
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
	return swapJSON(store, sessionstate.MetaCitations, func(all *map[string][]historyPoint) {
		if *all == nil {
			*all = map[string][]historyPoint{}
		}
		for _, p := range landed {
			(*all)[p.Path] = append((*all)[p.Path], p.Point)
		}
	})
}

// landedOf is the pending changes whose file holds exactly what they produce.
func landedOf(pending []pendingChange) []pendingChange {
	var out []pendingChange
	for _, p := range pending {
		if st, _ := fileState(p.Abs); st == p.Point.After {
			out = append(out, p)
		}
	}
	return out
}

// beginCycle runs at every hook that reads or records cited changes, and acts
// only at the FIRST of a cycle: it records a foreign point for each file that
// is not as the agent left it at its last Stop (at the session's first hook,
// each file that differs from the baseline at all). Between the agent's Stop
// and its next hook no tool of the agent's ran, so what changed there — the
// user's edit, a branch switch, a file already dirty when the session began —
// is not the agent's to cite.
func beginCycle(store sessionstate.Store, dir string, now int64, selects citedPath) error {
	if store == nil {
		return nil
	}
	var cyc cycleMeta
	if raw, ok, err := store.Meta(sessionstate.MetaCitedCycle); err != nil {
		return err
	} else if ok && raw != "" {
		_ = json.Unmarshal([]byte(raw), &cyc)
	}
	if cyc.State == "open" {
		return nil
	}
	opened := cycleMeta{State: "open", StartedAt: now, Outlives: cyc.Outlives}
	commit, _, _ := store.Meta(sessionstate.MetaBaselineCommit)
	root, err := gitrepo.Root(dir)
	if err != nil || commit == "" {
		return writeCycle(store, opened)
	}
	if cyc.State == "ended" && cyc.Outlives {
		// The agent started work that can outlive the call that started it —
		// a background job, a detached process, a background sub-agent. What
		// changed since its Stop may be that work landing, so none of it is
		// set aside as not the agent's: it is charged like any change the
		// agent made.
		return writeCycle(store, opened)
	}

	paths := map[string]bool{}
	changes, _ := gitrepo.Changed(root, commit)
	for _, c := range changes {
		paths[c.Path] = true
	}
	for p := range cyc.End {
		paths[p] = true
	}

	var foreign []pendingChange
	for path := range paths {
		abs := filepath.Join(root, path)
		if fi, err := os.Stat(abs); err == nil && fi.Size() > snapshotMax {
			continue
		}
		cur, content := fileState(abs)
		if selects != nil && !selects(path, cur.Exists, content) {
			continue
		}
		pt := historyPoint{Foreign: true, At: now}
		if cyc.State == "ended" {
			prev, ok := cyc.End[path]
			if !ok {
				prev = baselineState(store, root, commit, path)
			}
			if prev == cur {
				continue
			}
			pt.From = &prev
			pt.FromAt = cyc.EndedAt
		}
		pt.After = putState(store, cur.Exists, content)
		foreign = append(foreign, pendingChange{Path: path, Point: pt})
	}
	if len(foreign) > 0 {
		if err := swapJSON(store, sessionstate.MetaCitations, func(all *map[string][]historyPoint) {
			if *all == nil {
				*all = map[string][]historyPoint{}
			}
			for _, f := range foreign {
				(*all)[f.Path] = append((*all)[f.Path], f.Point)
			}
		}); err != nil {
			return err
		}
	}
	return writeCycle(store, opened)
}

// markOutlives records that the agent started work that can outlive the call
// that started it. See cycleMeta.Outlives.
func markOutlives(store sessionstate.Store) error {
	if store == nil {
		return nil
	}
	cyc := readCycle(store)
	if cyc.Outlives {
		return nil
	}
	cyc.Outlives = true
	return writeCycle(store, cyc)
}

// baselineState is path as the baseline commit holds it, through the
// checkout's filters.
func baselineState(store sessionstate.Store, root, commit, path string) historyState {
	content, ok := gitrepo.ContentAt(root, commit, path)
	return putState(store, ok, content)
}

// endCycle records, at Stop, how the agent leaves each file the cycle's
// difference names, so the next cycle's first hook can tell a change the agent
// did not make (beginCycle).
func endCycle(store sessionstate.Store, events []event.Event, selects citedPath) error {
	if store == nil {
		return nil
	}
	cyc := readCycle(store)
	end := map[string]historyState{}
	for _, e := range events {
		path, _ := e.Fields[filemod.FieldPath].(string)
		newContent, _ := e.Fields[filemod.FieldNewContent].(string)
		exists := e.Kind != filemod.KindPostDelete
		if selects != nil && !selects(path, exists, newContent) {
			continue
		}
		end[path] = putState(store, exists, newContent)
	}
	if err := writeCycle(store, cycleMeta{State: "ended", StartedAt: cyc.StartedAt, EndedAt: nowNano(), End: end, Outlives: cyc.Outlives}); err != nil {
		return err
	}
	return gcContent(store)
}

// gcContent deletes every stored content no point, pending change or cycle
// snapshot still names: a dropped pending change, a pruned history, a file's
// superseded state at the last Stop.
func gcContent(store sessionstate.Store) error {
	if store == nil {
		return nil
	}
	keep := map[string]bool{}
	mark := func(s historyState) {
		if s.Exists {
			keep[contentKey(s.Hash)] = true
		}
	}
	markPoint := func(p historyPoint) {
		mark(p.Before)
		mark(p.After)
		if p.From != nil {
			mark(*p.From)
		}
	}
	for _, pts := range historyIn(store, true) {
		for _, p := range pts {
			markPoint(p)
		}
	}
	var pending []pendingChange
	if raw, ok, err := store.Meta(sessionstate.MetaCitedPending); err == nil && ok && raw != "" {
		_ = json.Unmarshal([]byte(raw), &pending)
	}
	for _, p := range pending {
		markPoint(p.Point)
	}
	for _, s := range readCycle(store).End {
		mark(s)
	}
	keys, err := store.MetaKeys(contentKeyPrefix)
	if err != nil {
		return err
	}
	for _, k := range keys {
		if !keep[k] {
			if err := store.DeleteMeta(k); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeCycle(store sessionstate.Store, c cycleMeta) error {
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return store.SetMeta(sessionstate.MetaCitedCycle, string(raw))
}

// pruneHistory drops every point recorded before cutoff, when the tree has
// left the history the baseline was on: they describe a line of history the
// tree no longer has. At Stop the cutoff is the current cycle's start (the
// agent switched mid-cycle, and the cycle's own points still stand); at
// SessionStart it is now (the switch happened between turns). The points kept
// lose their From — the move itself is not the agent's — and how the agent left
// each file at its last Stop is forgotten, so the next cycle compares with the
// new baseline.
func pruneHistory(store sessionstate.Store, cutoff int64) error {
	if store == nil {
		return nil
	}
	if err := swapJSON(store, sessionstate.MetaCitations, func(all *map[string][]historyPoint) {
		for path, pts := range *all {
			var kept []historyPoint
			for _, p := range pts {
				if p.At < cutoff {
					continue
				}
				if p.Foreign {
					p.From = nil
				}
				kept = append(kept, p)
			}
			if len(kept) == 0 {
				delete(*all, path)
			} else {
				(*all)[path] = kept
			}
		}
	}); err != nil {
		return err
	}
	cyc := readCycle(store)
	cyc.End = nil
	if err := writeCycle(store, cyc); err != nil {
		return err
	}
	return gcContent(store)
}

// cycleStartedAt is when the store's current cycle began.
func cycleStartedAt(store sessionstate.Store) int64 {
	return readCycle(store).StartedAt
}

func readCycle(store sessionstate.Store) cycleMeta {
	var cyc cycleMeta
	if store == nil {
		return cyc
	}
	if raw, ok, err := store.Meta(sessionstate.MetaCitedCycle); err == nil && ok && raw != "" {
		_ = json.Unmarshal([]byte(raw), &cyc)
	}
	return cyc
}

// historyIn is every point the store kept, by path — for a store only read
// and never settled (another session's), its cited points only, with the
// pending ones that have landed; its foreign points are its own business.
func historyIn(store sessionstate.Store, own bool) map[string][]historyPoint {
	all := map[string][]historyPoint{}
	if store == nil {
		return all
	}
	if raw, ok, err := store.Meta(sessionstate.MetaCitations); err == nil && ok && raw != "" {
		_ = json.Unmarshal([]byte(raw), &all)
	}
	if own {
		return all
	}
	out := map[string][]historyPoint{}
	for path, pts := range all {
		for _, p := range pts {
			if !p.Foreign {
				out[path] = append(out[path], p)
			}
		}
	}
	var pending []pendingChange
	if raw, ok, err := store.Meta(sessionstate.MetaCitedPending); err == nil && ok && raw != "" {
		_ = json.Unmarshal([]byte(raw), &pending)
	}
	for _, p := range landedOf(pending) {
		out[p.Path] = append(out[p.Path], p.Point)
	}
	return out
}

// otherHistories is what the sessions this one shares its tree with kept, by
// path — their cited points, and the contents those name (read now: their
// stores are closed after). A session's own store knows only its own calls,
// but in a shared tree the file one changed is in another's difference:
//
//   - for the ROOT: every sub-agent dispatched beneath its record;
//   - for a SUB-AGENT: the root that dispatched it and the root's other
//     sub-agents — the file it edits may have been created, cited, by them.
//
// Each store is found by its session's identity under this cycle's working
// directory and read only if it already exists: a session isolated in its own
// worktree keeps its store under that worktree, and its files are not in this
// tree either. None of this is ever written.
func otherHistories(p HookPayload, record string) (map[string][]historyPoint, map[string]string) {
	points := map[string][]historyPoint{}
	contents := map[string]string{}
	if record == "" {
		return points, contents
	}
	root := transcript.SessionRootOf(record)
	if root == "" {
		root = record
	}
	var others []HookPayload
	if root != record {
		others = append(others, HookPayload{Cwd: p.Cwd, TranscriptPath: root})
	}
	subs, _ := transcript.DescendantSubagentPaths(root)
	for _, sub := range subs {
		if sub == record {
			continue
		}
		others = append(others, HookPayload{Cwd: p.Cwd, AgentTranscriptPath: sub})
	}
	for _, o := range others {
		id, err := stableID(o)
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
		for path, pts := range historyIn(store, false) {
			points[path] = append(points[path], pts...)
			for _, pt := range pts {
				for _, s := range []historyState{pt.Before, pt.After} {
					if s.Exists {
						if c, ok, err := store.Meta(contentKey(s.Hash)); err == nil && ok {
							contents[s.Hash] = c
						}
					}
				}
			}
		}
		store.Close()
	}
	return points, contents
}

// attachHistories sets `citations` on each Post file event to those of the
// cited changes that landed on its path this session — this session's own and
// those of the sessions it shares the tree with (others) — and returns each
// such path's history for its rules.
func attachHistories(store sessionstate.Store, events []event.Event, others map[string][]historyPoint, otherContents map[string]string) map[string]*dispatchcore.FileHistory {
	own := historyIn(store, true)
	out := map[string]*dispatchcore.FileHistory{}
	for i, e := range events {
		path, _ := e.Fields[filemod.FieldPath].(string)
		pts := append(append([]historyPoint(nil), own[path]...), others[path]...)
		var cs []transcript.Citation
		for _, p := range pts {
			cs = append(cs, p.Cites...)
		}
		if len(cs) == 0 {
			continue
		}
		events[i].Fields[grounding.FieldCitations] = grounding.ToWire(dedupe(cs))

		oldContent, _ := e.Fields[filemod.FieldOldContent].(string)
		newContent, _ := e.Fields[filemod.FieldNewContent].(string)
		local := map[string]string{}
		state := func(exists bool, c string) dispatchcore.HistoryState {
			if !exists {
				return dispatchcore.HistoryState{}
			}
			h := hashOf(c)
			local[h] = c
			return dispatchcore.HistoryState{Exists: true, Hash: h}
		}
		h := &dispatchcore.FileHistory{
			Baseline: state(e.Kind != filemod.KindPostCreate, oldContent),
			Current:  state(e.Kind != filemod.KindPostDelete, newContent),
		}
		for _, p := range pts {
			dp := dispatchcore.HistoryPoint{
				Foreign: p.Foreign, FromAt: p.FromAt, Whole: p.Whole, At: p.At,
				Before: dispatchcore.HistoryState(p.Before), After: dispatchcore.HistoryState(p.After),
				Pools: poolsOf(p.Cites),
			}
			if p.From != nil {
				from := dispatchcore.HistoryState(*p.From)
				dp.From = &from
			}
			h.Points = append(h.Points, dp)
		}
		h.Content = func(hash string) (string, bool) {
			if c, ok := local[hash]; ok {
				return c, true
			}
			if c, ok := otherContents[hash]; ok {
				return c, true
			}
			if store == nil {
				return "", false
			}
			c, ok, err := store.Meta(contentKey(hash))
			return c, ok && err == nil
		}
		out[path] = h
	}
	return out
}

// poolsOf is every pool a change's citations resolved in.
func poolsOf(cs []transcript.Citation) []transcript.SourceType {
	var out []transcript.SourceType
	seen := map[transcript.SourceType]bool{}
	for _, c := range cs {
		for _, s := range c.SourceTypes {
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
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

// nowNano is the ordering stamp a point carries.
func nowNano() int64 { return time.Now().UnixNano() }

// citedPathsOf is the citedPath of the loaded rules: a file any file-guard
// requiring a citation selects, as it now stands. A match that cannot be
// compiled or answered selects the file — a snapshot too many costs a stored
// content, one too few charges the agent for a change it did not make.
func citedPathsOf(guards []declaration.FileGuard) citedPath {
	var matches []*guardrail.Matcher
	for _, g := range guards {
		cites := false
		for _, r := range g.Require {
			cites = cites || r.Citation != nil
		}
		if !cites {
			continue
		}
		m, err := guardrail.CompileFileMatch(g.Match)
		if err != nil {
			return nil
		}
		matches = append(matches, m)
	}
	return func(path string, exists bool, content string) bool {
		fe := filemod.FileEvent{Path: path}
		kind := filemod.KindPostDelete
		if exists {
			fe.NewContent, fe.NewMarkers = content, filemod.Scan(content)
			kind = filemod.KindPostUpdate
		}
		e := fe.Event(kind)
		for _, m := range matches {
			if ok, err := fileGuardSelects(m, e, nil); ok || err != nil {
				return true
			}
		}
		return false
	}
}

// launchesBackground reports whether a tool call starts work that can outlive
// it: a shell command run in the background or detaching work (nohup, setsid,
// `&`, at, crontab, …), or a sub-agent run in the background.
func launchesBackground(p HookPayload) bool {
	var in struct {
		Command    string `json:"command"`
		Background bool   `json:"run_in_background"`
	}
	_ = json.Unmarshal(p.ToolInput, &in)
	if in.Background {
		return true
	}
	return commandmod.HarnessCommandTools[p.ToolName] && in.Command != "" && commandmod.Backgrounds(in.Command)
}

// markCitedUnknown remembers each file a permitted call changed with citations
// that resolved but a result that could not be computed ahead of time — an
// sr-file call the dry run never ran (not on its own in the line, named by a
// path that is not the engine's own sr-file, sr-file not on PATH). Such a
// change cannot be tied to its citations; if a rule refuses the file at Stop,
// the refusal says why (citedUnknownNote).
func markCitedUnknown(store sessionstate.Store, events []event.Event) error {
	var paths []string
	for _, e := range events {
		if (e.Kind == filemod.KindPreCreate || e.Kind == filemod.KindPreUpdate) && !resultKnown(e) &&
			len(grounding.FromWire(e.Fields[grounding.FieldCitations])) > 0 {
			path, _ := e.Fields[filemod.FieldPath].(string)
			paths = append(paths, path)
		}
	}
	if store == nil || len(paths) == 0 {
		return nil
	}
	return swapJSON(store, sessionstate.MetaCitedUnknown, func(all *map[string]bool) {
		if *all == nil {
			*all = map[string]bool{}
		}
		for _, p := range paths {
			(*all)[p] = true
		}
	})
}

// citedUnknownNote is what a Stop refusal of path adds when a cited change to
// it could not be computed ahead of time; "" otherwise.
func citedUnknownNote(store sessionstate.Store, path string) string {
	if store == nil || path == "" {
		return ""
	}
	raw, ok, err := store.Meta(sessionstate.MetaCitedUnknown)
	if err != nil || !ok {
		return ""
	}
	var all map[string]bool
	if json.Unmarshal([]byte(raw), &all) != nil || !all[path] {
		return ""
	}
	return "\nA cited sr-file call changed this file, but its result could not be computed before it ran, so its citations could not be tied to what landed. " +
		"Run sr-file ON ITS OWN in the line and by its bare name `sr-file` (it must be on PATH: check `command -v sr-file`; if that fails, install sloprail's binaries onto PATH), with every value quoted verbatim."
}
