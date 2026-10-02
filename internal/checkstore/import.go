package checkstore

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"

	"github.com/sloprail/sloprail/internal/checkcache"
)

// Import copies every run, check and item of the sqlite check-results database at src — the
// checks.db an older engine kept — into dst, tagging each run with agentID when the source
// says none: the migration path from the old store to the cache, and the rows an older engine
// kept in a sub-agent's own database. Idempotent (a run keeps its id and is skipped when dst
// already holds it), and src is only read: it is left in place, untouched. Returns how many
// runs were new to dst. dst must be a store opened by this package; the runs are written to
// its cache when it is closed.
func Import(dst Store, src, agentID string) (int, error) {
	s, ok := dst.(*store)
	if !ok {
		return 0, fmt.Errorf("checkstore: Import needs a store opened by this package")
	}
	s.mu.Lock()
	err := s.writable()
	s.mu.Unlock()
	if err != nil {
		return 0, err
	}
	if _, err := os.Stat(src); err != nil {
		return 0, err
	}
	db, err := sql.Open("sqlite", "file:"+url.PathEscape(src)+"?mode=ro&_pragma=busy_timeout(5000)&_pragma=query_only(ON)")
	if err != nil {
		return 0, fmt.Errorf("checkstore: open %s: %w", src, err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	hasAgent, err := hasColumn(db, "check_runs", "agent_id")
	if err != nil {
		return 0, fmt.Errorf("checkstore: read %s: %w", src, err)
	}
	agentExpr := "''"
	if hasAgent {
		agentExpr = "agent_id"
	}
	rows, err := db.Query(`SELECT id, run_batch_id, run_at, check_id, repo_id, branch, session_id, ` + agentExpr + `,
		base_ref, head_ref, exit_code, COALESCE(error, ''), metadata FROM check_runs ORDER BY run_at, rowid`)
	if err != nil {
		return 0, fmt.Errorf("checkstore: read %s: %w", src, err)
	}
	var runs []*checkcache.Run
	byID := map[string]*checkcache.Run{}
	for rows.Next() {
		var r checkcache.Run
		var meta string
		if err := rows.Scan(&r.ID, &r.BatchID, &r.RunAt, &r.Rule, &r.RepoID, &r.Branch, &r.SessionID, &r.AgentID,
			&r.BaseRef, &r.HeadRef, &r.ExitCode, &r.Error, &meta); err != nil {
			rows.Close()
			return 0, err
		}
		r.Metadata = decode(meta)
		r.Complete = r.Metadata["state"] == runComplete
		delete(r.Metadata, "state")
		if h, ok := r.Metadata["ruleHash"].(string); ok {
			r.RuleHash = h
			delete(r.Metadata, "ruleHash")
		}
		if r.AgentID == "" {
			r.AgentID = agentID
		}
		r.Checks = []checkcache.Check{}
		runs = append(runs, &r)
		byID[r.ID] = &r
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	rows.Close()

	type at struct {
		run *checkcache.Run
		i   int
	}
	checkIdx := map[string]at{} // check id -> where it sits in its run
	crows, err := db.Query(`SELECT id, run_id, subject, kind, status, COALESCE(fingerprint, ''), metadata FROM checks ORDER BY rowid`)
	if err != nil {
		return 0, fmt.Errorf("checkstore: read %s: %w", src, err)
	}
	for crows.Next() {
		var id, runID, meta string
		var c checkcache.Check
		if err := crows.Scan(&id, &runID, &c.Subject, &c.Kind, &c.Status, &c.Fingerprint, &meta); err != nil {
			crows.Close()
			return 0, err
		}
		c.Metadata = decode(meta)
		if r, ok := byID[runID]; ok {
			r.Checks = append(r.Checks, c)
			checkIdx[id] = at{r, len(r.Checks) - 1}
		}
	}
	if err := crows.Err(); err != nil {
		return 0, err
	}
	crows.Close()
	irows, err := db.Query(`SELECT check_id, COALESCE(key, ''), passed, metadata FROM check_items ORDER BY rowid`)
	if err != nil {
		return 0, fmt.Errorf("checkstore: read %s: %w", src, err)
	}
	for irows.Next() {
		var checkID, meta string
		var it checkcache.Item
		if err := irows.Scan(&checkID, &it.Key, &it.Passed, &meta); err != nil {
			irows.Close()
			return 0, err
		}
		it.Metadata = decode(meta)
		if c, ok := checkIdx[checkID]; ok {
			c.run.Checks[c.i].Items = append(c.run.Checks[c.i].Items, it)
		}
	}
	if err := irows.Err(); err != nil {
		return 0, err
	}
	irows.Close()

	known, err := s.allRuns()
	if err != nil {
		return 0, err
	}
	have := map[string]bool{}
	for _, r := range known {
		have[r.ID] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, r := range runs {
		if have[r.ID] {
			continue
		}
		s.runs = append(s.runs, r)
		s.byID[r.ID] = r
		n++
	}
	return n, nil
}

func hasColumn(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.Query(`SELECT 1 FROM pragma_table_info('`+table+`') WHERE name = ?`, column)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	return rows.Next(), rows.Err()
}
