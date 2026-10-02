package checkstore

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/sloprail/sloprail/internal/checkcache"

	// The in-memory database the queries run on is this package's resource, so the driver is
	// its import.
	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

// resolvesKey is the metadata key of a run that only resolves stale checks (see ResolveStale).
const resolvesKey = "resolves"

// resolution names one check of an earlier run that ResolveStale turned into a skip.
type resolution struct {
	Run     string `json:"run"`
	Subject string `json:"subject"`
	Kind    string `json:"kind"`
}

func resolutionsOf(r checkcache.Run) []resolution {
	raw, ok := r.Metadata[resolvesKey]
	if !ok {
		return nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var out []resolution
	_ = json.Unmarshal(b, &out)
	return out
}

// runs is every run the store can see, oldest first: the backend's history under what this
// process recorded (which wins for a run in both).
func (s *store) allRuns() ([]checkcache.Run, error) {
	stored, err := s.cache.Runs()
	if err != nil {
		return nil, fmt.Errorf("checkstore: read runs: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	have := map[string]bool{}
	var out []checkcache.Run
	for _, r := range s.runs {
		have[r.ID] = true
	}
	for i := len(stored) - 1; i >= 0; i-- { // the backend lists newest first
		if !have[stored[i].ID] {
			out = append(out, stored[i])
		}
	}
	for _, r := range s.runs {
		out = append(out, *r)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].RunAt < out[j].RunAt })
	return out, nil
}

// view builds the check_runs, checks and check_items tables of schema.sql, in memory, from
// the runs the store can see; done discards them. A run that only resolves stale checks is
// not a run: it turns the checks it names into skips.
func (s *store) view() (db *sql.DB, done func(), err error) {
	runs, err := s.allRuns()
	if err != nil {
		return nil, nil, err
	}
	db, err = sql.Open("sqlite", ":memory:?_pragma=foreign_keys(ON)")
	if err != nil {
		return nil, nil, fmt.Errorf("checkstore: open view: %w", err)
	}
	db.SetMaxOpenConns(1) // every connection to :memory: is a database of its own
	done = func() { db.Close() }
	if _, err := db.Exec(schema); err != nil {
		done()
		return nil, nil, fmt.Errorf("checkstore: apply schema: %w", err)
	}
	type at struct{ run, subject, kind string }
	resolved := map[at]bool{}
	for _, r := range runs {
		for _, m := range resolutionsOf(r) {
			resolved[at{m.Run, m.Subject, m.Kind}] = true
		}
	}
	tx, err := db.Begin()
	if err != nil {
		done()
		return nil, nil, err
	}
	defer tx.Rollback()
	for _, r := range runs {
		if _, only := r.Metadata[resolvesKey]; only {
			continue
		}
		state := map[string]any{"state": runRunning}
		if r.Complete {
			state["state"] = runComplete
		}
		for k, v := range r.Metadata {
			state[k] = v
		}
		if r.RuleHash != "" {
			state["ruleHash"] = r.RuleHash
		}
		meta, err := encode(state)
		if err != nil {
			done()
			return nil, nil, fmt.Errorf("checkstore: encode run metadata: %w", err)
		}
		var runErr any
		if r.Error != "" {
			runErr = r.Error
		}
		if _, err := tx.Exec(`
			INSERT INTO check_runs (id, run_batch_id, run_at, check_id, repo_id, branch, session_id, agent_id,
			                        base_ref, head_ref, exit_code, error, metadata)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.ID, r.BatchID, r.RunAt, r.Rule, r.RepoID, r.Branch, r.SessionID, r.AgentID,
			r.BaseRef, r.HeadRef, r.ExitCode, runErr, meta); err != nil {
			done()
			return nil, nil, fmt.Errorf("checkstore: load run %q: %w", r.ID, err)
		}
		for ci, c := range r.Checks {
			status, cmeta := c.Status, c.Metadata
			if resolved[at{r.ID, c.Subject, c.Kind}] && status == StatusFail {
				cm := map[string]any{}
				for k, v := range c.Metadata {
					cm[k] = v
				}
				cm["reason"], cm["staleFrom"] = "stale: its input is no longer in the range", StatusFail
				status, cmeta = StatusSkip, cm
			}
			cm, err := encode(cmeta)
			if err != nil {
				done()
				return nil, nil, fmt.Errorf("checkstore: encode check metadata: %w", err)
			}
			var fp any
			if c.Fingerprint != "" {
				fp = c.Fingerprint
			}
			id := fmt.Sprintf("%s/%d", r.ID, ci)
			if _, err := tx.Exec(`INSERT INTO checks (id, run_id, subject, kind, status, fingerprint, metadata, checked_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, id, r.ID, c.Subject, c.Kind, status, fp, cm, r.RunAt); err != nil {
				done()
				return nil, nil, fmt.Errorf("checkstore: load check %q: %w", c.Kind, err)
			}
			for ii, it := range c.Items {
				im, err := encode(it.Metadata)
				if err != nil {
					done()
					return nil, nil, fmt.Errorf("checkstore: encode item metadata: %w", err)
				}
				var key any
				if it.Key != "" {
					key = it.Key
				}
				if _, err := tx.Exec(`INSERT INTO check_items (id, check_id, key, passed, metadata, checked_at)
					VALUES (?, ?, ?, ?, ?, ?)`, fmt.Sprintf("%s/%d", id, ii), id, key, it.Passed, im, r.RunAt); err != nil {
					done()
					return nil, nil, fmt.Errorf("checkstore: load item of %q: %w", c.Kind, err)
				}
			}
		}
	}
	if err := tx.Commit(); err != nil {
		done()
		return nil, nil, err
	}
	return db, done, nil
}

// Run states, in a run's metadata.
const (
	runRunning  = "running"
	runComplete = "complete"
)

func encode(m map[string]any) (string, error) {
	if m == nil {
		m = map[string]any{}
	}
	b, err := json.Marshal(m)
	return string(b), err
}

func decode(s string) map[string]any {
	m := map[string]any{}
	_ = json.Unmarshal([]byte(s), &m)
	return m
}

// ResolveStale is a10n's ResolveStale for a rule (only COMPLETE runs: another Stop's
// run still in flight is not this evaluation's to clear): a failing check whose input is
// no longer one the live run holds is an orphan — the files it judged have left
// the range — and stays a failure forever unless it is cleared. It becomes skip,
// with the reason, and is no longer outstanding.
//
// A check without a fingerprint (a requirement, a script) has no input identity to
// compare, so its identity is (subject, kind): it stays failing while the live run
// refuses it again, or has not evaluated that kind at all (an earlier check
// refused first); it is stale once the live run passes it, or evaluates the kind
// for other subjects only (its subject left the range).
//
// The cache is append-only: the marks are one more run, recorded here and written on Close,
// that names the (run, subject, kind) of each check it resolves and carries no range of its own.
func (s *store) ResolveStale(rule, ruleHash, liveRunID string) (int, error) {
	s.mu.Lock()
	err := s.writable()
	s.mu.Unlock()
	if err != nil {
		return 0, err
	}
	db, done, err := s.view()
	if err != nil {
		return 0, err
	}
	defer done()
	at := stamp()
	_, err = db.Exec(`
		UPDATE checks
		SET status = 'skip',
		    metadata = json_set(metadata, '$.reason', 'stale: its input is no longer in the range', '$.staleFrom', 'fail'),
		    checked_at = ?
		WHERE status = 'fail'
		  AND run_id IN (SELECT id FROM check_runs
		                 WHERE check_id = ? AND json_extract(metadata, '$.ruleHash') = ? AND id <> ?
		                   AND json_extract(metadata, '$.state') = 'complete')
		  AND NOT EXISTS (SELECT 1 FROM checks live
		                  WHERE live.run_id = ? AND live.subject = checks.subject
		                    AND live.kind = checks.kind AND live.fingerprint = checks.fingerprint)
		  AND NOT (checks.fingerprint IS NULL AND (
		        EXISTS (SELECT 1 FROM checks live
		                WHERE live.run_id = ? AND live.subject = checks.subject AND live.kind = checks.kind
		                  AND live.status IN ('fail', 'error', 'interrupted'))
		        OR NOT EXISTS (SELECT 1 FROM checks live WHERE live.run_id = ? AND live.kind = checks.kind)))`,
		at, rule, ruleHash, liveRunID, liveRunID, liveRunID, liveRunID)
	if err != nil {
		return 0, fmt.Errorf("checkstore: resolve stale for %q: %w", rule, err)
	}
	rows, err := db.Query(`SELECT run_id, subject, kind, COALESCE(fingerprint, '') FROM checks WHERE checked_at = ? AND status = 'skip'`, at)
	if err != nil {
		return 0, fmt.Errorf("checkstore: resolve stale for %q: %w", rule, err)
	}
	defer rows.Close()
	var marks []resolution
	var skips []checkcache.Check // each mark's check, a skip that supersedes the fail it resolves
	for rows.Next() {
		var m resolution
		var fp string
		if err := rows.Scan(&m.Run, &m.Subject, &m.Kind, &fp); err != nil {
			return 0, err
		}
		marks = append(marks, m)
		skips = append(skips, checkcache.Check{Subject: m.Subject, Kind: m.Kind, Status: StatusSkip, Fingerprint: fp,
			Metadata: map[string]any{"reason": "stale: its input is no longer in the range", "staleFrom": StatusFail}})
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(marks) == 0 {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	run := &checkcache.Run{ID: newID("run"), RunAt: at, Rule: rule, RuleHash: ruleHash, Complete: true,
		Metadata: map[string]any{resolvesKey: marks}, Checks: skips}
	s.runs = append(s.runs, run)
	s.byID[run.ID] = run
	return len(marks), nil
}

func (s *store) RunRefs(rule string) (RunRefs, error) {
	db, done, err := s.view()
	if err != nil {
		return RunRefs{}, err
	}
	defer done()
	var out RunRefs
	collect := func(query string, into *[]RunRef) error {
		rows, err := db.Query(query, rule)
		if err != nil {
			return fmt.Errorf("checkstore: run refs for %q: %w", rule, err)
		}
		defer rows.Close()
		for rows.Next() {
			var r RunRef
			if err := rows.Scan(&r.Base, &r.Head, &r.RunAt); err != nil {
				return err
			}
			*into = append(*into, r)
		}
		return rows.Err()
	}
	if err := collect(`
		SELECT cr.base_ref, cr.head_ref, cr.run_at FROM check_runs cr
		WHERE cr.check_id = ? AND cr.base_ref <> '' AND cr.head_ref <> ''
		  AND (cr.error IS NOT NULL OR cr.exit_code <> 0
		       OR EXISTS (SELECT 1 FROM checks c WHERE c.run_id = cr.id
		                  AND c.status IN ('fail', 'error', 'interrupted')))
		ORDER BY cr.run_at`, &out.Failed); err != nil {
		return RunRefs{}, err
	}
	if err := collect(`
		SELECT cr.base_ref, cr.head_ref, cr.run_at FROM check_runs cr
		WHERE cr.check_id = ? AND cr.base_ref <> '' AND cr.head_ref <> ''
		  AND cr.exit_code = 0 AND cr.error IS NULL
		  AND json_extract(cr.metadata, '$.state') = 'complete'
		  AND NOT EXISTS (SELECT 1 FROM checks c WHERE c.run_id = cr.id
		                  AND (c.status IN ('fail', 'error', 'interrupted')
		                       OR json_extract(c.metadata, '$.staleFrom') IS NOT NULL))
		ORDER BY cr.run_at`, &out.Passed); err != nil {
		return RunRefs{}, err
	}
	return out, nil
}

// PassedHeads is a10n's EffectiveBase, kept as a list so the caller can skip the
// heads a rebase has orphaned. A run passed when it is not an engine failure and
// holds no failing check — including one whose failure was later resolved as
// stale: it failed, and clearing the orphan must not turn it into a pass; a run with no checks at all (`match` selected nothing)
// passed too, which is what lets an empty selection advance the watermark.
func (s *store) PassedHeads(rule string) ([]string, error) {
	db, done, err := s.view()
	if err != nil {
		return nil, err
	}
	defer done()
	rows, err := db.Query(`
		SELECT cr.head_ref FROM check_runs cr
		WHERE cr.check_id = ?
		  AND cr.head_ref <> '' AND cr.exit_code = 0 AND cr.error IS NULL
		  AND json_extract(cr.metadata, '$.state') = 'complete'
		  AND NOT EXISTS (SELECT 1 FROM checks c WHERE c.run_id = cr.id
		                  AND (c.status IN ('fail', 'error', 'interrupted')
		                       OR json_extract(c.metadata, '$.staleFrom') IS NOT NULL))
		ORDER BY cr.run_at DESC, cr.rowid DESC`, rule)
	if err != nil {
		return nil, fmt.Errorf("checkstore: passed heads for %q: %w", rule, err)
	}
	defer rows.Close()
	var heads []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		heads = append(heads, h)
	}
	return heads, rows.Err()
}

func (s *store) CheckStatus(failingOnly bool, rule string) ([]CheckStatusRow, error) {
	db, done, err := s.view()
	if err != nil {
		return nil, err
	}
	defer done()
	rows, err := db.Query(`
		SELECT cr.check_id, COALESCE(c.subject, ''), COALESCE(c.kind, ''),
		       CASE WHEN cr.exit_code <> 0 OR cr.error IS NOT NULL THEN COALESCE(c.status, 'error')
		            WHEN json_extract(cr.metadata, '$.state') <> 'complete' THEN 'interrupted'
		            WHEN c.id IS NULL THEN 'pass'
		            ELSE c.status END,
		       cr.base_ref, cr.head_ref, cr.run_at, COALESCE(c.fingerprint, ''), COALESCE(cr.error, ''),
		       COALESCE(c.metadata, cr.metadata)
		FROM check_runs cr
		LEFT JOIN checks c ON c.run_id = cr.id
		WHERE cr.id = (SELECT l.id FROM check_runs l WHERE l.check_id = cr.check_id
		               ORDER BY l.run_at DESC, l.rowid DESC LIMIT 1)
		  AND (? = '' OR cr.check_id = ? OR cr.check_id LIKE '%/' || ?)
		ORDER BY cr.check_id, c.subject, c.kind`, rule, rule, rule)
	if err != nil {
		return nil, fmt.Errorf("checkstore: check status: %w", err)
	}
	defer rows.Close()
	var out []CheckStatusRow
	for rows.Next() {
		var r CheckStatusRow
		var meta string
		if err := rows.Scan(&r.Rule, &r.Subject, &r.Kind, &r.Status, &r.BaseRef, &r.HeadRef, &r.RunAt, &r.Fingerprint, &r.Error, &meta); err != nil {
			return nil, err
		}
		r.Metadata = decode(meta)
		if failingOnly && r.Status != StatusFail && r.Status != StatusError && r.Status != StatusInterrupted {
			continue
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Query runs a SELECT on a connection that cannot write, so the statement's own
// text is not what stands between a caller and the session's record.
func (s *store) Query(query string) ([]map[string]any, error) {
	trimmed := strings.TrimSpace(query)
	if head := strings.ToLower(trimmed); !strings.HasPrefix(head, "select") && !strings.HasPrefix(head, "with") {
		return nil, fmt.Errorf("checkstore: only a SELECT can be run here")
	}
	db, done, err := s.view()
	if err != nil {
		return nil, err
	}
	defer done()
	conn, err := db.Conn(context.Background())
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(context.Background(), `PRAGMA query_only = ON`); err != nil {
		return nil, err
	}
	defer conn.ExecContext(context.Background(), `PRAGMA query_only = OFF`)
	rows, err := conn.QueryContext(context.Background(), trimmed)
	if err != nil {
		return nil, fmt.Errorf("checkstore: query: %w", err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(cols))
		for i, c := range cols {
			if b, ok := vals[i].([]byte); ok {
				vals[i] = string(b)
			}
			row[c] = vals[i]
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
