package e2e

import "testing"

// command_changes_a_file: a rule about a file fires whatever changed it — the
// write tool or a shell command — so an agent cannot route around a guardrail
// by reaching for Bash.
//
// # Why this suite exists
//
// Measured before it did: `filemod.extractPending` read the SHAPE of a tool
// call's arguments, so a `file_path` produced a file event and a command line
// produced nothing at all.
//
//	"rm -rf notes.md"          commandmod saw [rm]   ; filemod saw NOTHING
//	"mv a.md b.md"             commandmod saw [mv]   ; filemod saw NOTHING
//	"echo x > out.md"          commandmod saw [echo] ; filemod saw NOTHING
//	"sed -i '' s/a/b/ f.md"    commandmod saw [sed]  ; filemod saw NOTHING
//
// So a rule about a file's deletion did not fire on `rm notes.md`, and the Bash
// tool was a hole straight through every file rule — the difference between a
// guardrail and a suggestion.
//
// # Why these are end-to-end and not unit tests
//
// The unit tests in internal/filemod prove the module classifies a command line
// correctly. They cannot prove anything asks it to. What is asserted here is
// the whole path: the agent runs a command, the harness fires its PreToolUse
// hook, the plugin reaches the subcommand, the file module reads the command
// line, the trigger narrows, the check refuses, and the file is still on disk
// afterwards.
//
// The last clause is the one that matters, and it is asserted against the TREE
// rather than the stream. The mock really executes the command, so a refusal
// that did not prevent the work leaves no file to find — whether or not a
// message came back.
//
// # RE-VEHICLED onto the NEW gate nature (was old GUARDRAIL.md hooks)
//
// The property observed is PREVENTION of a shell deletion: `rm notes.md` is a
// command about to run whose derived PreFileDelete names notes.md, and the block
// happens at the pre-action moment before the command runs. The decision rule
// sends a pre-action block keyed to an event KIND to a GATE — here a gate
// triggering on the file events a command DERIVES (PreFileDelete for `rm`,
// PreFileUpdate for an in-place edit), narrowed on `event.path`. A gate is the
// faithful vehicle: prevention is a gate's job (a file-guard acts only on the
// settled result at Stop, after a command has already run), and a gate decides on
// its check alone. The check reads the
// FLAT GateCheckPayload (`.event.path`, `.event.kind`), refuses with a
// `{"reason":...}` on stdout, and its refusal blocks the command — the file never
// leaves disk.

// guardNotes refuses any deletion of notes.md, and nothing else.
//
// A gate triggering on PreFileDelete alone and narrowed by `event.path`, which is
// the ordinary shape of a rule protecting one file from deletion. Nothing in it
// mentions Bash or shells: that is the point. An author writes a rule about a
// FILE'S deletion, and it holds against every way a file can be deleted, because
// the file event is derived from whatever changed it.
const guardNotes = `on:
  - event: PreFileDelete
    match: event.path == "notes.md"
checks:
  - script: ./refuse.sh
`

// refuseScript refuses with a structured reason on stdout — the new-format refusal
// contract (exit non-zero, `{"reason":...}` preferred by scriptRefusalReason),
// replacing the old exit-1-with-`{"decision":"block"}` shape.
const refuseScript = `#!/bin/sh
cat >/dev/null
echo '{"reason":"notes.md is protected"}'
exit 1
`

// T021_01: a gate refusing deletion of notes.md refuses `rm notes.md`.
//
// THE case. A gate triggering on PreFileDelete, an agent reaching for Bash
// instead of a delete tool, and the file still there afterwards.
//
// Both halves are asserted because either alone can pass on a broken engine. A
// refusal that reached the agent while the file was removed anyway would be an
// opinion rather than a prevention; a file left in place with no refusal would
// mean the command simply failed for its own reasons.
func TestT021_01_ADeleteRuleRefusesRm(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Gate(proj, "protect-notes", guardNotes, map[string]string{"refuse.sh": refuseScript})
	e.WriteFile(proj, "notes.md", "the notes\n")

	got := e.Run(proj, "s-021-01", "delete the notes", Turns("done",
		Bash("b1", "rm notes.md"),
	))

	if !got.Saw("notes.md is protected") {
		t.Fatalf("the refusal never reached the agent — a gate on PreFileDelete did not fire on `rm`:\n%s", got.Output)
	}
	if !e.Exists(proj, "notes.md") {
		t.Fatal("the file was removed despite the refusal — the guardrail was an opinion, not a prevention")
	}
}

// T021_02: the same rule refuses `rm -rf notes.md`.
//
// The spelling the defect report measured. Flags must not hide the file: if
// `-rf` were read as an operand the rule would narrow on the wrong path and let
// the deletion through.
func TestT021_02_FlagsDoNotHideTheFile(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Gate(proj, "protect-notes", guardNotes, map[string]string{"refuse.sh": refuseScript})
	e.WriteFile(proj, "notes.md", "the notes\n")

	got := e.Run(proj, "s-021-02", "force delete the notes", Turns("done",
		Bash("b1", "rm -rf notes.md"),
	))

	if !got.Saw("notes.md is protected") {
		t.Fatalf("`rm -rf` evaded a rule that `rm` does not:\n%s", got.Output)
	}
	if !e.Exists(proj, "notes.md") {
		t.Fatal("the file was removed despite the refusal")
	}
}

// T021_03: a wrapper does not hide the deletion either.
//
// `sudo` in front of a command is a one-word change, and a rule it defeats is a
// rule that can be routed around by an agent that has read the rule. The
// unwrapping commandmod already does for invocations is what closes this, and
// this asserts it reaches the file side too.
//
// The command is `&&`-nested rather than literally `sudo`, because a test must
// not need a password prompt or real privileges to run. What it exercises is the
// same nesting: the deletion sits inside a construct the top-level parse does not
// stop at.
func TestT021_03_NestingDoesNotHideTheDeletion(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Gate(proj, "protect-notes", guardNotes, map[string]string{"refuse.sh": refuseScript})
	e.WriteFile(proj, "notes.md", "the notes\n")

	got := e.Run(proj, "s-021-03", "delete after a check", Turns("done",
		Bash("b1", "echo checking && rm notes.md"),
	))

	if !got.Saw("notes.md is protected") {
		t.Fatalf("a deletion nested behind `&&` evaded the rule:\n%s", got.Output)
	}
	if !e.Exists(proj, "notes.md") {
		t.Fatal("the file was removed despite the refusal")
	}
}

// T021_04: the rule holds against BOTH mechanisms, in one session.
//
// This is the invariant stated whole. The same declaration, unchanged, refuses
// a delete tool and a shell command in the same conversation — which is what
// "a rule about a file fires whatever changed it" means, and what neither half
// alone demonstrates.
//
// The tool half uses a write to a path the rule does NOT protect, so the
// session gets past it, and then the command half is refused. Running the two
// in one session also proves the command path does not depend on being the
// first thing that happened.
func TestT021_04_OneRuleHoldsAgainstBothMechanisms(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Gate(proj, "protect-notes", guardNotes, map[string]string{"refuse.sh": refuseScript})
	e.WriteFile(proj, "notes.md", "the notes\n")

	got := e.Run(proj, "s-021-04", "work then delete", Turns("done",
		Write("w1", "scratch.md", "unprotected\n"),
		Bash("b1", "rm notes.md"),
	))

	if !e.Exists(proj, "scratch.md") {
		t.Fatal("the unprotected write was blocked — the rule is firing outside its trigger")
	}
	if !got.Saw("notes.md is protected") {
		t.Fatalf("the command half was not refused:\n%s", got.Output)
	}
	if !e.Exists(proj, "notes.md") {
		t.Fatal("the file was removed despite the refusal")
	}
}
