package e2e

import (
	"encoding/json"
	"testing"
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

const bindPostFileEvents = `---
hooks:
  PostFileCreate:
    - hooks:
        - type: command
          command: ./judge.sh
  PostFileUpdate:
    - hooks:
        - type: command
          command: ./judge.sh
  PostFileDelete:
    - hooks:
        - type: command
          command: ./judge.sh
---

# Records every after-the-fact file event, and passes everything
`

// judgeScript records and permits.
//
// Permitting matters here: the skip this directory is about only applies to
// content a guardrail has judged AND passed, so a refusing fixture would keep
// every file eligible for re-judging and make the assertions meaningless.
const judgeScript = `#!/bin/sh
cat >> "$PWD/seen"
echo >> "$PWD/seen"
exit 0
`

type observed struct {
	Kind string
	Path string
}

func observedFiles(t *testing.T, lines []string) []observed {
	t.Helper()
	var got []observed
	for _, line := range lines {
		var p struct {
			Event struct {
				Kind   string         `json:"kind"`
				Fields map[string]any `json:"fields"`
			} `json:"event"`
		}
		if err := json.Unmarshal([]byte(line), &p); err != nil {
			t.Fatalf("hook was handed something that is not an event payload: %v\n%s", err, line)
		}
		path, _ := p.Event.Fields["path"].(string)
		got = append(got, observed{Kind: p.Event.Kind, Path: path})
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

// T020_01: content restored to something already judged is not judged again.
//
// The file is written and passed in cycle one, changed and passed in cycle two,
// then put back to the cycle-one bytes in cycle three. At that point the content
// is one the guardrail has already judged and permitted at that same path — so
// there is nothing new to judge, and running the hook again would be re-asking a
// settled question.
//
// An implementation deriving identity from a timestamp, a revision counter, or
// "has it been written since we last looked" re-judges here, because all three
// of those DID change. Only content-derived identity recognises the revert.
//
// The control is cycle two: it must show the hook running when the content is
// genuinely new, or "did not run in cycle three" means only that the hook never
// runs at all.
func TestT020_01_RevertedContentIsNotJudgedAgain(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Guardrail(proj, "watcher", bindPostFileEvents, map[string]string{"judge.sh": judgeScript})

	const sess = "s-020-01"
	const original = "the original content\n"

	e.Run(proj, sess, "write it", Turns("done",
		Write("w1", "subject.md", original),
	))
	afterFirst := countPath(observedFiles(t, e.Ledger(proj, "watcher", "seen")), "subject.md")
	if afterFirst == 0 {
		t.Fatalf("the file was never judged at all, so nothing below can be a skip")
	}

	e.Run(proj, sess, "change it", Turns("done",
		Write("w2", "subject.md", "different content\n"),
	))
	afterSecond := countPath(observedFiles(t, e.Ledger(proj, "watcher", "seen")), "subject.md")
	// The control: genuinely new content IS judged. Without this, the assertion
	// below passes for an engine that judges nothing after the first cycle.
	if afterSecond <= afterFirst {
		t.Fatalf("changed content was not re-judged (%d then %d) — the hook is not running for new "+
			"content, so the skip asserted below would hold for the wrong reason",
			afterFirst, afterSecond)
	}

	// Back to the bytes cycle one already judged and passed.
	e.Run(proj, sess, "put it back", Turns("done",
		Write("w3", "subject.md", original),
	))
	afterRevert := countPath(observedFiles(t, e.Ledger(proj, "watcher", "seen")), "subject.md")
	if afterRevert != afterSecond {
		t.Fatalf("content already judged and passed was judged again after being restored "+
			"(%d then %d) — identity is being derived from when the file was written rather than "+
			"from what it holds, so every revert re-opens a settled question",
			afterSecond, afterRevert)
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
	e.Guardrail(proj, "watcher", bindPostFileEvents, map[string]string{"judge.sh": judgeScript})

	const sess = "s-020-02"
	const content = "content that will move\n"

	e.Run(proj, sess, "write it", Turns("done",
		Write("w1", "origin.md", content),
	))
	if countPath(observedFiles(t, e.Ledger(proj, "watcher", "seen")), "origin.md") == 0 {
		t.Fatalf("the file was never judged at its original path, so the move below proves nothing")
	}

	e.Run(proj, sess, "move it", Turns("done",
		Bash("b1", "mv origin.md moved.md"),
	))

	got := observedFiles(t, e.Ledger(proj, "watcher", "seen"))
	if countPath(got, "moved.md") == 0 {
		t.Fatalf("content that moved to a new path was never judged there: %v — the verdict was "+
			"recorded for the old path, so identity keyed on content alone lets a file arrive "+
			"somewhere it has never been checked", got)
	}
}
