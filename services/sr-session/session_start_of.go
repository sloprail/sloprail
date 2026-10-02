package main

import (
	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/sessionstate"
)

// sessionStartOf is the HEAD the session FIRST began at (never the re-taken baseline, which
// an amend or a branch switch moves), or "" when none was kept: no state, or a session that
// began before it was kept. The declaration load reads it as the commit whose config.yaml
// may switch off a protected rule.
func sessionStartOf(state sessionstate.Store) string {
	if state == nil {
		return ""
	}
	start, ok, err := state.Meta(sessionstate.MetaSessionStart)
	if err != nil || !ok {
		return ""
	}
	if start == sessionstate.SessionStartUnborn {
		return gitrepo.EmptyTree
	}
	return start
}
