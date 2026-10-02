package checkcache

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sloprail/sloprail/internal/sessionpath"

	// The driver for reading an older engine's checks.db.
	_ "modernc.org/sqlite"
)

// ReadLegacyDB reads every run, check and item of the sqlite check-results database at src,
// the checks.db an older engine kept, as runs (oldest first), tagging each with agentID when
// the source says none. src is only read.
func ReadLegacyDB(src, agentID string) ([]Run, error) {
	if _, err := os.Stat(src); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+url.PathEscape(src)+"?mode=ro&_pragma=busy_timeout(5000)&_pragma=query_only(ON)")
	if err != nil {
		return nil, fmt.Errorf("checkcache: open %s: %w", src, err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	hasAgent, err := hasColumn(db, "check_runs", "agent_id")
	if err != nil {
		return nil, fmt.Errorf("checkcache: read %s: %w", src, err)
	}
	agentExpr := "''"
	if hasAgent {
		agentExpr = "agent_id"
	}
	rows, err := db.Query(`SELECT id, run_batch_id, run_at, check_id, repo_id, branch, session_id, ` + agentExpr + `,
		base_ref, head_ref, exit_code, COALESCE(error, ''), metadata FROM check_runs ORDER BY run_at, rowid`)
	if err != nil {
		return nil, fmt.Errorf("checkcache: read %s: %w", src, err)
	}
	var runs []*Run
	byID := map[string]*Run{}
	for rows.Next() {
		var r Run
		var meta string
		if err := rows.Scan(&r.ID, &r.BatchID, &r.RunAt, &r.Rule, &r.RepoID, &r.Branch, &r.SessionID, &r.AgentID,
			&r.BaseRef, &r.HeadRef, &r.ExitCode, &r.Error, &meta); err != nil {
			rows.Close()
			return nil, err
		}
		r.Metadata = decodeMeta(meta)
		r.Complete = r.Metadata["state"] == "complete"
		delete(r.Metadata, "state")
		if h, ok := r.Metadata["ruleHash"].(string); ok {
			r.RuleHash = h
			delete(r.Metadata, "ruleHash")
		}
		if r.AgentID == "" {
			r.AgentID = agentID
		}
		r.Checks = []Check{}
		runs = append(runs, &r)
		byID[r.ID] = &r
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()

	type at struct {
		run *Run
		i   int
	}
	checkIdx := map[string]at{} // check id -> where it sits in its run
	crows, err := db.Query(`SELECT id, run_id, subject, kind, status, COALESCE(fingerprint, ''), metadata FROM checks ORDER BY rowid`)
	if err != nil {
		return nil, fmt.Errorf("checkcache: read %s: %w", src, err)
	}
	for crows.Next() {
		var id, runID, meta string
		var c Check
		if err := crows.Scan(&id, &runID, &c.Subject, &c.Kind, &c.Status, &c.Fingerprint, &meta); err != nil {
			crows.Close()
			return nil, err
		}
		c.Metadata = decodeMeta(meta)
		if r, ok := byID[runID]; ok {
			r.Checks = append(r.Checks, c)
			checkIdx[id] = at{r, len(r.Checks) - 1}
		}
	}
	if err := crows.Err(); err != nil {
		return nil, err
	}
	crows.Close()
	irows, err := db.Query(`SELECT check_id, COALESCE(key, ''), passed, metadata FROM check_items ORDER BY rowid`)
	if err != nil {
		return nil, fmt.Errorf("checkcache: read %s: %w", src, err)
	}
	for irows.Next() {
		var checkID, meta string
		var it Item
		if err := irows.Scan(&checkID, &it.Key, &it.Passed, &meta); err != nil {
			irows.Close()
			return nil, err
		}
		it.Metadata = decodeMeta(meta)
		if c, ok := checkIdx[checkID]; ok {
			c.run.Checks[c.i].Items = append(c.run.Checks[c.i].Items, it)
		}
	}
	if err := irows.Err(); err != nil {
		return nil, err
	}
	irows.Close()
	out := make([]Run, len(runs))
	for i, r := range runs {
		out[i] = *r
	}
	return out, nil
}

func decodeMeta(s string) map[string]any {
	m := map[string]any{}
	_ = json.Unmarshal([]byte(s), &m)
	return m
}

func hasColumn(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.Query(`SELECT 1 FROM pragma_table_info('`+table+`') WHERE name = ?`, column)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	return rows.Next(), rows.Err()
}

// legacyDBs lists every checks.db an older engine kept under the sessions dir:
// {data home}/sloprail/sessions/<workspace>/<session>/checks.db (and one level deeper, for a
// sub-agent's), in a stable order. Which of them belong to this repository is decided by
// the caller.
func legacyDBs() []string {
	home, err := sessionpath.DataHome()
	if err != nil {
		return nil
	}
	base := filepath.Join(home, sessionpath.AppName, "sessions")
	var hits []string
	for _, pat := range []string{"*/*/checks.db", "*/*/*/checks.db"} {
		m, _ := filepath.Glob(filepath.Join(base, pat))
		hits = append(hits, m...)
	}
	sort.Strings(hits)
	return hits
}

// worktreeWorkspaces is the encoded workspace names of every worktree of the repository
// (the sessions dir is keyed by them).
func (s *Store) worktreeWorkspaces() map[string]bool {
	out := map[string]bool{}
	if top, err := s.g.str("rev-parse", "--show-toplevel"); err == nil {
		out[sessionpath.EncodeWorkspace(top)] = true
	}
	if txt, err := s.g.str("worktree", "list", "--porcelain"); err == nil {
		for _, line := range strings.Split(txt, "\n") {
			if p, ok := strings.CutPrefix(line, "worktree "); ok {
				out[sessionpath.EncodeWorkspace(p)] = true
			}
		}
	}
	return out
}

// rootCommits are the repository's root commits: the repo_id an older engine recorded.
func (s *Store) rootCommits() map[string]bool {
	out := map[string]bool{}
	if txt, err := s.g.str("rev-list", "--max-parents=0", "HEAD"); err == nil {
		for _, l := range strings.Fields(txt) {
			out[l] = true
		}
	}
	return out
}

// MigrateLegacy is the migration of an older engine's sqlite checks.db files into the ref. It
// imports EVERY old session store that belongs to this repository (every worktree's, a
// removed worktree's too: a store belongs when its workspace is one of the repository's
// worktrees, or when its runs record the repository's root commit). Only a write path calls
// it (`sr-checks run`): opening a store never migrates. It is idempotent per store: a marker
// in the git common dir records each store imported, and a run the ref already holds is never
// written again. The old files are only read and stay where they are. A database that cannot
// be read is skipped, and does not stop the others: the old engine's verdicts were keyed by an
// older fingerprint schema, so the history is what is kept, not hits.
func (s *Store) MigrateLegacy() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.migrateLegacy()
}

func (s *Store) migrateLegacy() {
	if s.cachePath() == "" {
		return
	}
	var pending []string
	for _, db := range legacyDBs() {
		if _, err := os.Stat(s.migrationMarker(db)); err != nil {
			pending = append(pending, db)
		}
	}
	if len(pending) == 0 {
		return
	}
	workspaces, roots := s.worktreeWorkspaces(), s.rootCommits()
	have := map[string]bool{}
	if sn, err := s.snapshotAt(s.tip()); err == nil && len(sn.Segs) > 0 {
		if known, err := s.runsOf(sn); err == nil {
			for _, r := range known {
				have[r.ID] = true
			}
		}
	}
	for _, db := range pending {
		rs, err := ReadLegacyDB(db, "")
		if err != nil {
			continue
		}
		ours := workspaces[filepath.Base(filepath.Dir(filepath.Dir(db)))] || workspaces[filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(db))))]
		var kept []Run
		for _, r := range rs {
			if (ours || roots[r.RepoID]) && !have[r.ID] {
				kept = append(kept, r)
			}
		}
		if len(kept) > 0 {
			sort.SliceStable(kept, func(i, j int) bool { return kept[i].ID < kept[j].ID })
			if err := s.put(kept); err != nil {
				continue // not marked: the next run tries again
			}
			for _, r := range kept {
				have[r.ID] = true
			}
		}
		if m := s.migrationMarker(db); os.MkdirAll(filepath.Dir(m), 0o755) == nil {
			_ = os.WriteFile(m, []byte("migrated\n"), 0o644)
		}
	}
}

// migrationMarker is the file that records the old store at db was imported.
func (s *Store) migrationMarker(db string) string {
	p := s.cachePath()
	if p == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(db))
	return filepath.Join(filepath.Dir(p), "legacy-checks-migrated-"+hex.EncodeToString(sum[:6]))
}
