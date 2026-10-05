package sessionstate

import (
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// What a session remembers about its folders and branches is bounded by what still exists.
//
// A long session registers a folder for every worktree and scratch repository its agents touch,
// and records, per branch and folder, the tip it saw. None of that is dropped when the worktree is
// removed or the branch deleted, and the registry is read at every Stop (a row per range, per
// folder, per agent), so a session of weeks made every hook slower than the one before.
//
// PruneGone drops what can no longer matter: rows for folders that are gone (an untracked range
// there decides nothing; no tracked range or live agent refers to the folder), observations of
// branches that are gone, the housekeeping rows the engine itself untracked (reason "pruned: …")
// for branches that are gone, and a memo past its cap. Everything it keeps is what a hook can
// still ask about: tracked ranges, live folders, and what was observed of live branches (without
// which a branch the session has seen would look new and be tracked again).
//
// What goes with a gone folder is gone with it, a decision included: an untracked range there
// judged nothing, and the folder is not coming back. One side effect is accepted: a branch only a
// gone sibling folder had recorded no longer reads as "recorded elsewhere" to a sub-agent
// worktree met late, which errs toward tracking it, never away from it.

const (
	// pruneEvery is how often a hook pays for it: it reads the registry and stats each folder.
	pruneEvery = 10 * time.Minute
	// metaPrunedAt is when PruneGone last ran, in Unix seconds.
	metaPrunedAt = "pruned_at"
	// maxVerifyMemos is how many remembered verify answers a session keeps. The memo is keyed by a
	// digest of everything an answer depends on, so an entry for a tip that moved is never asked
	// for again and there is no order to evict by: past the cap the memo starts afresh, and the
	// next Stop re-verifies what it needs.
	maxVerifyMemos = 1024
	// maxPrunedRows is how many housekeeping rows a session keeps before it asks git which of their
	// branches still exist.
	maxPrunedRows = 256
	// maxObservations is the same for observed tips.
	maxObservations = 4096
)

const (
	observedTipKey    = "observed-tip:"
	foreignCoverKey   = "foreign-cover:"
	observedFolderKey = "observed-folder:"
	verifyMemoKey     = "verify-memo:"
)

// legacyMetaPrefixes are keys older engines wrote and no code reads any more: the recorded refs
// layout (since replaced by rows), the import of the old check store.
var legacyMetaPrefixes = []string{"refs_at_start:", "refs_snapshot:", "ref_start:", "folder_home:", "orphan_origin:", "orphan_adopted:", "retired_tips:", "checks_imported:"}

// legacyMetaKeys are such keys that stand alone.
var legacyMetaKeys = []string{MetaRefsSchema, "cited_unknown", "observed-session-begun"}

// PruneGone drops what the session remembers of folders and branches that no longer exist. It is
// cheap to ask: it runs once per pruneEvery.
func (s *store) PruneGone() error {
	db, err := s.conn()
	if err != nil {
		return err
	}
	var last string
	if err := db.QueryRow("SELECT value FROM meta WHERE key = ?", metaPrunedAt).Scan(&last); err == nil {
		if at, err := strconv.ParseInt(last, 10, 64); err == nil && time.Since(time.Unix(at, 0)) < pruneEvery {
			return nil
		}
	}
	if err := pruneGone(db, false); err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
		metaPrunedAt, strconv.FormatInt(time.Now().Unix(), 10))
	return err
}

// pruneRegistry is the engine-run step of migration 010: the same pruning, asking git about every
// branch, so a store written before the bound shrinks on its first open.
func pruneRegistry(db *sql.DB) error { return pruneGone(db, true) }

type folderFacts struct {
	exists   map[string]bool
	branches map[string]map[string]bool // folder -> local branches; nil entry: unreadable
}

func (f *folderFacts) live(folder string) bool {
	if v, ok := f.exists[folder]; ok {
		return v
	}
	st, err := os.Stat(folder)
	v := err == nil && st.IsDir()
	f.exists[folder] = v
	return v
}

// localBranches is the folder's local branches, nil when git cannot say.
func (f *folderFacts) localBranches(folder string) map[string]bool {
	if b, ok := f.branches[folder]; ok {
		return b
	}
	var out map[string]bool
	if b, err := exec.Command("git", "-C", folder, "for-each-ref", "--format=%(refname)", "refs/heads").Output(); err == nil {
		out = map[string]bool{}
		for _, ln := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			if ln != "" {
				out[strings.TrimPrefix(ln, "refs/heads/")] = true
			}
		}
	}
	f.branches[folder] = out
	return out
}

func pruneGone(db *sql.DB, askGit bool) error {
	facts := &folderFacts{exists: map[string]bool{}, branches: map[string]map[string]bool{}}
	prefetch(db, facts, askGit)
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("sessionstate: prune: %w", err)
	}
	defer tx.Rollback()

	del := func(q string, args ...any) error {
		if _, err := tx.Exec(q, args...); err != nil {
			return fmt.Errorf("sessionstate: prune: %w", err)
		}
		return nil
	}
	strs := func(q string, args ...any) ([][]string, error) {
		rows, err := tx.Query(q, args...)
		if err != nil {
			return nil, fmt.Errorf("sessionstate: prune: %w", err)
		}
		defer rows.Close()
		var out [][]string
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]string, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				return nil, fmt.Errorf("sessionstate: prune: %w", err)
			}
			out = append(out, vals)
		}
		return out, rows.Err()
	}
	count := func(q string, args ...any) int {
		var n int
		_ = tx.QueryRow(q, args...).Scan(&n)
		return n
	}

	// Untracked ranges, folders and agent folders of folders that are gone.
	folders, err := strs(`SELECT DISTINCT folder FROM session_refs UNION SELECT DISTINCT path FROM session_folders UNION SELECT DISTINCT folder FROM session_agent_folders`)
	if err != nil {
		return err
	}
	for _, r := range folders {
		folder := r[0]
		if facts.live(folder) {
			continue
		}
		if err := del(`DELETE FROM session_refs WHERE folder = ? AND untracked_reason <> ''`, folder); err != nil {
			return err
		}
		if err := del(`DELETE FROM session_agent_folders WHERE folder = ?`, folder); err != nil {
			return err
		}
		// A folder a tracked range still names is verified at the commit it last held.
		if err := del(`DELETE FROM session_folders WHERE path = ? AND role <> 'root'
			AND NOT EXISTS (SELECT 1 FROM session_refs r WHERE r.folder = session_folders.path AND r.untracked_reason = '')`, folder); err != nil {
			return err
		}
	}

	// What was observed in folders that are gone, and of branches that are gone.
	obs, err := strs(`SELECT key FROM meta WHERE key >= ? AND key < ? OR key >= ? AND key < ? OR key >= ? AND key < ?`,
		observedTipKey, prefixEnd(observedTipKey), foreignCoverKey, prefixEnd(foreignCoverKey), observedFolderKey, prefixEnd(observedFolderKey))
	if err != nil {
		return err
	}
	checkBranches := askGit || len(obs) > maxObservations
	for _, r := range obs {
		key := r[0]
		var folder, branch string
		switch {
		case strings.HasPrefix(key, observedFolderKey):
			folder = strings.TrimPrefix(key, observedFolderKey)
		case strings.HasPrefix(key, observedTipKey), strings.HasPrefix(key, foreignCoverKey):
			rest := key[strings.Index(key, ":")+1:]
			// <branch>:<folder>: a branch name holds no colon.
			b, f, ok := strings.Cut(rest, ":")
			if !ok {
				continue
			}
			branch, folder = b, f
		}
		if !facts.live(folder) {
			if err := del(`DELETE FROM meta WHERE key = ?`, key); err != nil {
				return err
			}
			continue
		}
		if branch != "" && branch != "(detached)" && checkBranches {
			if have := facts.localBranches(folder); have != nil && !have[branch] {
				if err := del(`DELETE FROM meta WHERE key = ?`, key); err != nil {
					return err
				}
			}
		}
	}

	// The housekeeping rows the engine untracked itself, for branches that are gone.
	if askGit || count(`SELECT COUNT(*) FROM session_refs WHERE untracked_reason LIKE 'pruned:%'`) > maxPrunedRows {
		rows, err := strs(`SELECT folder, ref FROM session_refs WHERE untracked_reason LIKE 'pruned:%'`)
		if err != nil {
			return err
		}
		for _, r := range rows {
			folder, ref := r[0], r[1]
			if !facts.live(folder) || strings.HasPrefix(ref, "detached/") || isObjectName(ref) {
				continue
			}
			name, isBranch := strings.CutPrefix(ref, "refs/heads/")
			if !isBranch && strings.HasPrefix(ref, "refs/") {
				continue
			}
			if !isBranch {
				name = ref
			}
			if have := facts.localBranches(folder); have != nil && !have[name] {
				if err := del(`DELETE FROM session_refs WHERE folder = ? AND ref = ? AND untracked_reason LIKE 'pruned:%'`, folder, ref); err != nil {
					return err
				}
			}
		}
	}

	// A memo past its cap, and keys nothing reads any more.
	if count(`SELECT COUNT(*) FROM meta WHERE key >= ? AND key < ?`, verifyMemoKey, prefixEnd(verifyMemoKey)) > maxVerifyMemos {
		if err := del(`DELETE FROM meta WHERE key >= ? AND key < ?`, verifyMemoKey, prefixEnd(verifyMemoKey)); err != nil {
			return err
		}
	}
	for _, p := range legacyMetaPrefixes {
		if err := del(`DELETE FROM meta WHERE key >= ? AND key < ?`, p, prefixEnd(p)); err != nil {
			return err
		}
	}
	for _, k := range legacyMetaKeys {
		if err := del(`DELETE FROM meta WHERE key = ?`, k); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sessionstate: prune: %w", err)
	}
	reclaimIfFree(db)
	return nil
}

// prefetch asks for the branches of every live folder the registry names, before the write lock
// is taken: a process per folder must not be run while every other hook of the session waits. Only
// when the pruning will need them (the migration, or a registry past its bounds).
func prefetch(db *sql.DB, facts *folderFacts, force bool) {
	var obs, pruned int
	_ = db.QueryRow(`SELECT COUNT(*) FROM meta WHERE key >= ? AND key < ?`, observedTipKey, prefixEnd(observedTipKey)).Scan(&obs)
	_ = db.QueryRow(`SELECT COUNT(*) FROM session_refs WHERE untracked_reason LIKE 'pruned:%'`).Scan(&pruned)
	if !force && obs <= maxObservations && pruned <= maxPrunedRows {
		return
	}
	rows, err := db.Query(`SELECT folder FROM session_refs WHERE untracked_reason LIKE 'pruned:%'
		UNION SELECT substr(substr(key, ?), instr(substr(key, ?), ':') + 1) FROM meta WHERE key >= ? AND key < ?
		UNION SELECT substr(substr(key, ?), instr(substr(key, ?), ':') + 1) FROM meta WHERE key >= ? AND key < ?`,
		len(observedTipKey)+1, len(observedTipKey)+1, observedTipKey, prefixEnd(observedTipKey),
		len(foreignCoverKey)+1, len(foreignCoverKey)+1, foreignCoverKey, prefixEnd(foreignCoverKey))
	if err != nil {
		return
	}
	var folders []string
	for rows.Next() {
		var f string
		if rows.Scan(&f) == nil {
			folders = append(folders, f)
		}
	}
	rows.Close()
	for _, f := range folders {
		if facts.live(f) {
			facts.localBranches(f)
		}
	}
}

// prefixEnd is the first key after every key that starts with prefix: the prefix with its last byte
// raised. (A bound like prefix+"\x7f" would sort below a key whose next byte is not ASCII.)
func prefixEnd(prefix string) string {
	b := []byte(prefix)
	b[len(b)-1]++
	return string(b)
}

// isObjectName reports whether s is a full object id (a range whose head is a bare commit).
func isObjectName(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}
