package checkcache

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// GcStats says what a compaction did.
type GcStats struct {
	Records    int // latest result per key kept
	Duplicates int // superseded results dropped
	SegsBefore int
	SegsAfter  int
	Retrained  bool
}

// readAll inflates every record of the snapshot, resolving duplicate keys. It also returns
// every run the snapshot holds, each once (a run that holds a winning result in the copy that
// wins), so a compaction keeps the run history, not only the winning results.
func (s *Store) readAll(sn *snapshot) (map[string]Found, int, []Run, error) {
	out := map[string]Found{}
	total := 0
	if len(sn.Segs) == 0 {
		return out, 0, nil, nil
	}
	b, err := s.g.startBatch()
	if err != nil {
		return nil, 0, nil, err
	}
	defer b.close()
	byRun := map[string]Run{}
	for _, sg := range sn.Segs {
		blob, err := b.read(sg.ZstOid)
		if err != nil {
			return nil, 0, nil, err
		}
		if err := verifySegment(sg.Name, blob); err != nil {
			return nil, 0, nil, err
		}
		d, err := s.dictByOid(sg.Dict, sn.Dicts[sg.Dict])
		if err != nil {
			return nil, 0, nil, err
		}
		for i := range sg.Keys {
			if int(sg.Pos[i]) == runOnly {
				r, err := sg.decodeRun(blob, i, d)
				if err != nil {
					return nil, 0, nil, err
				}
				if _, ok := byRun[r.ID]; !ok {
					byRun[r.ID] = r // a run with nothing findable is history too
				}
				continue
			}
			r, err := sg.decodeAt(blob, i, d)
			if err != nil {
				return nil, 0, nil, err
			}
			byRun[r.Run.ID] = r.Run
			total++
			id := r.Run.CheckKey(r.Check).ID()
			if p, ok := out[id]; ok && !Newer(r, p) {
				continue
			}
			out[id] = r
		}
	}
	runs := make([]Run, 0, len(byRun))
	for _, r := range byRun {
		runs = append(runs, r)
	}
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].RunAt != runs[j].RunAt {
			return runs[i].RunAt < runs[j].RunAt
		}
		return runs[i].ID < runs[j].ID
	})
	return out, total, runs, nil
}

// Gc compacts the branch: one new commit on top of the tip (never a rewrite of the shared
// history) whose tree holds the latest result of every key and every run (run-only history
// included, so Runs is the same before and after)
// in segments of about SegmentTarget runs, with a dictionary trained on the runs when there
// are TrainMin or more of them, and none (plain zstd) below that. With a remote the commit is
// pushed like any Put's, as a fast-forward; a push that loses a race is fetched, replayed on the
// new tip and retried, and one that fails for another reason stays pending (PendingPush).
//
// Gc runs by itself after a Put once the branch holds GcSegments segments, or TrainMin runs
// with no dictionary yet (see maybeGc); it is also callable on its own.
func (s *Store) Gc() (GcStats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gc()
}

// GcSegments is how many segments a Put leaves behind before the store compacts them: every
// Lookup scans every segment index.
const GcSegments = 24

// maybeGc is Gc's cheap trigger, after a Put: enough segments to be worth squashing, or enough
// runs spread over several segments for a repository dictionary to be trained (a lone
// segment is already what Gc would write). A Gc that fails is not the Put's failure.
func (s *Store) maybeGc() {
	if s.opt.NoAutoGc {
		return
	}
	// Compaction is housekeeping after the results are already stored: it must never take
	// the run down with it. A failure leaves the segments as they are for the next Gc.
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "sloprail: checks cache compaction skipped: %v\n", r)
		}
	}()
	sn, err := s.snapshotAt(s.tip())
	if err != nil || len(sn.Segs) < 2 {
		return
	}
	runs := 0
	for _, sg := range sn.Segs {
		seen := map[uint32]bool{}
		for _, off := range sg.Offs {
			seen[off] = true
		}
		runs += len(seen)
	}
	if len(sn.Segs) >= GcSegments || (sn.ManifestDict == "" && runs >= TrainMin) {
		_, _ = s.gc()
	}
}

// compactedFiles is the layout of the runs in segments of SegmentTarget, with a dictionary
// trained on them when there are TrainMin or more: the manifest, dictionary and segments of one
// schema directory. st.Retrained and st.SegsAfter are filled in.
func (s *Store) compactedFiles(runs []Run, st *GcStats) (map[string][]byte, error) {
	d, err := s.plainDict()
	if err != nil {
		return nil, err
	}
	if len(runs) >= TrainMin {
		var samples [][]byte
		step := len(runs)/4000 + 1
		for i := 0; i < len(runs); i += step {
			raw, _ := json.Marshal(runs[i])
			samples = append(samples, raw)
		}
		if tb, err := trainDict(samples); err == nil {
			if nd, err := newZdict(tb); err == nil {
				s.dicts[nd.sha] = nd
				d, st.Retrained = nd, true
			}
		}
	}
	files := map[string][]byte{}
	if d.sha != "" {
		files["dict/"+d.sha+".zdict"] = d.bytes
	}
	m, _ := json.Marshal(manifest{Schema: s.schemaDir(), Dict: d.sha})
	files["MANIFEST.json"] = m
	for lo := 0; lo < len(runs); lo += SegmentTarget {
		hi := min(lo+SegmentTarget, len(runs))
		name, zst, idx, err := encodeSegment(runs[lo:hi], d)
		if err != nil {
			return nil, err
		}
		files["seg/"+name+".zst"] = zst
		files["seg/"+name+".idx"] = idx
		st.SegsAfter++
	}
	return files, nil
}

func (s *Store) gc() (GcStats, error) {
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(10+attempt*20) * time.Millisecond)
		}
		if err := s.sync(); err != nil {
			return GcStats{}, err
		}
		tip := s.tip()
		if tip == "" {
			return GcStats{}, nil
		}
		sn, err := s.snapshotAt(tip)
		if err != nil {
			return GcStats{}, err
		}
		recs, total, runs, err := s.readAll(sn)
		if err != nil {
			return GcStats{}, err
		}
		ids := make([]string, 0, len(recs))
		for id := range recs {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		st := GcStats{Records: len(ids), Duplicates: total - len(ids), SegsBefore: len(sn.Segs)}

		files, err := s.compactedFiles(runs, &st)
		if err != nil {
			return st, err
		}
		// A NEW commit on top of the tip whose tree is the compacted layout: the shared history is
		// never rewritten, so the push below is an ordinary fast-forward (a concurrent writer's
		// run is replayed on top of it by push, never overwritten).
		commit, err := s.commitReplacing(tip, files, fmt.Sprintf("checks: gc, %d results", len(ids)))
		if err != nil {
			return st, err
		}
		if _, err := s.g.run(nil, nil, "update-ref", s.opt.Ref, commit, tip); err != nil {
			lastErr = err // a local writer moved the ref meanwhile
			continue
		}
		if s.beforeGcPush != nil {
			s.beforeGcPush()
		}
		s.pushErr = s.push()
		return st, nil
	}
	return GcStats{}, fmt.Errorf("checkcache: gc gave up after %d attempts: %w", maxAttempts, lastErr)
}

// Find returns the latest stored result of every key of a subject, newest first. It reads
// the local ref only.
func (s *Store) Find(subject string) ([]Found, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sn, err := s.snapshotAt(s.tip())
	if err != nil {
		return nil, err
	}
	recs, _, _, err := s.readAll(sn)
	if err != nil {
		return nil, err
	}
	var out []Found
	for _, f := range recs {
		if f.Check.Subject == subject {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Run.RunAt != out[j].Run.RunAt {
			return out[i].Run.RunAt > out[j].Run.RunAt
		}
		return out[i].Run.CheckKey(out[i].Check).ID() < out[j].Run.CheckKey(out[j].Check).ID()
	})
	return out, nil
}

// Show renders the stored results of a subject for a human.
func (s *Store) Show(subject string) (string, error) {
	rs, err := s.Find(subject)
	if err != nil {
		return "", err
	}
	if len(rs) == 0 {
		return fmt.Sprintf("no stored results for %s\n", subject), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %d stored result(s)\n", subject, len(rs))
	for _, f := range rs {
		fp := f.Check.Fingerprint
		if len(fp) > 12 {
			fp = fp[:12]
		}
		fmt.Fprintf(&b, "\n%-5s %s  %s\n", strings.ToUpper(f.Check.Status), f.Run.Rule, f.Check.Kind)
		fmt.Fprintf(&b, "      fingerprint %s  rule %s  range %s..%s\n", fp, f.Run.RuleHash, short(f.Run.BaseRef), short(f.Run.HeadRef))
		fmt.Fprintf(&b, "      run %s at %s\n", f.Run.ID, f.Run.RunAt)
		if why, _ := f.Check.Metadata["reasoning"].(string); why != "" {
			fmt.Fprintf(&b, "      %s\n", strings.ReplaceAll(strings.TrimSpace(why), "\n", "\n      "))
		}
	}
	return b.String(), nil
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// Runs returns every run the local ref holds, each once, newest first. It reads the local ref
// only (call Sync first for fresh data); for a test or a reader that lists, not for a lookup.
func (s *Store) Runs() ([]Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tip := s.tip()
	if s.runsMemo != nil && s.runsMemoTip == tip && tip != "" {
		return append([]Run(nil), s.runsMemo...), nil
	}
	sn, err := s.snapshotAt(tip)
	if err != nil {
		return nil, err
	}
	out, err := s.runsOf(sn)
	if err != nil {
		return nil, err
	}
	// Decoding every segment is the dearest read the store has, and a verify asks for it once
	// per rule: keep the decoded runs for as long as the ref's tip is the same commit.
	s.runsMemo, s.runsMemoTip = out, tip
	return append([]Run(nil), out...), nil
}

// runsOf is Runs over one snapshot, for a caller that holds the lock.
func (s *Store) runsOf(sn *snapshot) ([]Run, error) {
	var out []Run
	if len(sn.Segs) == 0 {
		return out, nil
	}
	b, err := s.g.startBatch()
	if err != nil {
		return nil, err
	}
	defer b.close()
	seen := map[string]bool{}
	for _, sg := range sn.Segs {
		blob, err := b.read(sg.ZstOid)
		if err != nil {
			return nil, err
		}
		if err := verifySegment(sg.Name, blob); err != nil {
			return nil, err
		}
		d, err := s.dictByOid(sg.Dict, sn.Dicts[sg.Dict])
		if err != nil {
			return nil, err
		}
		for i := range sg.Keys {
			r, err := sg.decodeRun(blob, i, d)
			if err != nil {
				return nil, err
			}
			if !seen[r.ID] {
				seen[r.ID] = true
				out = append(out, r)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RunAt > out[j].RunAt })
	return out, nil
}
