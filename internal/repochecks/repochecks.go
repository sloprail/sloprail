// Package repochecks opens the check-results database of a repository for one session family.
//
// There is one database per repository (sessionpath.RepoChecksDB), written by every session
// and agent that works in it. A family is scoped to its own runs (checkstore.OpenFamily), so
// what a session was refused for and what it still owes stay its own, while a pass on exactly
// the same input stands for any session.
//
// MIGRATION happens here, on open: the check results an older engine kept per session
// (sessionpath.ChecksDB: sessions/<workspace>/<session>/checks.db) are imported into the
// repository's database, the session's own and those of the other sessions of its worktree.
// Idempotent, safe when two sessions open at once, and the old files are never changed: a
// session that began under the old engine continues at its next hook with everything it had,
// and a file an older engine writes to later is read again.
package repochecks

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/sloprail/sloprail/internal/checkstore"
	"github.com/sloprail/sloprail/internal/sessionpath"
	"github.com/sloprail/sloprail/internal/transcript"
)

// legacySources is every per-session database the older layout holds for the worktree cwd
// belongs to: the family's own, and its siblings' (the other session directories beside it).
func legacySources(cwd, family string) []checkstore.Legacy {
	own, err := sessionpath.ChecksDB(cwd, family)
	if err != nil {
		return nil
	}
	folder := sessionpath.WorkspaceAnchor(cwd)
	var out []checkstore.Legacy
	if _, err := os.Stat(own); err == nil {
		out = append(out, checkstore.Legacy{Path: own, Family: family, Folder: folder})
	}
	siblings, _ := filepath.Glob(filepath.Join(filepath.Dir(filepath.Dir(own)), "*", "checks.db"))
	for _, p := range siblings {
		if p == own {
			continue
		}
		out = append(out, checkstore.Legacy{Path: p, Family: filepath.Base(filepath.Dir(p)), Folder: folder})
	}
	return out
}

// Open opens the repository's database read-write for the family, importing what the older
// layout holds (and extra: databases the caller knows belong to the family, such as a
// sub-agent's own). A failure to import is reported on warn and costs a judgement made again,
// never the evaluation.
func Open(cwd, family string, warn io.Writer, extra ...checkstore.Legacy) (checkstore.Store, error) {
	path, err := sessionpath.RepoChecksDB(cwd)
	if err != nil {
		return nil, err
	}
	store, err := checkstore.OpenFamily(path, family)
	if err != nil {
		return nil, err
	}
	// A source the caller knows the family of (a sub-agent's own database) wins over the same
	// path found by the directory scan, which can only guess the family from the directory name.
	seen := map[string]bool{}
	var srcs []checkstore.Legacy
	for _, l := range append(extra, legacySources(cwd, family)...) {
		if !seen[l.Path] {
			seen[l.Path] = true
			srcs = append(srcs, l)
		}
	}
	postponed, err := checkstore.ImportLegacyReport(store, srcs)
	if err != nil && warn != nil {
		fmt.Fprintln(warn, "sloprail: some earlier check results were not imported:", err)
	}
	// Whatever is not in the repository database in full (postponed, or failed) keeps counting
	// for the evaluation: its open refusals are read from the old files.
	pending := checkstore.NotImported(path, srcs)
	checkstore.SetPendingLegacy(store, pending)
	// The family's own old database is being written by a Stop of an older engine this very
	// moment: this cycle keeps using it as it is (nothing it recorded is hidden), and the next
	// hook migrates it.
	for _, p := range postponed {
		for _, l := range srcs {
			if l.Path == p && l.Family == family && l.Agent == "" {
				store.Close()
				legacy, err := checkstore.OpenLegacy(p, path, family)
				if err == nil {
					checkstore.SetPendingLegacy(legacy, pending)
				}
				return legacy, err
			}
		}
	}
	return store, nil
}

// OpenReadOnly opens the family's check results for reading, and WRITES NOTHING: no migration,
// no import, no schema step (a reader must never fail a Stop's RecordRun by holding a lock, or
// change what a session concluded). Where the family's results are still in its old per-session
// file — the repository's database does not exist yet, holds nothing of this family, or an
// older engine's Stop is writing the old file right now — that file is read as it is.
// checkstore.ErrNoStore when nothing was recorded.
func OpenReadOnly(cwd, family string, extra ...checkstore.Legacy) (checkstore.Store, error) {
	path, err := sessionpath.RepoChecksDB(cwd)
	if err != nil {
		return nil, err
	}
	// The family's old file, while the repository's database does not hold all of it (never
	// imported, or written since): the reader sees BOTH, deduplicated by run id, the repository's
	// row winning.
	var old []checkstore.Legacy
	if own, err := sessionpath.ChecksDB(cwd, family); err == nil {
		if _, statErr := os.Stat(own); statErr == nil && !checkstore.Imported(path, own) {
			old = append(old, checkstore.Legacy{Path: own, Family: family, Folder: sessionpath.WorkspaceAnchor(cwd)})
		}
	}
	// The family's sub-agents' old files (the caller knows them, see SubagentSources) not in the
	// repository database in full yet are read the same way.
	for _, l := range extra {
		if _, statErr := os.Stat(l.Path); statErr == nil && !checkstore.Imported(path, l.Path) {
			old = append(old, l)
		}
	}
	if len(old) > 0 {
		return checkstore.OpenUnionReadOnly(path, family, old)
	}
	store, err := checkstore.OpenFamilyReadOnly(path, family)
	if err != nil {
		return nil, err
	}
	// The database is the repository's, so it exists once any session recorded anything: for
	// THIS family, no runs is "nothing recorded yet".
	rows, qerr := store.Query("select count(*) as n from check_runs")
	if qerr == nil && len(rows) == 1 {
		if n, ok := rows[0]["n"].(int64); ok && n == 0 {
			store.Close()
			return nil, checkstore.ErrNoStore
		}
	}
	return store, nil
}

// SubagentSources is the old per-session check-results databases of the sub-agents dispatched
// under a root session's record, as sources of that family (each tagged with its agent), for a
// reader that must see their open refusals before they are migrated. Best effort: a record that
// cannot be read is left out.
func SubagentSources(rootRecord, family string) []checkstore.Legacy {
	if rootRecord == "" {
		return nil
	}
	recs, _ := filepath.Glob(filepath.Join(strings.TrimSuffix(rootRecord, ".jsonl"), "subagents", "agent-*.jsonl"))
	var out []checkstore.Legacy
	for _, rec := range recs {
		agent := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(rec), "agent-"), ".jsonl")
		cwd, err := transcript.StartCwd(rec)
		if err != nil || cwd == "" {
			continue
		}
		id, err := sessionpath.StableIdentity(rec, cwd)
		if err != nil {
			continue
		}
		if path, err := sessionpath.ChecksDB(cwd, id.ID); err == nil {
			out = append(out, checkstore.Legacy{Path: path, Family: family, Agent: agent, Folder: sessionpath.WorkspaceAnchor(cwd)})
		}
	}
	return out
}

// RootRecord is the record of the root session a record belongs to: a sub-agent's record is
// nested under the session that dispatched it.
func RootRecord(record string) string {
	if dir := transcript.SessionDirOfSubagent(record); dir != "" {
		return dir + ".jsonl"
	}
	return record
}
