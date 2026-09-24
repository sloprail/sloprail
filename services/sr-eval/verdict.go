package main

import (
	"encoding/json"
	"os"
)

// Verdict is a scorer's structured account of what it found — the schema is
// copied from a10n-eval's own scorer verdict.json (spec-driven-development/
// scorers/*/verdict.json in that repo: {subject, status, rows:[{check_id,
// status, reasoning}]}), not invented fresh, so a fixture author who has
// written one already knows this shape.
type Verdict struct {
	Subject string       `json:"subject"`
	Status  string       `json:"status"` // "pass" | "fail"
	Rows    []VerdictRow `json:"rows"`
}

// VerdictRow is one check's own pass/fail and the evidence for it — a10n's
// per-row shape, field names unchanged.
type VerdictRow struct {
	CheckID   string `json:"check_id"`
	Status    string `json:"status"` // "pass" | "fail"
	Reasoning string `json:"reasoning"`
}

// newVerdictFile creates the temp file a scorer may write its verdict to,
// returning its path and a cleanup func. The file starts out NOT existing
// (os.CreateTemp then Remove) rather than empty, so readVerdictFile can tell
// "the scorer wrote nothing" (file absent) apart from "the scorer wrote an
// empty object" (file present, parses to a zero Verdict) — a scorer that
// never engages with the contract archives no verdict at all, not a blank one.
func newVerdictFile() (path string, cleanup func(), err error) {
	f, err := os.CreateTemp("", "sr-eval-verdict-*.json")
	if err != nil {
		return "", nil, err
	}
	p := f.Name()
	f.Close()
	os.Remove(p)
	return p, func() { os.Remove(p) }, nil
}

// readVerdictFile reads and parses the verdict a scorer wrote, or returns nil
// when it wrote none or wrote something unparseable. Unparseable is treated
// the same as absent rather than as an error: the scorer's own exit code is
// still the authority on pass/fail, and a malformed verdict file should not
// turn a scorer that otherwise ran fine into an infra failure — it just means
// this run's archive carries no structured evidence, same as a scorer that
// never wrote one.
func readVerdictFile(path string) *Verdict {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var v Verdict
	if json.Unmarshal(body, &v) != nil {
		return nil
	}
	return &v
}
