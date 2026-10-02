package harness

import (
	"os"
	"path/filepath"

	"github.com/sloprail/sloprail/internal/checkstore"
	"github.com/sloprail/sloprail/internal/sessionpath"
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
	// Where the engine reads the session's results: the repository's database, as this
	// session's family.
	family := e.SessionIdentity(projDir, sessionID)
	if family == "" {
		e.t.Fatalf("harness: no identity for session %s", sessionID)
	}
	store, err := checkstore.OpenFamily(sessionpath.RepoChecksDBUnder(dataHome(e.home), projDir), family)
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
	if err := store.FinishRun(id); err != nil {
		e.t.Fatalf("harness: finish run: %v", err)
	}
	return id
}

// RemoveCheckResults deletes the session's check results, as if nothing had been
// evaluated yet. The mock session that starts a test ends with a Stop, and that
// Stop evaluates the file-guards and records the runs — which a test about the
// range a rule has NOT yet been judged over must not inherit.
func (e *Env) RemoveCheckResults(projDir, sessionID string) {
	e.t.Helper()
	// The session's own database of an older layout, and the repository's (every session's
	// results live in one, keyed by the repository: this Env's data home holds only its own).
	paths := []string{e.checksDBPath(projDir, sessionID)}
	repoDBs, _ := filepath.Glob(filepath.Join(dataHome(e.home), "sloprail", "repos", "*", "checks.db"))
	paths = append(paths, repoDBs...)
	for _, path := range paths {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			if err := os.Remove(path + suffix); err != nil && !os.IsNotExist(err) {
				e.t.Fatalf("harness: remove check results: %v", err)
			}
		}
	}
}
