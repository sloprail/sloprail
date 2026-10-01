package main

import (
	"errors"
	"os"

	"github.com/sloprail/sloprail/internal/checkstore"
	"github.com/sloprail/sloprail/internal/sessionpath"
	"github.com/sloprail/sloprail/internal/transcript"
)

var errNoSession = errors.New("sr-checks: no session to read — run this inside a session (with " + transcript.SessionIDEnv + " set) whose transcript exists")

// familyCheckStores lists the check-result databases of the whole session family. There is
// ONE: the root session's checks.db, written by the root and by every sub-agent it
// dispatches (each run carries its agent_id; an older sub-agent's own database is imported
// into it by the root's first hook, see services/sr-session/results_family.go). So the
// family is that single store, whichever agent asks about it.
//
// Read-only: nothing here creates or changes a store. A store that is simply absent is not
// an error.
func familyCheckStores() ([]string, error) {
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
	path, err := sessionpath.ChecksDB(cwd, id.ID)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return []string{path}, nil
}

// queryFamily runs one SELECT over every store of the family and returns all the rows,
// each with `_store` naming the database it came from. A store that cannot be read is an
// error rather than a silent gap: a gate asking whether anything is refused must not
// take an unreadable store for an empty one.
func queryFamily(sql string) ([]map[string]any, error) {
	paths, err := familyCheckStores()
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
