package sessionstate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// The bounds of a session's citation history (meta key MetaCitations).
//
// The history is read, parsed and rewritten whole by the first hook of every cycle, so its size is
// every later hook's cost. It grew without bound: a file the agent never touched but whose content
// differed from how the agent last left it (a background job writing it, another worktree sharing
// the tree) was recorded again at the start of EVERY cycle, as a point naming the same stretch,
// carrying the full text of every detached command. One session reached 988 MB and a first hook
// took minutes. These bounds are what the history may hold however long the session runs.
const (
	// MaxUncitedPoints is how many points without a citation (a stretch of the file the agent did
	// not make, or made between turns) one path keeps; the newest stay.
	MaxUncitedPoints = 32
	// MaxCitedPoints is how many cited changes one path keeps; the newest stay.
	MaxCitedPoints = 256
	// MaxByEntries is how many distinct background commands a "by" names and a cycle record keeps.
	MaxByEntries = 8
	// MaxByEntryLen is the longest a named command is: a refusal says what ran, it does not quote
	// the script.
	MaxByEntryLen = 120
)

// citationPeek is what compaction needs to compare two points; the rest of a point is carried
// through untouched.
type citationPeek struct {
	Foreign      bool            `json:"foreign"`
	BetweenTurns bool            `json:"betweenTurns"`
	Whole        bool            `json:"whole"`
	From         json.RawMessage `json:"from"`
	Before       json.RawMessage `json:"before"`
	After        json.RawMessage `json:"after"`
	Cites        json.RawMessage `json:"cites"`
	By           string          `json:"by"`
}

func (p citationPeek) cited() bool {
	c := bytes.TrimSpace(p.Cites)
	return len(c) > 0 && string(c) != "null" && string(c) != "[]"
}

// sameStretch reports whether b names the very stretch a does: the same kind of point from the same
// state to the same state, neither cited. A later hook that finds the file still not as the agent
// left it records that again; it is one stretch, not one more per cycle.
func sameStretch(a, b citationPeek) bool {
	return !a.cited() && !b.cited() && !a.Whole && !b.Whole &&
		a.Foreign == b.Foreign && a.BetweenTurns == b.BetweenTurns &&
		bytes.Equal(canonJSON(a.From), canonJSON(b.From)) &&
		bytes.Equal(canonJSON(a.Before), canonJSON(b.Before)) &&
		bytes.Equal(canonJSON(a.After), canonJSON(b.After))
}

func canonJSON(m json.RawMessage) []byte {
	if len(m) == 0 {
		return []byte("null")
	}
	return bytes.TrimSpace(m)
}

// CompactCitationPoints bounds one path's history, oldest first: a point that repeats the stretch
// before it is folded into it (the first stays, with the newest "by"), a "by" is bounded, and each
// kind keeps its newest MaxUncitedPoints / MaxCitedPoints. Order is kept. A point that fails to
// parse is kept as it is.
func CompactCitationPoints(points []json.RawMessage) []json.RawMessage {
	type kept struct {
		raw   json.RawMessage
		peek  citationPeek
		ok    bool
		cited bool
	}
	var out []kept
	for _, raw := range points {
		var pk citationPeek
		ok := json.Unmarshal(raw, &pk) == nil
		if ok && len(out) > 0 {
			if last := &out[len(out)-1]; last.ok && sameStretch(last.peek, pk) {
				if pk.By != last.peek.By {
					last.peek.By = pk.By
					last.raw = withBy(last.raw, pk.By)
				}
				continue
			}
		}
		out = append(out, kept{raw: raw, peek: pk, ok: ok, cited: ok && pk.cited()})
	}
	// Newest of each kind stay: walk from the end counting.
	uncited, cited := 0, 0
	keep := make([]bool, len(out))
	for i := len(out) - 1; i >= 0; i-- {
		switch {
		case out[i].cited:
			cited++
			keep[i] = cited <= MaxCitedPoints
		case out[i].ok:
			uncited++
			keep[i] = uncited <= MaxUncitedPoints
		default:
			keep[i] = true
		}
	}
	var res []json.RawMessage
	for i, k := range out {
		if !keep[i] {
			continue
		}
		raw := k.raw
		if k.ok && k.peek.By != "" {
			if b := BoundBy(k.peek.By); b != k.peek.By {
				raw = withBy(raw, b)
			}
		}
		res = append(res, raw)
	}
	return res
}

// withBy is the point with its "by" replaced, every other field as it was.
func withBy(raw json.RawMessage, by string) json.RawMessage {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return raw
	}
	b, err := json.Marshal(by)
	if err != nil {
		return raw
	}
	m["by"] = b
	out, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return out
}

// BoundBy bounds the text a point's "by" holds: the newest MaxByEntries of its "; "-joined entries,
// each cut at MaxByEntryLen. Entries are told apart by the "; " the joiner puts between them, so a
// command that itself contains one is bounded as two; what it keeps is still only text a refusal
// shows.
func BoundBy(by string) string {
	parts := strings.Split(by, "; ")
	seen := map[string]bool{}
	var out []string
	for _, p := range parts {
		p = CutEntry(p)
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	if len(out) > MaxByEntries {
		out = out[len(out)-MaxByEntries:]
	}
	return strings.Join(out, "; ")
}

// CutEntry cuts one named command at MaxByEntryLen bytes, on a rune boundary, marking the cut.
func CutEntry(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= MaxByEntryLen+len("…") {
		return s
	}
	cut := MaxByEntryLen
	for cut > 0 && s[cut]&0xC0 == 0x80 {
		cut--
	}
	return s[:cut] + "…"
}

// LastEntries keeps the newest MaxByEntries of a list of named commands, each cut.
func LastEntries(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = CutEntry(s)
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	if len(out) > MaxByEntries {
		out = out[len(out)-MaxByEntries:]
	}
	return out
}

// CompactCitations rewrites a whole stored history (a JSON object of path to list of points) within
// the bounds. It reads the object path by path and holds only the compacted result, so a history
// that grew to gigabytes is not decoded into memory twice over. It returns the new text and
// whether it differs from raw.
func CompactCitations(raw string) (string, bool, error) {
	if strings.TrimSpace(raw) == "" {
		return raw, false, nil
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return raw, false, fmt.Errorf("sessionstate: the citation history is not a JSON object")
	}
	var out bytes.Buffer
	out.WriteByte('{')
	first := true
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return raw, false, fmt.Errorf("sessionstate: read the citation history: %w", err)
		}
		path, _ := tok.(string)
		var points []json.RawMessage
		if err := dec.Decode(&points); err != nil {
			return raw, false, fmt.Errorf("sessionstate: read the citation history of %q: %w", path, err)
		}
		points = CompactCitationPoints(points)
		if len(points) == 0 {
			continue
		}
		if !first {
			out.WriteByte(',')
		}
		first = false
		key, _ := json.Marshal(path)
		out.Write(key)
		out.WriteByte(':')
		out.WriteByte('[')
		for i, p := range points {
			if i > 0 {
				out.WriteByte(',')
			}
			out.Write(p)
		}
		out.WriteByte(']')
	}
	if _, err := dec.Token(); err != nil && err != io.EOF {
		return raw, false, fmt.Errorf("sessionstate: read the citation history: %w", err)
	}
	out.WriteByte('}')
	got := out.String()
	return got, got != raw, nil
}

// CompactCycle bounds the lists a cycle record keeps (the commands that may still run between
// turns). It returns the new text and whether it differs.
func CompactCycle(raw string) (string, bool) {
	if strings.TrimSpace(raw) == "" {
		return raw, false
	}
	var m map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &m) != nil {
		return raw, false
	}
	changed := false
	for _, field := range []string{"detachedBy", "tasksBy", "launched"} {
		v, ok := m[field]
		if !ok {
			continue
		}
		var list []string
		if json.Unmarshal(v, &list) != nil {
			continue
		}
		bounded := LastEntries(list)
		if len(bounded) == len(list) && equalStrings(bounded, list) {
			continue
		}
		changed = true
		if len(bounded) == 0 {
			delete(m, field)
			continue
		}
		b, _ := json.Marshal(bounded)
		m[field] = b
	}
	if !changed {
		return raw, false
	}
	out, err := json.Marshal(m)
	if err != nil {
		return raw, false
	}
	return string(out), true
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
