package e2e

import "testing"

// The reverse direction: a command that changes nothing must produce no event.
//
// This is not a nicety. The failure it guards against is the one that would
// make the whole feature worse than its absence: a rule that fires when the
// agent merely LOOKS at a file is a rule its author turns off, and a turned-off
// rule guards nothing. So the same declaration that must refuse `rm notes.md`
// must leave `cat notes.md` completely alone.
//
// # How this observes "nothing happened"
//
// A check that stays silent and a check that never ran look identical from
// outside, so these do not assert on the absence of a refusal alone — that
// would pass just as well against an engine where the whole rule failed to
// load. Each test uses a LEDGER: the check appends a line to a file in the gate's
// own folder every time it runs, so an absent file is positive evidence that the
// event never reached it, and a present one names exactly what did.
//
// The positive control is what makes the absence readable. T021_08 drives real
// changes through the SAME rule in the SAME shape and finds the ledger written —
// so when the reading commands leave it empty, that is the rule declining to fire
// rather than the rule being broken.
//
// What is observed here is WHICH file events a command derives — a delete for
// `rm`, an update for an in-place edit, nothing for a read. A gate triggering on
// the derived file events (PreFileDelete + PreFileUpdate) narrowed to notes.md is
// the faithful vehicle: it sees the command-derived pre file events with their
// real kinds and records them, and a gate decides on its check alone —
// so the recording check can permit and the ledger reflects exactly which kinds
// arrived. The check reads the
// FLAT GateCheckPayload (`.event.kind`) and the ledger is read with
// e.GateLedgerLines from `.sloprail/gate/<name>/`.

// watchNotes is a gate that runs its check on every derived file event about
// notes.md — a delete or an update — and permits the work. Permitting is
// deliberate: what is under test is which events ARRIVE, not what is decided about
// them, and a refusing check would stop the scenario at the first one.
const watchNotes = `on:
  - event: PreFileDelete
    match: event.path == "notes.md"
  - event: PreFileUpdate
    match: event.path == "notes.md"
checks:
  - script: ./note.sh
`

// noteScript writes the event's kind into a ledger under the gate's own folder and
// permits the work.
//
// The kind is read off the FLAT payload on stdin rather than guessed, so the
// ledger says WHICH event arrived — a test that only counted lines could not tell
// a delete from an update, and this feature can get exactly that wrong. The flat
// wire form carries `"kind":"…"` directly under `event`, so the same sed matches.
const noteScript = `#!/bin/sh
payload="$(cat)"
printf '%s\n' "$payload" | sed -n 's/.*"kind":"\([A-Za-z]*\)".*/\1/p' >> "$SR_GUARDRAIL_DIR/ledger"
exit 0
`

// T021_05: commands that only READ a file produce no event.
//
// Every command here names notes.md and changes none of it. If any produced an
// event the ledger records it, and the rule would be firing on the agent
// reading its own project.
//
// `sed` without `-i` is the sharpest of them: it is a binary this engine knows
// and reads the file, writing stdout. Reporting it as a write would mean the
// vocabulary was matched on the NAME alone rather than on what the arguments
// say the program will do.
// sr:proves events/command-changes-are-file-changes
func TestT021_05_ReadingCommandsProduceNoEvent(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Gate(proj, "watch-notes", watchNotes, map[string]string{"note.sh": noteScript})
	e.WriteFile(proj, "notes.md", "the notes\n")

	e.Run(proj, "s-021-05", "look at the notes", Turns("done",
		Bash("b1", "cat notes.md"),
		Bash("b2", "wc -l notes.md"),
		Bash("b3", "grep notes notes.md"),
		Bash("b4", "sed s/a/b/ notes.md"),
	))

	if lines := e.GateLedgerLines(proj, "watch-notes", "ledger"); len(lines) != 0 {
		t.Fatalf("a command that changes nothing produced %d event(s): %v — the rule fires on reading", len(lines), lines)
	}
	if !e.Exists(proj, "notes.md") {
		t.Fatal("a reading command removed the file, so this test proved nothing")
	}
}

// T021_06: commands that touch no file at all produce no event.
//
// The unknowable tier, stated plainly. No parse can say what `python` or `make`
// touch, and this engine does not pretend otherwise — it says nothing rather
// than guessing. A guess here would be an event naming a file the command may
// never open, and a rule refusing work on the strength of it.
// sr:proves events/command-changes-are-file-changes
func TestT021_06_UnknowableCommandsProduceNoEvent(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Gate(proj, "watch-notes", watchNotes, map[string]string{"note.sh": noteScript})
	e.WriteFile(proj, "notes.md", "the notes\n")

	e.Run(proj, "s-021-06", "run some tooling", Turns("done",
		Bash("b1", "echo hello"),
		Bash("b2", "true"),
		Bash("b3", "ls -la"),
	))

	if lines := e.GateLedgerLines(proj, "watch-notes", "ledger"); len(lines) != 0 {
		t.Fatalf("a command naming no file produced %d event(s): %v", len(lines), lines)
	}
}

// T021_07: a command changing a DIFFERENT file does not fire a rule about this
// one.
//
// The trigger's own half. A gate narrowed to notes.md must not be woken by
// `rm other.md` — and this is the assertion that would catch a path being
// reported wrongly, since a rule bound to one file firing on another means the
// path in the event is not the path the command named.
func TestT021_07_AnotherFileDoesNotFireThisRule(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Gate(proj, "watch-notes", watchNotes, map[string]string{"note.sh": noteScript})
	e.WriteFile(proj, "notes.md", "the notes\n")
	e.WriteFile(proj, "other.md", "not protected\n")

	e.Run(proj, "s-021-07", "remove the other file", Turns("done",
		Bash("b1", "rm other.md"),
	))

	if lines := e.GateLedgerLines(proj, "watch-notes", "ledger"); len(lines) != 0 {
		t.Fatalf("a rule bound to notes.md fired on other.md: %v", lines)
	}
	if e.Exists(proj, "other.md") {
		t.Fatal("the unprotected file was not removed, so nothing was actually exercised")
	}
	if !e.Exists(proj, "notes.md") {
		t.Fatal("the wrong file was removed")
	}
}

// T021_08 is the POSITIVE CONTROL for the three tests above, and without it
// they are all vacuous.
//
// Each of those asserts an empty ledger. An empty ledger is also what a broken
// rule produces — one that failed to load, bound to a kind nothing dispatches,
// or whose check cannot run. So this drives commands that MUST fire through the
// identical declaration and check, and requires the ledger to hold exactly what
// they should have produced.
//
// It also pins the kinds, which is the part a line count would miss. `rm`
// yields PreFileDelete and `sed -i` yields PreFileUpdate, and getting those two
// the wrong way round would leave a delete rule guarding an in-place edit and
// nothing guarding the deletion.
// sr:proves events/command-changes-are-file-changes
func TestT021_08_TheSameRuleDoesFireOnRealChanges(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Gate(proj, "watch-notes", watchNotes, map[string]string{"note.sh": noteScript})
	e.WriteFile(proj, "notes.md", "the notes\n")

	e.Run(proj, "s-021-08", "edit then remove", Turns("done",
		Bash("b1", "sed -i.bak s/the/a/ notes.md"),
		Bash("b2", "rm notes.md"),
	))

	lines := e.GateLedgerLines(proj, "watch-notes", "ledger")
	if len(lines) != 2 {
		t.Fatalf("want 2 events (an update then a delete), got %d: %v — "+
			"the empty-ledger assertions in this file are vacuous unless this fires", len(lines), lines)
	}
	if lines[0] != "PreFileUpdate" {
		t.Errorf("`sed -i` should be an update, got %q", lines[0])
	}
	if lines[1] != "PreFileDelete" {
		t.Errorf("`rm` should be a delete, got %q", lines[1])
	}
}

// T021_09: a redirection is seen, and it is the half with no vocabulary to
// maintain.
//
// `>` is SYNTAX. The shell grammar says what it does, mvdan/sh already parses
// it, and nobody renames it — so unlike the binary table this cannot drift at
// all. It also fires for a program the engine has never heard of, which is what
// this asserts: `some-unknown-tool > notes.md` changes notes.md, and the rule
// holds without the engine knowing anything about the tool.
// sr:proves events/command-changes-are-file-changes
func TestT021_09_ARedirectionIsSeenWhateverRanInFrontOfIt(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Gate(proj, "watch-notes", watchNotes, map[string]string{"note.sh": noteScript})
	e.WriteFile(proj, "notes.md", "the notes\n")

	e.Run(proj, "s-021-09", "append via redirection", Turns("done",
		Bash("b1", "printf 'more\\n' >> notes.md"),
	))

	lines := e.GateLedgerLines(proj, "watch-notes", "ledger")
	if len(lines) != 1 || lines[0] != "PreFileUpdate" {
		t.Fatalf("an append redirection should be one PreFileUpdate, got %v", lines)
	}
}
