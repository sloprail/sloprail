package main

import (
	"encoding/json"
	"os"

	"github.com/sloprail/sloprail/internal/sessionpath"
	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/sloprail/sloprail/internal/transcript"
)

// recordedCitations is the citations this session, and the sessions sharing its tree (a root's
// sub-agents, a sub-agent's root and siblings), recorded per file with `sr-file --cite`: what a
// citation refusal hands back as the trailer to paste. It is read where the refusal is made
// (`run`), because `verify` only replays what `run` stored.
func recordedCitations(s session, root string) map[string][]transcript.Citation {
	out := map[string][]transcript.Citation{}
	read := func(store sessionstate.Store) {
		raw, ok, err := store.Meta(sessionstate.MetaCitations)
		if err != nil || !ok || raw == "" {
			return
		}
		var points map[string][]struct {
			Cites []transcript.Citation `json:"cites"`
		}
		if json.Unmarshal([]byte(raw), &points) != nil {
			return
		}
		for path, pts := range points {
			for _, pt := range pts {
				out[path] = append(out[path], pt.Cites...)
			}
		}
	}
	if s.state != nil {
		read(s.state)
	}
	if s.record == "" {
		return out
	}
	top := transcript.SessionRootOf(s.record)
	if top == "" {
		top = s.record
	}
	var others []string
	if top != s.record {
		others = append(others, top)
	}
	subs, _ := transcript.DescendantSubagentPaths(top)
	for _, sub := range subs {
		if sub != s.record {
			others = append(others, sub)
		}
	}
	for _, rec := range others {
		id, err := sessionpath.StableIdentity(rec, root)
		if err != nil {
			continue
		}
		db, err := sessionpath.StateDB(sessionpath.StateCwd(rec, root), id.ID)
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
		read(store)
		store.Close()
	}
	return out
}
