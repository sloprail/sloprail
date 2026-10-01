package main

import (
	"errors"
	"os"

	"github.com/sloprail/sloprail/internal/checkstore"
	"github.com/sloprail/sloprail/internal/repochecks"
	"github.com/sloprail/sloprail/internal/sessionpath"
	"github.com/sloprail/sloprail/internal/transcript"
)

var errNoSession = errors.New("sr-checks: no session to read — run this inside a session (with " + transcript.SessionIDEnv + " set) whose transcript exists")

// openFamilyChecks opens the check results of the whole session family: the repository's
// database scoped to the ROOT session's family, written by the root and by every sub-agent it
// dispatches (each run carries its agent_id; an older sub-agent's own database is imported by
// the root's first hook, see services/sr-session/results_family.go). So the family is that
// single scope, whichever agent asks about it.
//
// Read-only: nothing here creates or changes a store. checkstore.ErrNoStore when nothing was
// recorded.
func openFamilyChecks() (checkstore.Store, string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, "", err
	}
	record := transcript.CurrentSessionPath(cwd)
	if record == "" {
		return nil, "", errNoSession
	}
	family, err := sessionpath.FamilyID(record, cwd)
	if err != nil {
		return nil, "", err
	}
	store, err := repochecks.OpenReadOnly(sessionpath.StateCwd(record, cwd), family)
	if err != nil {
		return nil, "", err
	}
	return store, store.Path(), nil
}

// queryFamily runs one SELECT over the family's results and returns all the rows, each with
// `_store` naming the database it came from. A store that cannot be read is an error rather
// than a silent gap: a gate asking whether anything is refused must not take an unreadable
// store for an empty one.
func queryFamily(sql string) ([]map[string]any, error) {
	rows := []map[string]any{}
	s, path, err := openFamilyChecks()
	if errors.Is(err, checkstore.ErrNoStore) {
		return rows, nil
	}
	if err != nil {
		return nil, err
	}
	defer s.Close()
	got, err := s.Query(sql)
	if err != nil {
		return nil, err
	}
	for _, r := range got {
		r["_store"] = path
		rows = append(rows, r)
	}
	return rows, nil
}
