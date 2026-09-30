package e2e

import (
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
	"github.com/sloprail/sloprail/tests/e2e/session/changesetkit"
)

// identity_is_content: a file check's fingerprint derives from a file's content,
// and from nothing else about it.
//
// The spec's reasoning: "A file reverted to something already judged has not
// become new again, and one moved to another path has not become the same file
// there. Deriving identity from when it was written or where it sits would get
// both of those backwards."
//
// The fingerprint itself is an internal value with no command that prints it, so
// this directory tests the two CONSEQUENCES the spec names — which is what the
// invariant is for. Each is an observation a test can make from outside:
//
//   - reverted content is not re-judged (T020_01) — identity does not include
//     WHEN the content was written
//   - the same content at another path IS judged (T020_02) — identity is not the
//     content alone in a way that ignores where the check was recorded, and a
//     move is a new file to judge
//
// The pair is deliberate. An engine keying identity on the path alone passes the
// first and fails the second; one keying on content alone across all paths
// passes the second's letter and fails its spirit; one keying on modification
// time fails both.

// recordEverything is a NEW-FORMAT file-guard that records every Changeset it is handed and passes everything . `match: "**/*.md"` fires
// on every committed change. The content-identity SKIP this directory is about is the file-guard's own
// revalidation record — a guard that judged AND passed a fingerprint of content
// skips that content when it recurs, keyed per guard (rev.Skip/rev.Record in the
// post dispatch). The ledger (`seen`, no `.md`) is not
// matched, so the guard cannot re-observe its own bookkeeping.
const recordEverything = `match: "**/*.md"
checks:
  - script: ./judge.sh
`

// The check is changesetkit.RecordScript: it records every payload and permits.
//
// Permitting matters here: the skip this directory is about only applies to
// content a guardrail has judged AND passed, so a refusing fixture would keep
// every file eligible for re-judging and make the assertions meaningless. The
// ledger lives OUTSIDE the rule's folder (harness.Ledger): a rule's hash covers its
// whole folder, and a ledger growing inside it would change the hash between runs
// and drop the rule's watermark.

func countPath(got []changesetkit.Observed, path string) int {
	return len(changesetkit.Statuses(got, path))
}

// T020_01: content restored to something already judged is not judged again.
//
// On the changeset model a rule judges the difference between two commits, and a
// file's identity there is its content: a file changed in one commit and put back
// to the bytes it had at the range's start by a later commit nets out to nothing,
// so the range holds no change to judge. (Per-file "already judged at this
// fingerprint" revalidation is retired; the intent, that identity derives from
// content and not from when it was written, is carried here.)
//
// Why only WITHIN one range, and not across cycles: the later-cycle version (judge
// A, pass, change to B in a cycle, restore A in a later one, expect silence) no
// longer holds. A passing Stop moves the rule's base to the judged commit, so the
// restore in the later cycle is a real difference against B and is judged; the
// engine keeps no per-file memory of fingerprints it once passed. That cross-cycle
// skip was the old revalidation record, which the commit model retired.
//
// The control is the last run: genuinely new content IS judged, so silence about
// the reverted file is not an engine that judges nothing.
func TestT020_01_RevertedContentIsNotJudgedAgain(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	led := e.NewLedger("seen")
	e.FileGuard(proj, "watcher", recordEverything, map[string]string{"judge.sh": changesetkit.RecordScript(led.Path())})
	e.DisableShippedFileGuards(proj)
	e.CommitAll(proj, "the project before the session")

	const sess = "s-020-01"
	const original = "the original content\n"

	e.Run(proj, sess, "write it", Turns("done",
		Write("w1", "subject.md", original),
	).ThenCommit("add subject"))
	afterFirst := countPath(changesetkit.Files(t, led.Lines()), "subject.md")
	if afterFirst == 0 {
		t.Fatalf("the file was never judged at all, so nothing below can be a skip")
	}

	// Changed and put back within one range: two commits, no net difference.
	e.Run(proj, sess, "change it and put it back", Turns("done",
		Write("w2", "subject.md", "different content\n"),
		harness.Commit("c2", "change subject"),
		Write("w3", "subject.md", original),
		harness.Commit("c3", "restore subject"),
	))
	if got := countPath(changesetkit.Files(t, led.Lines()), "subject.md"); got != afterFirst {
		t.Fatalf("content restored to what the range started with was judged again (%d then %d) — "+
			"identity is being derived from the commits made rather than from what the file holds",
			afterFirst, got)
	}

	// The control: content that genuinely differs IS judged.
	e.Run(proj, sess, "change it for real", Turns("done",
		Write("w4", "subject.md", "genuinely new content\n"),
	).ThenCommit("really change subject"))
	if got := countPath(changesetkit.Files(t, led.Lines()), "subject.md"); got <= afterFirst {
		t.Fatalf("changed content was not judged (%d then %d) — the hook is not running for new "+
			"content, so the skip asserted above would hold for the wrong reason", afterFirst, got)
	}
}

// T020_02: the same content at a different path is judged there.
//
// The other half. A file moved to another path has not become the same file
// there — the check was recorded for a path, and the new path has never been
// judged, whatever its bytes. An engine keying identity on content alone, with
// no regard for where the verdict was recorded, would skip the new path and let
// a file arrive somewhere it has never been checked.
//
// The move is done as a real rename through the shell, so the destination has
// content byte-identical to something already passed.
func TestT020_02_TheSameContentAtANewPathIsJudged(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	led := e.NewLedger("seen")
	e.FileGuard(proj, "watcher", recordEverything, map[string]string{"judge.sh": changesetkit.RecordScript(led.Path())})
	e.DisableShippedFileGuards(proj)

	const sess = "s-020-02"
	const content = "content that will move\n"

	e.CommitAll(proj, "the project before the session")

	e.Run(proj, sess, "write it", Turns("done",
		Write("w1", "origin.md", content),
	).ThenCommit("add origin"))
	if countPath(changesetkit.Files(t, led.Lines()), "origin.md") == 0 {
		t.Fatalf("the file was never judged at its original path, so the move below proves nothing")
	}

	e.Run(proj, sess, "move it", Turns("done",
		Bash("b1", "mv origin.md moved.md"),
	).ThenCommit("move origin"))

	got := changesetkit.Files(t, led.Lines())
	if countPath(got, "moved.md") == 0 {
		t.Fatalf("content that moved to a new path was never judged there: %v — the verdict was "+
			"recorded for the old path, so identity keyed on content alone lets a file arrive "+
			"somewhere it has never been checked", got)
	}
}
