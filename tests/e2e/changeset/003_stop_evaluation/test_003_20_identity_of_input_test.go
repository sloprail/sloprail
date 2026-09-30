package e2e

import (
	"reflect"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// What a verdict is about is the input the rule was handed — its files and their
// content, at the paths they sit at — not the bytes alone and not the history that
// produced them.

// badPathCheck refuses, naming them, every changed file under docs/bad*: a
// verdict that depends on the PATH, so identical bytes can be fine in one place
// and refused in another.
const badPathCheck = `#!/bin/sh
payload="$(cat)"
bad="$(printf '%s' "$payload" | jq -r '[.changeset.files[].path | select(startswith("docs/bad"))] | join(" ")')"
[ -z "$bad" ] && exit 0
printf '{"reason":"BAD-PATH: %s"}' "$bad"
exit 1
`

// T003_20: identical bytes are judged at every path. Bytes that passed at one path
// are still refused at another, and bytes refused at one path do not silence the
// judgement of the same bytes at a second.
func TestT003_20_IdenticalBytesAreJudgedAtEveryPath(t *testing.T) {
	e, proj, _ := seededProject(t, docsRule, map[string]string{"docs/seed.md": "seed\n"},
		func(string) string { return badPathCheck })
	const sess = "s-003-20"
	const same = "the very same bytes\n"

	e.Run(proj, sess, "write the good path", Turns("done",
		harness.CommitFile("c1", "docs/ok.md", same, "add ok"),
	))
	if n := stopBlocks(e, proj, sess); n != 0 {
		t.Fatalf("premise: the good path was refused (%d blocks)", n)
	}

	e.Run(proj, sess, "same bytes, another path", Turns("done",
		harness.CommitFile("c2", "docs/bad1.md", same, "add bad1"),
	))
	if b := newBlocks(e, proj, sess, 0); !strings.Contains(b, "BAD-PATH: docs/bad1.md") {
		t.Fatalf("bytes that passed at docs/ok.md were not refused at docs/bad1.md: %q", b)
	}
	seen := stopBlocks(e, proj, sess)

	e.Run(proj, sess, "and a third path", Turns("done",
		harness.CommitFile("c3", "docs/bad2.md", same, "add bad2"),
	))
	b := newBlocks(e, proj, sess, seen)
	if !strings.Contains(b, "docs/bad2.md") {
		t.Fatalf("bytes refused at docs/bad1.md silenced the judgement at docs/bad2.md: %q", b)
	}
}

// T003_21: content edited back to a body that was refused is refused again. The
// judge refuses the body, passes a corrected one (and the watermark moves), then
// the agent puts the refused body back: a pass for the fix does not stand in for
// the body it replaced, and the judge is asked about it afresh.
func TestT003_21_ContentEditedBackToARefusedBodyIsRefusedAgain(t *testing.T) {
	e, proj := judgeProject(t, verdictFail)
	const sess = "s-003-21"

	e.Run(proj, sess, "write the doc", Turns("done",
		harness.CommitFile("c1", "docs/a.md", "the release is Friday", "add a"),
	))
	if !strings.Contains(newBlocks(e, proj, sess, 0), "JUDGE-SAYS-NO") {
		t.Fatalf("premise: the body was not refused: %q", e.BlockingErrors(proj, sess))
	}

	// The corrected body passes.
	e.InstallJudgeClaudeCapturing(proj, promptFile, verdictPass)
	seen := stopBlocks(e, proj, sess)
	e.Run(proj, sess, "fix it", Turns("done",
		harness.CommitFile("c2", "docs/a.md", "the release is Monday", "fix a"),
	))
	if n := stopBlocks(e, proj, sess); n != seen {
		t.Fatalf("the corrected body was refused:\n%s", newBlocks(e, proj, sess, seen))
	}
	asked := e.JudgeCalls(proj, promptFile, "")

	// Back to the refused body: judged again, and refused again.
	e.InstallJudgeClaudeCapturing(proj, promptFile, verdictFail)
	e.Run(proj, sess, "put it back", Turns("done",
		harness.CommitFile("c3", "docs/a.md", "the release is Friday", "revert a"),
	))
	if !strings.Contains(newBlocks(e, proj, sess, seen), "JUDGE-SAYS-NO") {
		t.Fatalf("content edited back to a refused body was not refused: %q", e.BlockingErrors(proj, sess))
	}
	if n := e.JudgeCalls(proj, promptFile, ""); n <= asked {
		t.Fatalf("the judge was not asked about the restored body (%d calls, had %d)", n, asked)
	}
}

// T003_22: a stale fail is judged again when its input returns. The failing
// judge's verdict is terminal while its input stands; when the file leaves the
// range the old failure goes stale (the range passes, nothing outstanding), and
// when the same file comes back the verdict is not replayed from the dead record:
// the judge is asked again and refuses again.
func TestT003_22_AStaleFailIsJudgedAgainWhenItsInputReturns(t *testing.T) {
	e, proj := judgeProject(t, verdictFail)
	const sess = "s-003-22"
	const body = "the release is Friday"

	e.Run(proj, sess, "write the doc", Turns("done",
		harness.CommitFile("c1", "docs/a.md", body, "add a"),
	))
	if !strings.Contains(newBlocks(e, proj, sess, 0), "JUDGE-SAYS-NO") {
		t.Fatalf("premise: the body was not refused: %q", e.BlockingErrors(proj, sess))
	}
	first := e.JudgeCalls(proj, promptFile, "")
	if first == 0 {
		t.Fatal("premise: the judge was never asked")
	}
	seen := stopBlocks(e, proj, sess)

	// The input leaves the range: added and deleted inside one range is no change.
	e.Run(proj, sess, "take it back out", Turns("done",
		Bash("d1", "git rm -q docs/a.md"),
		harness.Commit("d2", "remove a"),
	))
	if n := stopBlocks(e, proj, sess); n != seen {
		t.Fatalf("the range with its input gone was still refused:\n%s", newBlocks(e, proj, sess, seen))
	}
	if n := e.JudgeCalls(proj, promptFile, ""); n != first {
		t.Fatalf("the judge was asked about a range holding no file (%d calls, had %d)", n, first)
	}

	// The same input returns.
	e.Run(proj, sess, "bring it back", Turns("done",
		harness.CommitFile("c3", "docs/a.md", body, "add a"),
	))
	if !strings.Contains(newBlocks(e, proj, sess, seen), "JUDGE-SAYS-NO") {
		t.Fatalf("the returning input was not refused: %q", e.BlockingErrors(proj, sess))
	}
	if n := e.JudgeCalls(proj, promptFile, ""); n <= first {
		t.Fatalf("the stale failure was replayed instead of judged again (%d calls, had %d)", n, first)
	}
}

// protectedDeletionCheck records what it is handed and refuses a deletion of any
// file whose old content is PROTECTED, naming each.
func protectedDeletionCheck(ledger string) string {
	return `#!/bin/sh
payload="$(cat)"
printf '%s' "$payload" | jq -c '{kind: .event.kind, files: [.changeset.files[] | {path, status}]}' >> ` + ledger + `
gone="$(printf '%s' "$payload" | jq -r '[.changeset.files[] | select(.status == "D" and (.oldContent | contains("PROTECTED"))) | .path] | join(" ")')"
[ -z "$gone" ] && exit 0
printf '{"reason":"PROTECTED-DELETED: %s"}' "$gone"
exit 1
`
}

// T003_23: under `deletions: include` every deleted file reaches the rule, each
// with its old content and no new one — two in one commit are both handed over
// and both named in the refusal; restoring them leaves nothing to judge and passes.
func TestT003_23_AMultiFileDeletionReachesTheRule(t *testing.T) {
	const x, y = "PROTECTED x", "PROTECTED y"
	rule := "match: \"docs/**\"\ndeletions: include\nchecks:\n  - script: ./check.sh\n"
	e, proj, led := seededProject(t, rule, map[string]string{"docs/x.md": x, "docs/y.md": y}, protectedDeletionCheck)
	const sess = "s-003-23"

	e.Run(proj, sess, "clean up", Turns("done",
		Bash("d1", "git rm -q docs/x.md docs/y.md"),
		harness.Commit("d2", "delete both"),
	))
	if b := newBlocks(e, proj, sess, 0); !strings.Contains(b, "PROTECTED-DELETED: docs/x.md docs/y.md") {
		t.Fatalf("a two-file deletion was not refused naming both: %q", e.BlockingErrors(proj, sess))
	}
	got := lastRun(t, led)
	if !reflect.DeepEqual(paths(got.Files), []string{"docs/x.md", "docs/y.md"}) {
		t.Fatalf("the rule was handed %v; want both deleted files", paths(got.Files))
	}
	for _, f := range got.Files {
		if f.Status != "D" {
			t.Fatalf("%s arrived with status %s, want D", f.Path, f.Status)
		}
	}

	// Restored exactly: the range nets to nothing and passes.
	seen := stopBlocks(e, proj, sess)
	e.Run(proj, sess, "put them back", Turns("done",
		harness.CommitFile("r1", "docs/x.md", x, "restore x"),
		harness.CommitFile("r2", "docs/y.md", y, "restore y"),
	))
	if n := stopBlocks(e, proj, sess); n != seen {
		t.Fatalf("the restored files were still refused:\n%s", newBlocks(e, proj, sess, seen))
	}
}
