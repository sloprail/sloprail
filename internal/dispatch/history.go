package dispatch

import (
	"slices"
	"sort"

	"github.com/sloprail/sloprail/internal/transcript"
)

// A file's history this session, as far as citations are concerned: what a
// `citation` prerequisite at Stop needs to tell the parts of a file's change a
// citation rode on from the parts none did.
//
// A citation grounds the change it rode on and nothing else. The Post event at
// Stop is one change — the session baseline to the file as it stands — made of
// many: cited sr-file calls, uncited writes by the agent, and changes the agent
// never made at all (the file was already dirty when the session began, the
// user edited it between turns, a branch switch or a checkout filter rewrote
// it). The history lays them out in order so each requirement can ask which
// parts it leaves ungrounded.

// HistoryState is a file's content at one moment, by hash, or its absence.
type HistoryState struct {
	Exists bool
	Hash   string
}

// HistoryPoint is one step of a file's history.
type HistoryPoint struct {
	// Foreign marks a change the agent did not make: the file as it stood at
	// the start of one of the agent's cycles, when that differs from how the
	// agent left it (or, at the session's first hook, from the baseline). Such
	// a change is never charged to the agent. From is where it started; nil
	// when that is not known (the session's first hook), which charges nothing
	// before it either.
	Foreign bool
	From    *HistoryState

	// Pools are the pools the citations of a cited change resolved in. A cited
	// change grounds a requirement only when one of them is one it accepts:
	// a change cited in the wrong pool is, to that requirement, uncited.
	Pools []transcript.SourceType

	// Whole marks a cited change that stated the file's entire content (an
	// sr-file write): it grounds the file as it left it, so nothing before it
	// is left ungrounded.
	Whole bool

	Before, After HistoryState

	// At orders points across the stores a Stop merges (the session's own and
	// its sub-agents' or dispatcher's).
	At int64
}

// FileHistory is one path's history, from its session baseline to its
// current content.
type FileHistory struct {
	Baseline, Current HistoryState
	Points            []HistoryPoint

	// Content returns the content a hash names, and whether it is known.
	Content func(hash string) (string, bool)
}

// uncitedParts is every stretch of the history that no cited change counting
// for pools made and that the agent made (foreign stretches are skipped), with
// content where it is known.
func (h FileHistory) uncitedParts(pools []transcript.SourceType) []UncitedChange {
	points := append([]HistoryPoint(nil), h.Points...)
	sort.SliceStable(points, func(i, j int) bool { return points[i].At < points[j].At })

	var gaps [][2]HistoryState
	state := h.Baseline
	for _, p := range points {
		switch {
		case p.Foreign:
			if p.From != nil && *p.From != state {
				gaps = append(gaps, [2]HistoryState{state, *p.From})
			}
			state = p.After
		case !countsFor(p.Pools, pools):
			// Cited in another pool: to this requirement, an uncited change,
			// which the stretch it sits in already covers.
		case p.Whole:
			gaps = nil
			state = p.After
		default:
			if state != p.Before {
				gaps = append(gaps, [2]HistoryState{state, p.Before})
			}
			state = p.After
		}
	}
	if state != h.Current {
		gaps = append(gaps, [2]HistoryState{state, h.Current})
	}

	out := make([]UncitedChange, 0, len(gaps))
	for _, g := range gaps {
		out = append(out, UncitedChange{
			FromExists: g[0].Exists, From: h.content(g[0]),
			ToExists: g[1].Exists, To: h.content(g[1]),
		})
	}
	return out
}

// countsFor reports whether a change cited in got grounds a requirement
// accepting want.
func countsFor(got, want []transcript.SourceType) bool {
	for _, g := range got {
		if slices.Contains(want, g) {
			return true
		}
	}
	return false
}

func (h FileHistory) content(s HistoryState) string {
	if !s.Exists || h.Content == nil {
		return ""
	}
	c, _ := h.Content(s.Hash)
	return c
}
