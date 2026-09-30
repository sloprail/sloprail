package sessionstate

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// VerdictKey names one verdict: everything it depends on and nothing it does
// not. See migrations/002_changeset_verdicts.sql for what each part is for.
type VerdictKey struct {
	Rule        string
	RuleHash    string
	Check       string
	Subject     string
	Model       string
	Fingerprint string
}

// ChangesetVerdict is what a rule concluded about one input.
type ChangesetVerdict struct {
	Passed    bool
	Reasoning string
	// Files are the paths a failing verdict names, if it names any.
	Files []string
	// Stale reports a failure whose input is not the live one. It never applies
	// to a pass, and it never makes a verdict unreadable: a stale fail is still
	// returned by ChangesetVerdict, so a returning input finds its verdict.
	Stale bool
}

// ChangesetVerdict reads the verdict stored under key. Absent is not an error.
func (s *store) ChangesetVerdict(k VerdictKey) (ChangesetVerdict, bool, error) {
	db, err := s.conn()
	if err != nil {
		return ChangesetVerdict{}, false, err
	}
	var v ChangesetVerdict
	var files string
	err = db.QueryRow(`
		SELECT passed, reasoning, files, stale FROM changeset_verdicts
		WHERE rule = ? AND rule_hash = ? AND "check" = ? AND subject = ? AND model = ? AND fingerprint = ?`,
		k.Rule, k.RuleHash, k.Check, k.Subject, k.Model, k.Fingerprint).
		Scan(&v.Passed, &v.Reasoning, &files, &v.Stale)
	if errors.Is(err, sql.ErrNoRows) {
		return ChangesetVerdict{}, false, nil
	}
	if err != nil {
		return ChangesetVerdict{}, false, fmt.Errorf("sessionstate: read verdict for %q: %w", k.Rule, err)
	}
	if err := json.Unmarshal([]byte(files), &v.Files); err != nil {
		return ChangesetVerdict{}, false, fmt.Errorf("sessionstate: verdict for %q names unreadable files: %w", k.Rule, err)
	}
	return v, true, nil
}

// RecordChangesetVerdict stores a verdict under key, live (not stale).
//
// Recording over an existing key replaces it, which only ever happens for an
// input that has not been judged before or whose stored verdict was cleared;
// a stored FAIL is never re-judged, so nothing overwrites one with a pass.
func (s *store) RecordChangesetVerdict(k VerdictKey, v ChangesetVerdict) error {
	db, err := s.conn()
	if err != nil {
		return err
	}
	files := v.Files
	if files == nil {
		files = []string{}
	}
	encoded, err := json.Marshal(files)
	if err != nil {
		return fmt.Errorf("sessionstate: encode verdict files: %w", err)
	}
	_, err = db.Exec(`
		INSERT INTO changeset_verdicts (rule, rule_hash, "check", subject, model, fingerprint, passed, reasoning, files, stale)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0)
		ON CONFLICT (rule, rule_hash, "check", subject, model, fingerprint) DO UPDATE SET
			passed = excluded.passed, reasoning = excluded.reasoning, files = excluded.files, stale = 0`,
		k.Rule, k.RuleHash, k.Check, k.Subject, k.Model, k.Fingerprint, v.Passed, v.Reasoning, string(encoded))
	if err != nil {
		return fmt.Errorf("sessionstate: write verdict for %q: %w", k.Rule, err)
	}
	return nil
}

// MarkChangesetStale reconciles one rule's stored failures with the inputs it
// has NOW: a failure whose fingerprint is in live is live again, every other
// failure of that rule at that hash is stale.
//
// Called with the fingerprints of the current run, not per verdict, because
// staleness is a fact about the whole set: a failure is orphaned exactly when
// none of the current inputs is it. a10n left such failures as permanent
// orphans, and its agents edited the database by hand to get rid of them.
func (s *store) MarkChangesetStale(rule, ruleHash string, live []string) error {
	db, err := s.conn()
	if err != nil {
		return err
	}
	query := `UPDATE changeset_verdicts SET stale = CASE WHEN fingerprint IN (` +
		placeholders(len(live)) + `) THEN 0 ELSE 1 END
		WHERE rule = ? AND rule_hash = ? AND passed = 0`
	args := make([]any, 0, len(live)+2)
	for _, f := range live {
		args = append(args, f)
	}
	args = append(args, rule, ruleHash)
	if _, err := db.Exec(query, args...); err != nil {
		return fmt.Errorf("sessionstate: mark stale verdicts for %q: %w", rule, err)
	}
	return nil
}

// OutstandingChangesetFailures lists a rule's failures that still stand: not
// passed, not stale. In a stable order, so a report does not reshuffle.
func (s *store) OutstandingChangesetFailures(rule, ruleHash string) ([]StoredFailure, error) {
	db, err := s.conn()
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(`
		SELECT "check", subject, model, fingerprint, reasoning, files FROM changeset_verdicts
		WHERE rule = ? AND rule_hash = ? AND passed = 0 AND stale = 0
		ORDER BY "check", subject, model, fingerprint`, rule, ruleHash)
	if err != nil {
		return nil, fmt.Errorf("sessionstate: read outstanding failures for %q: %w", rule, err)
	}
	defer rows.Close()
	var out []StoredFailure
	for rows.Next() {
		var f StoredFailure
		var files string
		if err := rows.Scan(&f.Key.Check, &f.Key.Subject, &f.Key.Model, &f.Key.Fingerprint, &f.Reasoning, &files); err != nil {
			return nil, fmt.Errorf("sessionstate: read outstanding failure for %q: %w", rule, err)
		}
		if err := json.Unmarshal([]byte(files), &f.Files); err != nil {
			return nil, fmt.Errorf("sessionstate: failure for %q names unreadable files: %w", rule, err)
		}
		f.Key.Rule, f.Key.RuleHash = rule, ruleHash
		out = append(out, f)
	}
	return out, rows.Err()
}

// StoredFailure is one failing verdict that still stands.
type StoredFailure struct {
	Key       VerdictKey
	Reasoning string
	Files     []string
}

// Watermark reads the last head rule passed at this definition hash.
func (s *store) Watermark(rule, ruleHash string) (string, bool, error) {
	db, err := s.conn()
	if err != nil {
		return "", false, err
	}
	var head string
	err = db.QueryRow(`SELECT head FROM rule_watermarks WHERE rule = ? AND rule_hash = ?`, rule, ruleHash).Scan(&head)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("sessionstate: read watermark for %q: %w", rule, err)
	}
	return head, true, nil
}

// SetWatermark records that rule passed at head. Only a real pass moves it —
// callers never advance it on an error or an unresolved range.
func (s *store) SetWatermark(rule, ruleHash, head string) error {
	db, err := s.conn()
	if err != nil {
		return err
	}
	_, err = db.Exec(`
		INSERT INTO rule_watermarks (rule, rule_hash, head) VALUES (?, ?, ?)
		ON CONFLICT (rule, rule_hash) DO UPDATE SET head = excluded.head`, rule, ruleHash, head)
	if err != nil {
		return fmt.Errorf("sessionstate: write watermark for %q: %w", rule, err)
	}
	return nil
}

func placeholders(n int) string {
	if n == 0 {
		// `IN ()` is a syntax error in some engines; a subquery with no rows is
		// the same statement, "no fingerprint is live".
		return "SELECT NULL WHERE 0"
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}
