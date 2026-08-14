package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

// before_refusable_only: refusing an event whose timing is `before` prevents the
// work it describes; refusing one whose timing is `after` cannot — the work has
// already landed, and the refusal demands a correction instead.
//
// The distinction is asserted against the FILE ON DISK, not against the stream.
// Whether a message came back says nothing about whether the write landed, and
// "the work was prevented" is a claim about the tree.
//
// # ONLY THE `before` HALF IS COVERED
//
// The `after` half — that a refusal at an after-the-fact point cannot prevent
// work already done — is NOT tested here. It had a test, and that test worked by
// hand-writing a PostToolUse hook into the project's settings.json, which is
// wiring no user has: it proved something about an arrangement the harness had
// built for itself rather than about the product. The harness can no longer
// express it at all — writeSettings emits the plugin and nothing else.
//
// Through the plugin it cannot be stated today. A guardrail bound to a Post kind
// is the product's own way to say "after the fact", and `sr-session stop`
// dispatches nothing: a declaration binding to PostFileCreate loads without
// complaint, the write lands, and the hook never runs. Verified, not assumed —
// such a guardrail was driven through the mock and its ledger came back empty.
//
// So this is an uncovered invariant, deliberately, rather than one a green test
// silently stopped watching. When session stop dispatches Post events, the test
// to write is the mirror of T006_01: the same refusing script, bound to
// PostFileCreate instead, asserting the file EXISTS afterwards and that the hook
// ran — the ledger is what separates "correctly ignored" from "never fired",
// which is the trap the old test needed a marker file to avoid.

const refuseEveryWrite = `---
hooks:
  PreFileCreate:
    - hooks:
        - type: command
          command: ./refuse.sh
---

# Refuses the write before it lands
`

const refuseScript = `#!/bin/sh
cat >/dev/null
echo "refused before it landed" >&2
exit 1
`

// T006_01: a Pre refusal prevents the work — the file is never created.
//
// The meaning of `before`: prevention leaves nothing to clean up and costs an
// attempt. If the file exists afterwards, the refusal was an opinion rather than
// a prevention, whatever the agent was told.
func TestT006_01_PreRefusalPreventsTheWrite(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "prevents", refuseEveryWrite, map[string]string{"refuse.sh": refuseScript})

	got := e.Run(proj, "s-006-01", "write a note", Turns("done",
		Write("w1", "some/notes.md", "hello"),
	))

	if !got.Saw("refused before it landed") {
		t.Fatalf("the refusal never reached the agent:\n%s", got.Output)
	}
	if _, err := os.Stat(filepath.Join(proj, "some", "notes.md")); err == nil {
		t.Fatalf("a refused Pre event still let the file be created — the refusal did not prevent the work")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat: %v", err)
	}
}
