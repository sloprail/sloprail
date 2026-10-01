package main

import (
	"errors"
	"os"
	"path/filepath"
	"sort"

	"github.com/sloprail/sloprail/internal/checkstore"
	"github.com/sloprail/sloprail/internal/sessionpath"
	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/sloprail/sloprail/internal/transcript"
)

var errNoSession = errors.New("sr-checks: no session to read — run this inside a session (with " + transcript.SessionIDEnv + " set) whose transcript exists")

// familyChecks lists the check-result databases of the whole session family: the root
// session's own and those of every agent that worked for it.
//
// A sub-agent is a session of its own (see sessionpath.StateDB), keyed under its own
// worktree when it was dispatched into one, so its Stop's verdicts live in a store the
// coordinator's own `sql` never reads. The family is therefore every `checks.db` under
//
//   - the root session's workspace directory (the root, its shared-tree sub-agents, and
//     the earlier sessions of the same tree), and
//   - the workspace directory of every folder the root's registry holds for the session
//     (each sub-agent's own worktree).
//
// Read-only: nothing here creates or changes a store. A store that cannot be listed is
// an error; one that is simply absent is not.
func familyChecks() ([]string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	record := transcript.CurrentSessionPath(cwd)
	if record == "" {
		return nil, errNoSession
	}
	id, err := sessionpath.StableIdentity(record, cwd)
	if err != nil {
		return nil, err
	}
	state, err := sessionpath.StateDB(cwd, id.ID)
	if err != nil {
		return nil, err
	}
	sessions := filepath.Dir(filepath.Dir(state)) // .../sessions/<workspace>
	dirs := map[string]bool{sessions: true}
	if _, serr := os.Stat(state); serr == nil {
		reg, err := sessionstate.Open(state)
		if err != nil {
			return nil, err
		}
		folders, ferr := reg.Folders(id.ID)
		reg.Close()
		if ferr != nil {
			return nil, ferr
		}
		for _, f := range folders {
			dirs[filepath.Join(filepath.Dir(sessions), sessionpath.EncodeWorkspace(f.Path))] = true
		}
	} else if !errors.Is(serr, os.ErrNotExist) {
		return nil, serr
	}

	var out []string
	for d := range dirs {
		found, err := filepath.Glob(filepath.Join(d, "*", "checks.db"))
		if err != nil {
			return nil, err
		}
		out = append(out, found...)
	}
	sort.Strings(out)
	return out, nil
}

// queryFamily runs one SELECT over every store of the family and returns all the rows,
// each with `_store` naming the database it came from. A store that cannot be read is an
// error rather than a silent gap: a gate asking whether anything is refused must not
// take an unreadable store for an empty one.
func queryFamily(sql string) ([]map[string]any, error) {
	paths, err := familyChecks()
	if err != nil {
		return nil, err
	}
	rows := []map[string]any{}
	for _, p := range paths {
		s, err := checkstore.OpenReadOnly(p)
		if errors.Is(err, checkstore.ErrNoStore) {
			continue
		}
		if err != nil {
			return nil, err
		}
		got, err := s.Query(sql)
		s.Close()
		if err != nil {
			return nil, err
		}
		for _, r := range got {
			r["_store"] = p
			rows = append(rows, r)
		}
	}
	return rows, nil
}
