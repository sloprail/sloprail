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
// Both refusals block, but they do not mean the same thing, and a rule author
// choosing where to bind is choosing between them. The half tested here is the
// one with a wrong answer available: an engine that let an `after` hook prevent
// work would be promising a rollback it cannot perform, and an author binding
// there would believe the work never happened.
//
// The distinction is asserted against the FILE ON DISK, not against the stream.
// Whether a message came back says nothing about whether the write landed, and
// "the work was prevented" is a claim about the tree.

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

// T006_02: a hook at an after-the-fact point cannot prevent the work.
//
// The same script, refusing just as hard, at a hook point that fires once the
// write has landed. It must not stop the file existing — there is nothing left
// to stop. An engine that reported prevention here would be claiming a rollback
// it never performed.
//
// Wired by hand into the project's settings rather than through a guardrail
// binding, because that is what makes it a test of the ENGINE'S contract: the
// distinction between the two timings has to hold at the harness's own hook
// points, whatever a declaration asks for. This is also the control the
// `after` half of the invariant needs — the two runs differ only in which
// lifecycle point the identical refusing script is attached to.
func TestT006_02_PostHookCannotPreventTheWrite(t *testing.T) {
	e := New(t)
	proj := e.Project()

	// The hook records that it ran, to a path of its own, before refusing.
	//
	// Without this line the test is vacuous: its only claim would be that the
	// file exists after a refusing Post hook, and a Post hook that never fired
	// at all produces exactly that. The file would exist because nothing tried
	// to stop it, not because an after-the-fact refusal was correctly ignored.
	// Point the ExtraHook at an event that cannot fire and the difference is the
	// whole test — the ran-marker disappears, the write still lands.
	ran := filepath.Join(proj, "post-hook-ran")
	script := filepath.Join(proj, "refuse-after.sh")
	body := "#!/bin/sh\ncat >/dev/null\necho ran >> " + ran + "\necho \"refused after it landed\" >&2\nexit 1\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	// The plugin's own wiring plus one refusing PostToolUse hook. Keeping the
	// plugin enabled matters: this must be the ordinary arrangement with an
	// after-the-fact rule added, not a project with the engine taken out.
	e.ExtraHook(proj, "PostToolUse", "Write", script)

	got := e.Run(proj, "s-006-02", "write a note", Turns("done",
		Write("w1", "some/notes.md", "hello"),
	))

	// The hook fired. Asserted first: everything below is about what its refusal
	// failed to prevent, and none of it means anything if the hook was never
	// reached.
	//
	// Read from the marker file, not the stream. The refusal is NOT carried back
	// to the agent here, and that absence is itself part of the invariant rather
	// than a gap in the observation — an after-the-fact refusal has nothing left
	// to refuse, so there is no tool call for it to come back on. Which leaves
	// the marker as the only channel that distinguishes a hook that ran and was
	// ignored from one that never fired.
	if _, err := os.Stat(ran); err != nil {
		t.Fatalf("the PostToolUse hook never ran, so this proves nothing about after-the-fact refusals: %v\n%s", err, got.Output)
	}

	// The work landed anyway. A refusal after the fact demands a correction; it
	// does not and cannot undo the write.
	if _, err := os.Stat(filepath.Join(proj, "some", "notes.md")); err != nil {
		t.Fatalf("a hook refusing AFTER the write prevented it — the two timings are being treated alike: %v", err)
	}
}
