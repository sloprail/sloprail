package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

// before_refusable_only: refusing a `before` (pre) action prevents the work it
// describes; refusing an `after` (post) one cannot — the work has already landed,
// and the refusal demands a correction instead.
//
// # Vehicle: a PreFileWrite gate vs. a plain file-guard
//
// This pre-tool DISPATCH INVARIANT — that a pre binding refuses BEFORE the action
// and so prevents it — maps onto the two natures: a gate on PreFileWrite fires on
// the PRE write and denies it before it lands, while a file-guard acts only AFTER
// the write settles, at Stop, and cannot undo it (a file-guard never sees a Pre*
// event; the old `preventive:` flag is gone and refused at load). So the two halves
// of before_refusable_only become the two natures:
//
//   - the `before` half (this file, T006_01): a gate refuses the write; the file is
//     never created.
//   - the `after` half (test_006_02, T006_02): a file-guard's after-check cannot
//     remove the file, but blocks the turn.
//
// The distinction is asserted against the FILE ON DISK, not against the stream:
// whether a message came back says nothing about whether the write landed, and
// "the work was prevented" is a claim about the tree.

// refuseEveryWrite is a gate that refuses every markdown write before it lands.
// Being a gate on PreFileWrite is what makes it a `before` binding.
const refuseEveryWrite = `on:
  - event: PreFileWrite
    match: event.path endsWith ".md"
checks:
  - script: ./refuse.sh
`

const refuseScript = `#!/bin/sh
cat >/dev/null
echo '{"reason":"refused before it landed"}'
exit 1
`

// T006_01: a gate (before) refusal prevents the work — the file is never
// created.
//
// The meaning of `before`: prevention leaves nothing to clean up and costs an
// attempt. If the file exists afterwards, the refusal was an opinion rather than a
// prevention, whatever the agent was told.
// sr:proves gates/refusal-stops-the-action
func TestT006_01_PreRefusalPreventsTheWrite(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "prevents", refuseEveryWrite, map[string]string{"refuse.sh": refuseScript})

	got := e.Run(proj, "s-006-01", "write a note", Turns("done",
		Write("w1", "some/notes.md", "hello"),
	))

	if !got.Saw("refused before it landed") {
		t.Fatalf("the refusal never reached the agent:\n%s", got.Output)
	}
	if !got.Refused() {
		t.Fatalf("the gate did not deny the write at pre-tool:\n%s", got.Output)
	}
	if _, err := os.Stat(filepath.Join(proj, "some", "notes.md")); err == nil {
		t.Fatalf("a refused pre write still let the file be created — the refusal did not prevent the work")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat: %v", err)
	}
}
