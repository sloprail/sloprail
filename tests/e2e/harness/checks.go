package harness

import (
	"path/filepath"

	"github.com/sloprail/sloprail/internal/checkstore"
)

// Seeding the session's check results.
//
// What the Stop evaluation records, a test can record directly: the readers
// (`sr-checks`, and the watermark `sr-session changeset` derives) are the
// behaviour under test, and they read rows, not the run that wrote them. Opened
// the way harness.Meta opens the state store — the engine's own resolution of
// where the session's directory is, so a test cannot pass against a database the
// engine would never have read.

// checksDBPath is the session's check-results database, beside its state.db.
func (e *Env) checksDBPath(projDir, sessionID string) string {
	return filepath.Join(filepath.Dir(e.sessionDBPath(projDir, sessionID)), "checks.db")
}

// RecordCheckRun writes one rule's run and its checks into the session's check
// results and returns the run's id. Identity columns not set on run are filled
// with fixed test values; the batch defaults to one shared batch.
func (e *Env) RecordCheckRun(projDir, sessionID string, run checkstore.CheckRun, checks ...checkstore.CheckRecord) string {
	e.t.Helper()
	store, err := checkstore.Open(e.checksDBPath(projDir, sessionID))
	if err != nil {
		e.t.Fatalf("harness: open check results: %v", err)
	}
	defer store.Close()
	if run.RepoID == "" {
		run.RepoID = "e2e-repo"
	}
	if run.Branch == "" {
		run.Branch = "main"
	}
	if run.SessionID == "" {
		run.SessionID = sessionID
	}
	if run.BatchID == "" {
		run.BatchID = "e2e-batch"
	}
	id, err := store.RecordRun(run)
	if err != nil {
		e.t.Fatalf("harness: record run: %v", err)
	}
	for _, c := range checks {
		if _, err := store.RecordCheck(id, c); err != nil {
			e.t.Fatalf("harness: record check: %v", err)
		}
	}
	return id
}
