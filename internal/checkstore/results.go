package checkstore

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Check statuses, as the checks table spells them.
const (
	StatusPass        = "pass"
	StatusFail        = "fail"
	StatusSkip        = "skip"
	StatusError       = "error"
	StatusInterrupted = "interrupted"
)

// RunIdentity says whose run it is. Filled although the database is per session
// today, so rows can move to a global store unchanged.
type RunIdentity struct {
	// RepoID is the repository's root-commit SHA: it survives worktrees and branches.
	RepoID    string
	Branch    string
	SessionID string
	// AgentID is the sub-agent that ran it, "" for the root session itself.
	AgentID string
	// Family is the root session of the session family the run belongs to: what a store opened
	// for a family (OpenFamily) reads, and what keeps one session's open refusals and owed work
	// its own in a database every session of the repository writes. A store opened for a family
	// fills it itself.
	Family string
	// Folder is the working tree the run judged in (its git root).
	Folder string
}

// CheckRun is one rule evaluated once, over one commit range.
type CheckRun struct {
	RunIdentity
	// BatchID groups the runs of one Stop.
	BatchID string
	// CheckID is the rule's qualified name (<plugin>/file-guard/<name>).
	CheckID string
	BaseRef string
	HeadRef string
	// ExitCode and Error record an ENGINE failure — a git error, a range that
	// could not be computed. Such a run passes nothing and moves nothing: it is
	// never read as an empty range.
	ExitCode int
	Error    string
	// Metadata is {ruleHash, eventKind, baseOrigin, droppedWatermark}. ruleHash is
	// how a run is tied to the rule definition it ran under.
	Metadata map[string]any
	// Complete says the run is already finished when it is recorded: an engine
	// failure, or a range where `match` selected nothing. Any other run is
	// recorded RUNNING and finished with FinishRun once every check is stored.
	//
	// The distinction is what keeps a run that died half-way (a crash, a kill, a
	// judge that never came back) from reading as a pass: a run with no checks
	// yet is indistinguishable from one with nothing to check, and only a
	// finished run is ever a watermark.
	Complete bool
}

// CheckRecord is one check of a run.
type CheckRecord struct {
	Subject string
	// Kind names the check inside the rule: check[0]:script:./x.sh,
	// check[1]:judge:./rubric.md.j2, require:citation.
	Kind   string
	Status string
	// Fingerprint is the cache key; "" is stored as NULL and always re-runs.
	Fingerprint string
	// Metadata is {reasoning, files, model, prompt, ...}.
	Metadata map[string]any
	Items    []CheckItem
}

// CheckItem is one finding inside a check.
type CheckItem struct {
	Key      string
	Passed   bool
	Metadata map[string]any
}

// CachedCheck is a stored pass or fail found by fingerprint.
type CachedCheck struct {
	Status   string
	Metadata map[string]any
}

// CheckStatusRow is one check of a rule's latest run — or, for a run that failed
// as an engine, the run itself (Kind empty, Status "error").
type CheckStatusRow struct {
	Rule        string         `json:"rule"`
	Subject     string         `json:"subject"`
	Kind        string         `json:"kind"`
	Status      string         `json:"status"`
	BaseRef     string         `json:"base_ref"`
	HeadRef     string         `json:"head_ref"`
	RunAt       string         `json:"run_at"`
	Fingerprint string         `json:"fingerprint,omitempty"`
	Error       string         `json:"error,omitempty"`
	Metadata    map[string]any `json:"metadata"`
}

func newID(prefix string) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err) // the system's randomness failing is not a state to carry on in
	}
	return prefix + "_" + hex.EncodeToString(b)
}

// stamp is a sortable UTC timestamp.
func stamp() string { return time.Now().UTC().Format("2006-01-02T15:04:05.000000000Z") }

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

func (s *store) RecordRun(r CheckRun) (string, error) {
	db, err := s.conn()
	if err != nil {
		return "", err
	}
	state := map[string]any{"state": runRunning}
	if r.Complete {
		state["state"] = runComplete
	}
	for k, v := range r.Metadata {
		state[k] = v
	}
	meta, err := encode(state)
	if err != nil {
		return "", fmt.Errorf("checkstore: encode run metadata: %w", err)
	}
	var runErr any
	if r.Error != "" {
		runErr = r.Error
	}
	id := newID("run")
	if s.family != "" {
		_, err = db.Exec(`
		INSERT INTO main.check_runs (id, run_batch_id, run_at, check_id, repo_id, branch, session_id, agent_id,
		                        family, folder, base_ref, head_ref, exit_code, error, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, r.BatchID, stamp(), r.CheckID, r.RepoID, r.Branch, r.SessionID, r.AgentID,
			s.family, r.Folder, r.BaseRef, r.HeadRef, r.ExitCode, runErr, meta)
	} else {
		_, err = db.Exec(`
		INSERT INTO main.check_runs (id, run_batch_id, run_at, check_id, repo_id, branch, session_id, agent_id,
		                        base_ref, head_ref, exit_code, error, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, r.BatchID, stamp(), r.CheckID, r.RepoID, r.Branch, r.SessionID, r.AgentID,
			r.BaseRef, r.HeadRef, r.ExitCode, runErr, meta)
	}
	if err != nil {
		return "", fmt.Errorf("checkstore: record run of %q: %w", r.CheckID, err)
	}
	return id, nil
}

// Run states, in a run's metadata.
const (
	runRunning  = "running"
	runComplete = "complete"
)

// FinishRun marks a run complete: every check it was going to run is stored.
func (s *store) FinishRun(runID string) error {
	db, err := s.conn()
	if err != nil {
		return err
	}
	res, err := db.Exec(`UPDATE main.check_runs SET metadata = json_set(metadata, '$.state', ?) WHERE id = ?`, runComplete, runID)
	if err != nil {
		return fmt.Errorf("checkstore: finish run: %w", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("checkstore: finish run: no run %q", runID)
	}
	return nil
}

func (s *store) RecordCheck(runID string, c CheckRecord) (string, error) {
	if !validStatus(c.Status) {
		return "", fmt.Errorf("checkstore: %q is not a check status", c.Status)
	}
	db, err := s.conn()
	if err != nil {
		return "", err
	}
	meta, err := encode(c.Metadata)
	if err != nil {
		return "", fmt.Errorf("checkstore: encode check metadata: %w", err)
	}
	var fp any
	if c.Fingerprint != "" {
		fp = c.Fingerprint
	}
	tx, err := db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	id := newID("chk")
	if _, err := tx.Exec(`
		INSERT INTO main.checks (id, run_id, subject, kind, status, fingerprint, metadata, checked_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (run_id, subject, kind) DO UPDATE SET
			status = excluded.status, fingerprint = excluded.fingerprint,
			metadata = excluded.metadata, checked_at = excluded.checked_at`,
		id, runID, c.Subject, c.Kind, c.Status, fp, meta, stamp()); err != nil {
		return "", fmt.Errorf("checkstore: record check %q: %w", c.Kind, err)
	}
	// On a conflict the row kept its own id; the items belong to that one.
	if err := tx.QueryRow(`SELECT id FROM checks WHERE run_id = ? AND subject = ? AND kind = ?`,
		runID, c.Subject, c.Kind).Scan(&id); err != nil {
		return "", err
	}
	if _, err := tx.Exec(`DELETE FROM main.check_items WHERE check_id = ?`, id); err != nil {
		return "", err
	}
	for _, it := range c.Items {
		imeta, err := encode(it.Metadata)
		if err != nil {
			return "", fmt.Errorf("checkstore: encode item metadata: %w", err)
		}
		var key any
		if it.Key != "" {
			key = it.Key
		}
		if _, err := tx.Exec(`
			INSERT INTO main.check_items (id, check_id, key, passed, metadata, checked_at)
			VALUES (?, ?, ?, ?, ?, ?)`, newID("itm"), id, key, it.Passed, imeta, stamp()); err != nil {
			return "", fmt.Errorf("checkstore: record item of %q: %w", c.Kind, err)
		}
	}
	return id, tx.Commit()
}

func validStatus(s string) bool {
	switch s {
	case StatusPass, StatusFail, StatusSkip, StatusError, StatusInterrupted:
		return true
	}
	return false
}

// CachedCheck is a10n's CacheHit on (subject, kind, fingerprint), extended to
// read a fail as well as a pass: a fail is terminal, and replaying it is what
// keeps a judge from being asked again about input that has not changed.
func (s *store) CachedCheck(subject, kind, fingerprint string) (CachedCheck, bool, error) {
	if fingerprint == "" {
		return CachedCheck{}, false, nil
	}
	db, err := s.conn()
	if err != nil {
		return CachedCheck{}, false, err
	}
	var c CachedCheck
	var meta string
	err = db.QueryRow(`
		SELECT status, metadata FROM checks
		WHERE subject = ? AND kind = ? AND fingerprint = ? AND status IN ('pass', 'fail')
		ORDER BY checked_at DESC, rowid DESC LIMIT 1`, subject, kind, fingerprint).Scan(&c.Status, &meta)
	if errors.Is(err, sql.ErrNoRows) && s.family != "" {
		// Another session of this repository judged exactly this input (the fingerprint is the
		// rule's whole folder, the model and the exact input) and passed it: a pass is a fact
		// about that content, so it stands here too. Only a PASS crosses: a refusal stays the
		// family that was refused.
		err = db.QueryRow(`
			SELECT status, metadata FROM main.checks
			WHERE subject = ? AND kind = ? AND fingerprint = ? AND status = 'pass'
			ORDER BY checked_at DESC, rowid DESC LIMIT 1`, subject, kind, fingerprint).Scan(&c.Status, &meta)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return CachedCheck{}, false, nil
	}
	if err != nil {
		return CachedCheck{}, false, fmt.Errorf("checkstore: cache lookup: %w", err)
	}
	c.Metadata = decode(meta)
	return c, true, nil
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
func (s *store) ResolveStale(rule, ruleHash, liveRunID string) (int, error) {
	db, err := s.conn()
	if err != nil {
		return 0, err
	}
	res, err := db.Exec(`
		UPDATE main.checks
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
		stamp(), rule, ruleHash, liveRunID, liveRunID, liveRunID, liveRunID)
	if err != nil {
		return 0, fmt.Errorf("checkstore: resolve stale for %q: %w", rule, err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// RunRef is one run's range and when it ran (RunAt is a fixed-width UTC stamp, so
// stamps from different databases compare as strings).
type RunRef struct {
	Base, Head, RunAt string
}

// RunRefs is a rule's refused and passed runs, at any rule hash. Failed holds a run
// that is an engine failure or holds a failing check (a failure that went stale is
// not one); Passed holds a COMPLETE run with no failing check and no engine error. A
// run with no recorded range is in neither.
type RunRefs struct {
	Failed, Passed []RunRef
}

func (s *store) RunRefs(rule string) (RunRefs, error) {
	return s.runRefs(rule, "check_runs", "checks", "", nil)
}

// SiblingRunRefs is RunRefs over the OTHER session families of the repository that judged in
// the same working tree: what an earlier or concurrent session of this worktree was refused
// for, or passed. Only a store opened for a family has siblings.
func (s *store) SiblingRunRefs(rule, folder string) (RunRefs, error) {
	if s.family == "" || folder == "" {
		return RunRefs{}, nil
	}
	return s.runRefs(rule, "main.check_runs", "main.checks", " AND cr.family <> ? AND cr.folder = ?", []any{s.family, folder})
}

// SiblingRefs is implemented by a store opened for a family.
type SiblingRefs interface {
	SiblingRunRefs(rule, folder string) (RunRefs, error)
}

func (s *store) runRefs(rule, runs, checks, where string, extra []any) (RunRefs, error) {
	db, err := s.conn()
	if err != nil {
		return RunRefs{}, err
	}
	var out RunRefs
	collect := func(query string, into *[]RunRef) error {
		rows, err := db.Query(query, append([]any{rule}, extra...)...)
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
		SELECT cr.base_ref, cr.head_ref, cr.run_at FROM `+runs+` cr
		WHERE cr.check_id = ? AND cr.base_ref <> '' AND cr.head_ref <> ''`+where+`
		  AND (cr.error IS NOT NULL OR cr.exit_code <> 0
		       OR EXISTS (SELECT 1 FROM `+checks+` c WHERE c.run_id = cr.id
		                  AND c.status IN ('fail', 'error', 'interrupted')))
		ORDER BY cr.run_at`, &out.Failed); err != nil {
		return RunRefs{}, err
	}
	if err := collect(`
		SELECT cr.base_ref, cr.head_ref, cr.run_at FROM `+runs+` cr
		WHERE cr.check_id = ? AND cr.base_ref <> '' AND cr.head_ref <> ''`+where+`
		  AND cr.exit_code = 0 AND cr.error IS NULL
		  AND json_extract(cr.metadata, '$.state') = 'complete'
		  AND NOT EXISTS (SELECT 1 FROM `+checks+` c WHERE c.run_id = cr.id
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
	db, err := s.conn()
	if err != nil {
		return nil, err
	}
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
	db, err := s.conn()
	if err != nil {
		return nil, err
	}
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
	db, err := s.conn()
	if err != nil {
		return nil, err
	}
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
