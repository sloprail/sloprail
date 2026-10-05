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
	At           int64           `json:"at"`
}

func (p citationPeek) cited() bool {
	c := bytes.TrimSpace(p.Cites)
	return len(c) > 0 && string(c) != "null" && string(c) != "[]"
}

func (p citationPeek) noFrom() bool {
	f := bytes.TrimSpace(p.From)
	return len(f) == 0 || string(f) == "null"
}

// anchor reports whether the point only says where the file stood: foreign, from nowhere, no
// citation. The session's first hook leaves one for each file already dirty; compaction leaves
// one for what it drops (see CompactCitationPoints).
func (p citationPeek) anchor() bool {
	return p.Foreign && p.noFrom() && !p.BetweenTurns && !p.Whole && !p.cited() && p.By == ""
}

// sameStretch reports whether b names the very stretch a does: the same kind of point from the same
// state to the same state, neither cited. A later hook that finds the file still not as the agent
// left it records that again.
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

type keptPoint struct {
	raw   json.RawMessage
	peek  citationPeek
	ok    bool
	cited bool
	drop  bool
}

// CompactCitationPoints bounds one path's history, oldest first. Order is kept, and so is what the
// walk over the history (dispatch.FileHistory) needs of it:
//
//   - a stretch recorded again and again keeps its first two sightings and the newest: the second
//     sighting is what says the file went back to where it started and forth again, and a third
//     adds nothing the second did not;
//   - each kind keeps its newest MaxUncitedPoints / MaxCitedPoints. What goes is a PREFIX of the
//     history by time: the newest point over a cap sets a cut, and everything up to it, of either
//     kind, is replaced by ONE anchor, a foreign point from nowhere standing where that point left
//     the file. What follows is then measured from the state it was really measured from, and no
//     stretch is invented across the gap (a kept point older than the cut would run before the
//     anchor and not see the state the dropped ones set). Anchors are not counted against the
//     bound;
//   - a "by" is bounded (BoundBy).
//
// A point that fails to parse is kept as it is.
func CompactCitationPoints(points []json.RawMessage) []json.RawMessage {
	var out []keptPoint
	for _, raw := range points {
		var pk citationPeek
		ok := json.Unmarshal(raw, &pk) == nil
		it := keptPoint{raw: raw, peek: pk, ok: ok, cited: ok && pk.cited()}
		if ok && !it.cited && len(out) >= 2 {
			last, before := &out[len(out)-1], out[len(out)-2]
			if last.ok && before.ok && sameStretch(last.peek, pk) && sameStretch(before.peek, pk) {
				*last = it // the newest sighting stands for the ones between
				continue
			}
		}
		out = append(out, it)
	}

	uncited, cited := 0, 0
	for i := len(out) - 1; i >= 0; i-- {
		switch {
		case !out[i].ok || out[i].peek.anchor():
		case out[i].cited:
			if cited++; cited > MaxCitedPoints {
				out[i].drop = true
			}
		default:
			if uncited++; uncited > MaxUncitedPoints {
				out[i].drop = true
			}
		}
	}
	var newest *keptPoint
	for i := range out {
		if out[i].drop && (newest == nil || out[i].peek.At >= newest.peek.At) {
			newest = &out[i]
		}
	}
	var res []json.RawMessage
	if newest != nil {
		cut := newest.peek.At
		for i := range out {
			if out[i].ok && out[i].peek.At <= cut { // the prefix up to the cut, whatever its kind
				out[i].drop = true
			}
		}
		for i := range out {
			// An existing anchor later than the cut stands in for the dropped ones too.
			if out[i].ok && out[i].peek.anchor() && out[i].peek.At > newest.peek.At {
				newest = &out[i]
			}
		}
		res = append(res, anchorAt(newest.peek))
	}
	for _, k := range out {
		if k.drop {
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

// anchorAt is the foreign point that stands where p left the file.
func anchorAt(p citationPeek) json.RawMessage {
	after := p.After
	if len(bytes.TrimSpace(after)) == 0 || string(bytes.TrimSpace(after)) == "null" {
		after = json.RawMessage(`{"exists":false}`)
	}
	b, _ := json.Marshal(map[string]any{
		"foreign": true,
		"before":  json.RawMessage(`{"exists":false}`),
		"after":   after,
		"at":      p.At,
	})
	return b
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
	return strings.Join(LastEntries(strings.Split(by, "; ")), "; ")
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

// LastEntries keeps the newest MaxByEntries distinct entries of a list of named commands, oldest
// first, each cut. A command named again counts as newest.
func LastEntries(in []string) []string {
	seen := map[string]bool{}
	var rev []string
	for i := len(in) - 1; i >= 0 && len(rev) < MaxByEntries; i-- {
		s := CutEntry(in[i])
		if s != "" && !seen[s] {
			seen[s] = true
			rev = append(rev, s)
		}
	}
	out := make([]string, len(rev))
	for i, s := range rev {
		out[len(rev)-1-i] = s
	}
	return out
}

// compactEvery is how many bytes of one path's points are held before they are compacted mid-read.
const compactEvery = 8 << 20

// CompactCitations rewrites a whole stored history (a JSON object of path to list of points) within
// the bounds. It reads the object point by point and keeps only what each path's compaction needs,
// so a history that grew to gigabytes is not decoded into memory twice over. It returns the new
// text and whether it differs from raw.
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
		tok, err = dec.Token()
		if err == nil && tok == nil {
			continue // a path with no list: nothing to keep
		}
		if err != nil || tok != json.Delim('[') {
			return raw, false, fmt.Errorf("sessionstate: the citation history of %q is not a list", path)
		}
		var points []json.RawMessage
		held := 0
		for dec.More() {
			var p json.RawMessage
			if err := dec.Decode(&p); err != nil {
				return raw, false, fmt.Errorf("sessionstate: read the citation history of %q: %w", path, err)
			}
			points = append(points, p)
			held += len(p)
			if len(points) >= 4*(MaxUncitedPoints+MaxCitedPoints) || held > compactEvery {
				// A list this long, or this heavy, is compacted as it is read: what it holds beyond
				// the bounds is never all in memory at once.
				points = CompactCitationPoints(points)
				held = 0
				for _, q := range points {
					held += len(q)
				}
			}
		}
		if _, err := dec.Token(); err != nil { // the closing ]
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
		out.WriteString(":[")
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
		if equalStrings(bounded, list) {
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
