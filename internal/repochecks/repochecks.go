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

	"github.com/sloprail/sloprail/internal/checkstore"
	"github.com/sloprail/sloprail/internal/sessionpath"
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
	srcs := append(extra, legacySources(cwd, family)...)
	if err := checkstore.ImportLegacy(store, srcs); err != nil && warn != nil {
		fmt.Fprintln(warn, "sloprail: some earlier check results were not imported:", err)
	}
	return store, nil
}

// OpenReadOnly opens the family's check results for reading. checkstore.ErrNoStore when
// nothing was ever recorded for the repository (and nothing is waiting to be imported).
func OpenReadOnly(cwd, family string) (checkstore.Store, error) {
	path, err := sessionpath.RepoChecksDB(cwd)
	if err != nil {
		return nil, err
	}
	hadOwn := false
	if srcs := legacySources(cwd, family); len(srcs) > 0 {
		for _, l := range srcs {
			hadOwn = hadOwn || l.Family == family
		}
		// Reading is never allowed to change what a session concluded, but bringing the old
		// layout across is only a copy: do it before reading, so a reader sees the same rows the
		// writer will.
		if w, err := checkstore.OpenFamily(path, family); err == nil {
			_ = checkstore.ImportLegacy(w, srcs)
			w.Close()
		}
	}
	store, err := checkstore.OpenFamilyReadOnly(path, family)
	if err != nil || hadOwn {
		return store, err
	}
	// The database is the repository's, so it exists once any session recorded anything: for
	// THIS family, no runs is still "nothing recorded yet".
	rows, qerr := store.Query("select count(*) as n from check_runs")
	if qerr == nil && len(rows) == 1 {
		if n, ok := rows[0]["n"].(int64); ok && n == 0 {
			store.Close()
			return nil, checkstore.ErrNoStore
		}
	}
	return store, nil
}
