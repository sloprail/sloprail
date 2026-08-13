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
		WHERE path = ? AND guardrail = ?`, path, guardrail).Scan(&v.Fingerprint, &v.Passed)
	if errors.Is(err, sql.ErrNoRows) {
		return Verdict{}, false, nil
	}
	if err != nil {
		return Verdict{}, false, fmt.Errorf("sessionstate: read check %q/%q: %w", path, guardrail, err)
	}
	return v, true, nil
}

// RecordFileCheck stores a verdict, replacing whatever the same guardrail last
// said about the same file.
//
// Replacing is the point: the row answers "has this guardrail already judged
// the content the file holds now", and a superseded fingerprint answers nothing
// — the content it described is gone and cannot come back under the same path
// without producing the same fingerprint again.
func (s *store) RecordFileCheck(path, guardrail string, v Verdict) error {
	db, err := s.conn()
	if err != nil {
		return err
	}
	_, err = db.Exec(`
		INSERT INTO file_checks (path, guardrail, fingerprint, passed) VALUES (?, ?, ?, ?)
		ON CONFLICT (path, guardrail) DO UPDATE SET
			fingerprint = excluded.fingerprint,
			passed      = excluded.passed`,
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
func (s *store) Skippable(path, guardrail, fingerprint string) (bool, error) {
	v, found, err := s.FileCheck(path, guardrail)
	if err != nil || !found {
		return false, err
	}
	return v.Passed && v.Fingerprint == fingerprint, nil
}
