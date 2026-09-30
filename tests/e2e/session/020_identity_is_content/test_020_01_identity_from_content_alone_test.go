package e2e

import (
	"encoding/json"
	"testing"
)

// identity_is_content: a file's identity is what it holds, not where it sits or
// when it was written.
//
// A file-guard judges a range of commits (a changeset), and what it is handed for
// a file is that file's old and new content. So the two halves of the invariant
// are observable from a check's payload:
//
//   - a file moved to another path has not become the same file there: the move
//     arrives as ONE file at the new path, a rename that names where it came from
//     (T020_01), never as an unrelated create
//   - the old "reverted content is not re-judged" half belonged to the retired
//     per-path fingerprint. What replaced it is the verdict cache, keyed on the
//     input a check receives: an unchanged input is not judged again, proven in
//     fileguard/034 (T034_03) and changeset/003.

// recordEverything is a file-guard that records the changeset it is handed and
// passes it.
const recordEverything = `match: "**/*.md"
checks:
  - script: ./judge.sh
`

// judgeScript records and permits. The ledger is $SR_GUARDRAIL_DIR/seen.
const judgeScript = `#!/bin/sh
cat >> "$SR_GUARDRAIL_DIR/seen"
echo >> "$SR_GUARDRAIL_DIR/seen"
exit 0
`

// observed is one file a recorded changeset selected.
type observed struct {
	Status  string
	Path    string
	OldPath string
}

// observedFiles decodes what a file-guard's check was handed: the Changeset
// payload's files.
func observedFiles(t *testing.T, lines []string) []observed {
	t.Helper()
	var got []observed
	for _, line := range lines {
		var p struct {
			Changeset struct {
				Files []struct {
					Path    string `json:"path"`
					OldPath string `json:"oldPath"`
					Status  string `json:"status"`
				} `json:"files"`
			} `json:"changeset"`
		}
		if err := json.Unmarshal([]byte(line), &p); err != nil {
			t.Fatalf("the check was handed something that is not a changeset payload: %v\n%s", err, line)
		}
		for _, f := range p.Changeset.Files {
			got = append(got, observed{Status: f.Status, Path: f.Path, OldPath: f.OldPath})
		}
	}
	return got
}

func countPath(got []observed, path string) int {
	n := 0
	for _, o := range got {
		if o.Path == path {
			n++
		}
	}
	return n
}

// T020_01: the same content at a different path is judged there, as a rename.
//
// A file moved to another path has not become the same file there: the new path
// has never been judged, whatever its bytes. The move is a real rename through
// the shell, so the destination is byte-identical to the file it replaces, and it
// still reaches the rule — at the new path, naming the old one.
func TestT020_01_TheSameContentAtANewPathIsJudged(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.FileGuard(proj, "watcher", recordEverything, map[string]string{"judge.sh": judgeScript})
	// The file exists, with the rule, before the session: the rule's range starts
	// at the commit that last touched its folder, so the move is all that is in it.
	writeFile(t, proj, "origin.md", "content that will move\n")
	e.CommitAll(proj, "the project and its guardrail, before the session")

	e.Run(proj, "s-020-01", "move it", Turns("done",
		Bash("b1", "mv origin.md moved.md"),
	).ThenCommit("move it"))

	var moved *observed
	for _, o := range observedFiles(t, e.FileGuardLedgerLines(proj, "watcher", "seen")) {
		if o.Path == "moved.md" {
			o := o
			moved = &o
		}
	}
	if moved == nil {
		t.Fatalf("content that moved to a new path was never judged there — the verdict was " +
			"recorded for the old path, so a file could arrive somewhere it has never been checked")
	}
	if moved.Status != "R" || moved.OldPath != "origin.md" {
		t.Fatalf("the move arrived as %+v, want a rename from origin.md", *moved)
	}
}
