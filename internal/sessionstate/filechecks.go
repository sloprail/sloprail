package sessionstate

import (
	"database/sql"
	"errors"
	"fmt"
)

// FileCheck reads one guardrail's verdict on one file.
//
// Absent is not an error. "Never judged" and "judged and passed" are both
// ordinary answers at the start of a cycle, and the caller distinguishes them
// by the boolean rather than by inspecting an error.
func (s *store) FileCheck(path, guardrail string) (Verdict, bool, error) {
	db, err := s.conn()
	if err != nil {
		return Verdict{}, false, err
	}
	var v Verdict
	err = db.QueryRow(`
		SELECT fingerprint, passed FROM file_checks
		WHERE path = ? AND guardrail = ?
		ORDER BY seq DESC LIMIT 1`, path, guardrail).Scan(&v.Fingerprint, &v.Passed)
	if errors.Is(err, sql.ErrNoRows) {
		return Verdict{}, false, nil
	}
	if err != nil {
		return Verdict{}, false, fmt.Errorf("sessionstate: read check %q/%q: %w", path, guardrail, err)
	}
	return v, true, nil
}

// RecordFileCheck stores a verdict for one guardrail's judgement of one version
// of one file's content.
//
// A row PER FINGERPRINT, not one per (path, guardrail). The obvious design —
// overwrite, because "the content it described is gone" — rests on a claim that
// is false: content edited away and edited back produces exactly the same
// fingerprint again, and under an overwriting key the verdict that already
// judged it has been destroyed, so the file is judged from scratch. That is
// `identity_is_content` failing, and it is the reason the key is what it is.
func (s *store) RecordFileCheck(path, guardrail string, v Verdict) error {
	db, err := s.conn()
	if err != nil {
		return err
	}
	_, err = db.Exec(`
		INSERT INTO file_checks (path, guardrail, fingerprint, passed, seq)
		VALUES (?, ?, ?, ?, (SELECT COALESCE(MAX(seq), 0) + 1 FROM file_checks))
		ON CONFLICT (path, guardrail, fingerprint) DO UPDATE SET
			passed = excluded.passed,
			seq    = excluded.seq`,
		path, guardrail, v.Fingerprint, v.Passed)
	if err != nil {
		return fmt.Errorf("sessionstate: write check %q/%q: %w", path, guardrail, err)
	}
	return nil
}

// Skippable reports whether a guardrail may skip a file holding the given
// content — which it may only when it has judged that exact content and
// permitted it.
//
// Anything else runs the hook: different content, a failing verdict, or no row
// at all. Expressed here rather than left to each caller because getting it
// wrong in the permissive direction means a rule silently stops firing.
// The row is looked up BY FINGERPRINT rather than by reading whatever this
// guardrail last said about the path. Those differ exactly when a path has been
// judged at more than one content — an agent that edits a file and then reverts
// it — and reading only the latest verdict would answer "no" for content this
// guardrail has already seen and permitted, re-opening a settled question.
func (s *store) Skippable(path, guardrail, fingerprint string) (bool, error) {
	db, err := s.conn()
	if err != nil {
		return false, err
	}
	var passed bool
	err = db.QueryRow(`
		SELECT passed FROM file_checks
		WHERE path = ? AND guardrail = ? AND fingerprint = ?`,
		path, guardrail, fingerprint).Scan(&passed)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("sessionstate: read check %q/%q: %w", path, guardrail, err)
	}
	return passed, nil
}

// OutstandingRefusals reports every (path, guardrail) whose most recent verdict
// is a refusal, with the content that refusal was reached on.
//
// "Most recent" is the highest seq for the pair, and the per-pair maximum is
// what the correlated subquery selects. Reading it any other way gets the rule
// backwards in one direction or the other: filtering on passed = 0 alone would
// keep reporting a file that was refused at one content and has since passed at
// another, so a fix would never end the reporting; taking the global maximum
// would report only whichever pair was written last.
//
// Ordered so the report a cycle produces is stable rather than in whatever order
// the pages come back — a set of violations that reshuffles between two
// identical cycles reads as churn.
func (s *store) OutstandingRefusals() ([]Refusal, error) {
	db, err := s.conn()
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(`
		SELECT path, guardrail, fingerprint FROM file_checks AS c
		WHERE passed = 0
		  AND seq = (SELECT MAX(seq) FROM file_checks AS l
		             WHERE l.path = c.path AND l.guardrail = c.guardrail)
		ORDER BY path, guardrail`)
	if err != nil {
		return nil, fmt.Errorf("sessionstate: read outstanding refusals: %w", err)
	}
	defer rows.Close()

	var out []Refusal
	for rows.Next() {
		var r Refusal
		if err := rows.Scan(&r.Path, &r.Guardrail, &r.Fingerprint); err != nil {
			return nil, fmt.Errorf("sessionstate: read outstanding refusals: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sessionstate: read outstanding refusals: %w", err)
	}
	return out, nil
}
