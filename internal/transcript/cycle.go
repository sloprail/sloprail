package transcript

import "encoding/json"

// Where a cycle's work begins.
//
// A session's record only grows, so a turn already judged has not changed and
// judging it again is waste that compounds — by the twentieth cycle, nineteen
// turns are re-read every time. Worse than the waste: a judge is a model call
// rather than a function, so re-reading settled work invites a different
// verdict on a turn the agent can no longer reach to fix.
//
// Where the last cycle stopped is a POSITION, and a position is remembered
// rather than derived: nothing in the record itself says which turns have
// already been judged, because that is a fact about what sloprail did, not
// about what the agent did. It belongs in the engine's own per-session state,
// alongside the baseline commit — which is a separate piece of work and a
// separate store. This package takes the position as an input and says what
// follows it; what remembers it, and the rule that it only moves when a cycle
// finished, live where that state does.

// Since narrows entries to those after the entry with the given uuid.
//
// The mark is the uuid of the last entry the previous cycle read. Entries up to
// and including it are behind the mark; everything after it is this cycle's
// work.
//
// An empty mark means nothing has been read yet, so everything is this cycle's
// — a first cycle sees the whole session, which is correct rather than a
// special case. A mark naming an entry that is not here also yields everything:
// the record it pointed into is gone or is another conversation's, and the safe
// reading of a lost position is to look again rather than to skip. Re-reading a
// turn costs a second look; skipping one loses a violation for good.
func Since(entries []Entry, mark string) []Entry {
	if mark == "" {
		return entries
	}
	for i, e := range entries {
		if e.UUID == mark {
			return entries[i+1:]
		}
	}
	return entries
}

// Mark is the position a cycle read up to — the uuid of the last entry it saw,
// for the next cycle to resume after. Empty when there was nothing to read,
// which leaves the previous mark standing rather than resetting it.
func Mark(entries []Entry) string {
	if len(entries) == 0 {
		return ""
	}
	return entries[len(entries)-1].UUID
}

// decode turns a raw field into something an expression can reach into. Invalid
// or absent JSON reads as nothing, so an expression referring to it sees an
// absent value rather than failing — a rule asking about a tool result on an
// entry that has none is asking a fair question with the answer "no".
func decode(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	return v
}
