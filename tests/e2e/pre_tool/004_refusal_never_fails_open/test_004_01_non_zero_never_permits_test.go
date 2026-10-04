package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// refusal_never_fails_open: however the CHECK misbehaves, the engine fails
// CLOSED — the write is blocked. A guardrail failing open is the one failure that
// looks exactly like success, so this is the critical safety invariant of the
// pre-tool dispatch, and it must hold against the NEW dispatch as well.
//
// It used to install a rule via the OLD format (`hooks: PreFileCreate:
// [command: ./refuse.sh]`) and vary HOW the hook refused, observing the OLD
// dispatch fail closed. The NEW pre-tool dispatch
// (services/sr-session/nature_pre_tool.go) runs a gate's check
// through internal/dispatch's Runner, and that runner is fail-closed throughout
// (internal/dispatch/exec.go runScriptExec): a clean exit passes, and EVERY other
// outcome — a non-zero exit, a check that could not be run, a check that emitted
// garbage — refuses. the gate dispatch turns that refusal into a pre-tool
// deny, so the write never lands. This directory re-proves that guarantee against
// the new dispatch by driving each misbehavior mode and asserting the write is
// blocked every way.
//
// The new-format refusal CONTRACT is `{"reason": …}` on stdout with a non-zero
// exit — but the invariant here is not "the script used the contract correctly",
// it is "however the script FAILS to produce a clean permit, the engine refuses".
// So the modes below are misbehaviors (non-executable, silent non-zero, garbage
// output, a stderr-only refusal, an empty reason) and each must still block. The
// one control (T004_05) proves the engine does NOT fail closed on a clean exit 0,
// because failing closed everywhere would be as broken as failing open.
//
// The observation is res.Refused / res.Saw — the harness's own pre-tool deny
// marker (with the reason appended), format-neutral across old and new dispatch.

// bindEveryWrite is a NEW-FORMAT gate bound to every markdown write on
// PreFileWrite. `event.path endsWith ".md"` selects a write at the repo root or at
// any depth; a gate fires on the PRE write so a refusal denies before the file lands.
// The check is swapped per test — what varies is only HOW the check misbehaves,
// never whether it meant to refuse.
const bindEveryWrite = `on:
  - event: PreFileWrite
    match: event.path endsWith ".md"
checks:
  - script: ./refuse.sh
`

// T004_01: a check refusing via stderr is a refusal.
//
// `echo "..." >&2; exit 1` is an ordinary way for a shell script to fail, and the
// new contract prefers a structured stdout reason but still accepts a plain
// stderr one (scriptRefusalReason's plainText(stderr) fallback). A non-zero exit
// is never consent whatever channel carried the words, so the write is blocked
// and the stderr text is what the agent is told.
func TestT004_01_StderrRefusalReachesTheAgent(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "stderr-refuse", bindEveryWrite, map[string]string{
		"refuse.sh": "#!/bin/sh\ncat >/dev/null\necho \"this path is guarded\" >&2\nexit 1\n",
	})

	got := e.Run(proj, "s-004-01", "write a note", Turns("done",
		Write("w1", "any/notes.md", "hello"),
	))

	if !got.Saw("this path is guarded") {
		t.Fatalf("a refusal written to stderr never reached the agent:\n%s", got.Output)
	}
	if !got.Refused() {
		t.Fatalf("the write was permitted despite the check refusing:\n%s", got.Output)
	}
}

// T004_02: a gate whose declared script cannot be exec'd directly does not LOAD, and says so.
//
// Scripts are run directly, never as `sh <file>`: one that is not executable (chmod 000 here,
// beyond even a readable file missing its execute bit) or has no shebang makes the rule fail to
// load, like any other declaration fault. It is reported to the agent at the next session hook,
// naming the file and the fix, not silently permitted: a rule that did not load is not a rule
// that approved. (A script that LOADS and then fails at run time still refuses: T004_03 on.)
func TestT004_02_UnrunnableCheckDoesNotLoadAndIsReported(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "unrunnable", bindEveryWrite, map[string]string{
		"refuse.sh": "#!/bin/sh\nexit 0\n",
	})
	script := filepath.Join(proj, ".sloprail", "gate", "unrunnable", "refuse.sh")
	if err := os.Chmod(script, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	start := e.CLI(proj, "session", "start")
	for _, want := range []string{"not loaded", "unrunnable", "executable"} {
		if !strings.Contains(start.Output, want) {
			t.Errorf("the session start does not report the unloadable gate (missing %q):\n%s", want, start.Output)
		}
	}
}

// T004_02b: the negative control for T004_02: a script missing ONLY its execute bit is no longer
// run through its `#!` interpreter either. It is the same load error, and the report names the fix
// (`chmod +x`). The script never runs, so its own refusal never reaches the agent.
func TestT004_02b_MissingExecuteBitAloneIsALoadErrorNamingTheFix(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "chmod-forgotten", bindEveryWrite, map[string]string{
		"refuse.sh": "#!/bin/sh\ncat >/dev/null\necho '{\"reason\":\"refused by the script\"}'\nexit 1\n",
	})
	script := filepath.Join(proj, ".sloprail", "gate", "chmod-forgotten", "refuse.sh")
	if err := os.Chmod(script, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	start := e.CLI(proj, "session", "start")
	for _, want := range []string{"not loaded", "chmod-forgotten", "chmod +x"} {
		if !strings.Contains(start.Output, want) {
			t.Errorf("the session start does not report the missing execute bit (missing %q):\n%s", want, start.Output)
		}
	}
	got := e.Run(proj, "s-004-02b", "write a note", Turns("done",
		Write("w1", "any/notes.md", "hello"),
	))
	if got.Saw("refused by the script") {
		t.Errorf("a script without its execute bit was run through its interpreter:\n%s", got.Output)
	}
}

// T004_03: a check that exits non-zero saying nothing at all still refuses.
//
// The bare form of the rule: refusal is the exit status, not the output. There is
// nothing to quote back, so the engine has to supply a reason itself rather than
// treat an empty one as consent. scriptRefusalReason falls through to "the check
// … refused (exit 1) but gave no reason".
func TestT004_03_SilentNonZeroRefuses(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "silent", bindEveryWrite, map[string]string{
		"refuse.sh": "#!/bin/sh\ncat >/dev/null\nexit 1\n",
	})

	got := e.Run(proj, "s-004-03", "write a note", Turns("done",
		Write("w1", "any/notes.md", "hello"),
	))

	if !got.Refused() {
		t.Fatalf("a silent non-zero exit permitted the write:\n%s", got.Output)
	}
	// The refusal names the guard that produced it, even with nothing of the
	// check's own to quote.
	if !got.Saw("silent") {
		t.Errorf("the refusal does not name the gate that produced it:\n%s", got.Output)
	}
}

// T004_04: a check that emits GARBAGE on stdout and exits non-zero still refuses,
// and does not hand the agent unparseable noise where an instruction belongs.
//
// This is the new-format analogue of the old empty-JSON-leak test. A `{"reason":
// ""}` parses but says nothing, and a bare non-JSON blob is not a structured
// reason either — structuredReason returns "" for both, and the engine supplies
// its own reason rather than dumping the raw output. Either way the write is
// blocked: the misbehavior is the CHECK's, and it must not read as a pass.
func TestT004_04_GarbageOutputStillRefusesWithoutLeaking(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	// An empty structured reason on stdout, plus noise, exit non-zero.
	e.Gate(proj, "empty-reason", bindEveryWrite, map[string]string{
		"refuse.sh": "#!/bin/sh\ncat >/dev/null\necho '{\"reason\":\"\"}'\nexit 1\n",
	})

	got := e.Run(proj, "s-004-04", "write a note", Turns("done",
		Write("w1", "any/notes.md", "hello"),
	))

	if !got.Refused() {
		t.Fatalf("an empty-reason refusal permitted the write:\n%s", got.Output)
	}
	// The raw JSON is not handed to the agent as the reason.
	if got.Saw(`\"reason\":\"\"`) {
		t.Errorf("raw JSON leaked into the reason shown to the agent:\n%s", got.Output)
	}
}

// T004_04b: a check that emits a NON-JSON blob and exits non-zero refuses, and
// the blob (being plain text, not JSON) is allowed through as the reason.
//
// This pins the other edge of scriptRefusalReason: plain stdout that is NOT JSON
// is a check answering in words (plainText(stdout)), so it is shown — but the
// write is still blocked because the exit was non-zero. A misbehaving check that
// forgot the structured contract but exited non-zero must never be read as a
// permit.
func TestT004_04b_PlainNonZeroOutputRefuses(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "plain-refuse", bindEveryWrite, map[string]string{
		"refuse.sh": "#!/bin/sh\ncat >/dev/null\necho 'nope, not allowed'\nexit 3\n",
	})

	got := e.Run(proj, "s-004-04b", "write a note", Turns("done",
		Write("w1", "any/notes.md", "hello"),
	))

	if !got.Refused() {
		t.Fatalf("a non-zero exit with plain output permitted the write:\n%s", got.Output)
	}
	if !got.Saw("nope, not allowed") {
		t.Errorf("the check's own plain-text reason did not reach the agent:\n%s", got.Output)
	}
}

// T004_05: exit zero still permits.
//
// The other half. Failing closed everywhere would be easy and useless — a
// guardrail that refuses work its check approved is as broken as one that
// approves work its check refused, and only this test tells the two fixes apart.
// Chatter on both streams at exit 0 must not be mistaken for a refusal.
func TestT004_05_ZeroExitStillPermits(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Gate(proj, "permits", bindEveryWrite, map[string]string{
		"refuse.sh": "#!/bin/sh\ncat >/dev/null\necho 'chatter on stdout'\necho 'chatter on stderr' >&2\nexit 0\n",
	})

	got := e.Run(proj, "s-004-05", "write a note", Turns("done",
		Write("w1", "any/notes.md", "hello"),
	))

	if got.Refused() {
		t.Fatalf("a check that exited zero was treated as refusing:\n%s", got.Output)
	}
	// And the fine write actually landed — a permit that did not let the write
	// through would be a fail-closed masquerading as a permit.
	if !e.Exists(proj, filepath.Join("any", "notes.md")) {
		t.Errorf("a write the check permitted did not land:\n%s", got.Output)
	}
}
