// Package changesetkit reads what a file-guard's recording check was handed on
// the changeset model: one payload per judged range, its selected files under
// `.changeset.files[]` as {path, status} with status A (added), M (modified) or
// D (deleted). It is shared by the session e2e packages, which each record the
// payload to a ledger and assert on the files the engine put in front of a rule.
package changesetkit

import (
	"encoding/json"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// JudgeRun is one Run followed by `sr check run` over exactly the commits that Run
// added: HEAD before it .. HEAD after it. A file-guard judges the range its caller
// states, and nothing remembers what an earlier range passed, so a test about what
// ONE cycle of work puts in front of a rule states that cycle's range itself. The
// Env must be built with harness.NoAutoCheck, or the harness's own check of the
// whole session's range records into the same ledger too.
func JudgeRun(t testing.TB, e *harness.Env, proj, sess, prompt string, s harness.Scenario) harness.Result {
	t.Helper()
	base := e.Git(proj, "rev-parse", "HEAD")
	res := e.Run(proj, sess, prompt, s)
	e.CheckRunRange(proj, sess, base, "HEAD")
	return res
}

// Observed is one file entry of one recorded changeset.
type Observed struct {
	Status  string // A, M or D
	Path    string
	OldPath string // the path a rename came from; empty otherwise

	OldContent string // what the file held at the range's base; empty for an addition
	NewContent string // what it holds at the range's head; empty for a deletion
}

// Files decodes every recorded payload line into its file entries, in order.
// A line that is not a changeset payload fails the test: the check was handed
// something the engine should never send.
func Files(t testing.TB, lines []string) []Observed {
	t.Helper()
	var got []Observed
	for _, line := range lines {
		var p struct {
			Changeset struct {
				Files []struct {
					Path       string `json:"path"`
					Status     string `json:"status"`
					OldPath    string `json:"oldPath"`
					OldContent string `json:"oldContent"`
					NewContent string `json:"newContent"`
				} `json:"files"`
			} `json:"changeset"`
		}
		if err := json.Unmarshal([]byte(line), &p); err != nil {
			t.Fatalf("the check was handed something that is not a changeset payload: %v\n%s", err, line)
		}
		for _, f := range p.Changeset.Files {
			got = append(got, Observed{Status: f.Status, Path: f.Path, OldPath: f.OldPath, OldContent: f.OldContent, NewContent: f.NewContent})
		}
	}
	return got
}

// Statuses is every status the path was reported with, in order.
func Statuses(got []Observed, path string) []string {
	var out []string
	for _, o := range got {
		if o.Path == path {
			out = append(out, o.Status)
		}
	}
	return out
}

// Saw reports whether any recorded changeset names the path.
func Saw(got []Observed, path string) bool { return len(Statuses(got, path)) > 0 }

// RenamedFrom reports whether some recorded changeset holds an entry for path
// that says it was renamed from old.
func RenamedFrom(got []Observed, path, old string) bool {
	for _, o := range got {
		if o.Path == path && o.OldPath == old {
			return true
		}
	}
	return false
}

// Has reports whether some recorded changeset holds the path with the given
// status, both on the same file entry.
func Has(got []Observed, status, path string) bool {
	for _, o := range got {
		if o.Path == path && o.Status == status {
			return true
		}
	}
	return false
}
