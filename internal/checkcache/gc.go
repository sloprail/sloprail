package checkcache

import (
	"encoding/json"
	"fmt"
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

// readAll inflates every record of the snapshot, resolving duplicate keys.
func (s *Store) readAll(sn *snapshot) (map[string]Found, int, error) {
	out := map[string]Found{}
	total := 0
	if len(sn.Segs) == 0 {
		return out, 0, nil
	}
	b, err := s.g.startBatch()
	if err != nil {
		return nil, 0, err
	}
	defer b.close()
	for _, sg := range sn.Segs {
		blob, err := b.read(sg.ZstOid)
		if err != nil {
			return nil, 0, err
		}
		if err := verifySegment(sg.Name, blob); err != nil {
			return nil, 0, err
		}
		d, err := s.dictByOid(sg.Dict, sn.Dicts[sg.Dict])
		if err != nil {
			return nil, 0, err
		}
		for i := range sg.Keys {
			if int(sg.Pos[i]) == runOnly {
				continue // a run with nothing findable is not a result; Gc lets it go
			}
			r, err := sg.decodeAt(blob, i, d)
			if err != nil {
				return nil, 0, err
			}
			total++
			id := r.Run.CheckKey(r.Check).ID()
			if p, ok := out[id]; ok && !Newer(r, p) {
				continue
			}
			out[id] = r
		}
	}
	return out, total, nil
}

// Gc squashes the branch to a single root commit holding the latest result of
// every key in segments of about SegmentTarget records, with a dictionary
// trained on the records when there are enough of them. A concurrent writer
// makes the lease fail; Gc then replays on the new tip.
func (s *Store) Gc() (GcStats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
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
		recs, total, err := s.readAll(sn)
		if err != nil {
			return GcStats{}, err
		}
		ids := make([]string, 0, len(recs))
		for id := range recs {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		st := GcStats{Records: len(ids), Duplicates: total - len(ids), SegsBefore: len(sn.Segs)}

		// The runs that hold a winning check, each once, in key order.
		var runs []Run
		seenRun := map[string]bool{}
		for _, id := range ids {
			if r := recs[id].Run; !seenRun[r.ID] {
				seenRun[r.ID] = true
				runs = append(runs, r)
			}
		}
		d, err := s.defaultDict()
		if err != nil {
			return st, err
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
		files := map[string][]byte{
			"dict/" + d.sha + ".zdict": d.bytes,
		}
		m, _ := json.Marshal(manifest{Schema: SchemaDir, Dict: d.sha})
		files["MANIFEST.json"] = m
		for lo := 0; lo < len(runs); lo += SegmentTarget {
			hi := min(lo+SegmentTarget, len(runs))
			name, zst, idx, err := encodeSegment(runs[lo:hi], d)
			if err != nil {
				return st, err
			}
			files["seg/"+name+".zst"] = zst
			files["seg/"+name+".idx"] = idx
			st.SegsAfter++
		}
		commit, err := s.commit("", true, files, fmt.Sprintf("checks: gc, %d results", len(ids)))
		if err != nil {
			return st, err
		}
		if err := s.publish(commit, tip, true); err != nil {
			lastErr = err
			continue
		}
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
	recs, _, err := s.readAll(sn)
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
	sn, err := s.snapshotAt(s.tip())
	if err != nil {
		return nil, err
	}
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
