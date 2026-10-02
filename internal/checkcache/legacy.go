package checkcache

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"

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

// legacyDBs lists the checks.db files an older engine kept for the worktree dir belongs to:
// {data home}/sloprail/sessions/<workspace>/<session>/checks.db, in a stable order.
func legacyDBs(dir string) []string {
	home, err := sessionpath.DataHome()
	if err != nil {
		return nil
	}
	hits, _ := filepath.Glob(filepath.Join(home, sessionpath.AppName, "sessions", sessionpath.EncodeWorkspace(dir), "*", "checks.db"))
	sort.Strings(hits)
	return hits
}

// migrateLegacy is the one-time migration of an older engine's sqlite checks.db files (one
// per session of this worktree) into the ref. It runs when a Store is first opened on a
// repository, under the store's lock, and is idempotent twice over: a marker in the git common
// dir records that it ran, and a run the ref already holds is never written again. The old
// files are only read and stay where they are. A database that cannot be read is skipped, never
// an error of Open, and does not stop the marker: the old engine's verdicts were keyed by an
// older fingerprint schema, so the history is what is kept, not hits.
func (s *Store) migrateLegacy() {
	marker := s.migrationMarker()
	if marker == "" {
		return
	}
	if _, err := os.Stat(marker); err == nil {
		return
	}
	dir, err := s.g.str("rev-parse", "--show-toplevel")
	if err != nil {
		return
	}
	var runs []Run
	for _, db := range legacyDBs(dir) {
		if rs, err := ReadLegacyDB(db, ""); err == nil {
			runs = append(runs, rs...)
		}
	}
	if len(runs) > 0 {
		if sn, err := s.snapshotAt(s.tip()); err == nil && len(sn.Segs) > 0 {
			have := map[string]bool{}
			if known, err := s.runsOf(sn); err == nil {
				for _, r := range known {
					have[r.ID] = true
				}
			}
			kept := runs[:0]
			for _, r := range runs {
				if !have[r.ID] {
					kept = append(kept, r)
				}
			}
			runs = kept
		}
		if len(runs) > 0 {
			sort.SliceStable(runs, func(i, j int) bool { return runs[i].ID < runs[j].ID })
			if err := s.put(runs); err != nil {
				return // not marked: the next open tries again
			}
		}
	}
	if os.MkdirAll(filepath.Dir(marker), 0o755) == nil {
		_ = os.WriteFile(marker, []byte("migrated\n"), 0o644)
	}
}

func (s *Store) migrationMarker() string {
	p := s.cachePath()
	if p == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(p), "legacy-checks-migrated")
}
