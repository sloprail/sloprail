package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A guardrail bound to every write, whose hook is swapped per test. What varies
// is only HOW the hook refuses — never whether it meant to.
const bindEveryWrite = `---
hooks:
  PreFileCreate:
    - hooks:
        - type: command
          command: ./refuse.sh
---

# Refuses whatever it is shown

The tests here vary how the refusal is expressed, not whether one was intended.
`

// permitted reports whether the write went through — i.e. whether the agent got
// to its final answer without the refusal reaching it.
//
// A guardrail failing open is the one failure that looks exactly like success,
// which is why each test below asserts the write was stopped rather than merely
// that some message appeared.
func permitted(output string) bool {
	for _, sign := range []string{"deny", "denied", "block", "blocked"} {
		if strings.Contains(output, sign) {
			return false
		}
	}
	return true
}

// T004_01: a hook refusing via stderr is a refusal.
//
// `echo "..." >&2; exit 1` is an ordinary way for a shell script to fail, and
// nothing in the format tells an author to prefer stdout. Reading only stdout
// discarded these refusals entirely: a correctly authored, executable,
// deliberately refusing hook let the write through.
func TestT004_01_StderrRefusalReachesTheAgent(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "stderr-refuse", bindEveryWrite, map[string]string{
		"refuse.sh": "#!/bin/sh\necho \"this path is guarded\" >&2\nexit 1\n",
	})

	got := e.Run(proj, "s-004-01", "write a note", Turns("done",
		Write("w1", "any/notes.md", "hello"),
	))

	if !got.Saw("this path is guarded") {
		t.Fatalf("a refusal written to stderr never reached the agent:\n%s", got.Output)
	}
	if permitted(got.Output) {
		t.Fatalf("the write was permitted despite the hook refusing:\n%s", got.Output)
	}
}

// T004_02: a hook that cannot be run refuses.
//
// Exit 126 with no output. The mechanism failing must not read as approval —
// otherwise forgetting chmod +x silently disarms the rule, and the project looks
// guarded while nothing is.
func TestT004_02_UnrunnableHookRefuses(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "unrunnable", bindEveryWrite, map[string]string{
		"refuse.sh": "#!/bin/sh\nexit 0\n",
	})

	// Guardrail writes scripts executable; take that away.
	script := filepath.Join(proj, ".sloprail", "guardrails", "unrunnable", "refuse.sh")
	if err := os.Chmod(script, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	got := e.Run(proj, "s-004-02", "write a note", Turns("done",
		Write("w1", "any/notes.md", "hello"),
	))

	if permitted(got.Output) {
		t.Fatalf("a hook that could not run permitted the write:\n%s", got.Output)
	}
	if !got.Saw("executable") {
		t.Errorf("the refusal does not say what to fix (expected it to mention being executable):\n%s", got.Output)
	}
}

// T004_03: a hook that exits non-zero saying nothing at all still refuses.
//
// The bare form of the rule: refusal is the exit status, not the output. There
// is nothing to quote back, so the engine has to supply a reason itself rather
// than treat an empty one as consent.
func TestT004_03_SilentNonZeroRefuses(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "silent", bindEveryWrite, map[string]string{
		"refuse.sh": "#!/bin/sh\ncat >/dev/null\nexit 1\n",
	})

	got := e.Run(proj, "s-004-03", "write a note", Turns("done",
		Write("w1", "any/notes.md", "hello"),
	))

	if permitted(got.Output) {
		t.Fatalf("a silent non-zero exit permitted the write:\n%s", got.Output)
	}
	if !got.Saw("silent") {
		t.Errorf("the refusal does not name the guardrail that produced it:\n%s", got.Output)
	}
}

// T004_04: a refusal carrying an empty reason does not leak raw JSON.
//
// `{"decision":"block","reason":""}` parsed but said nothing, and the fallback
// printed the whole document as the user-facing reason — handing the agent an
// implementation detail where an instruction belongs.
func TestT004_04_EmptyJSONReasonDoesNotLeakJSON(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "empty-reason", bindEveryWrite, map[string]string{
		"refuse.sh": "#!/bin/sh\ncat >/dev/null\necho '{\"decision\":\"block\",\"reason\":\"\"}'\nexit 1\n",
	})

	got := e.Run(proj, "s-004-04", "write a note", Turns("done",
		Write("w1", "any/notes.md", "hello"),
	))

	if permitted(got.Output) {
		t.Fatalf("an empty-reason refusal permitted the write:\n%s", got.Output)
	}
	if got.Saw(`\"decision\"`) || got.Saw(`{\"decision`) {
		t.Errorf("raw JSON leaked into the reason shown to the agent:\n%s", got.Output)
	}
}

// T004_05: exit zero still permits.
//
// The other half. Failing closed everywhere would be easy and useless — a
// guardrail that refuses work its hook approved is as broken as one that
// approves work its hook refused, and only this test tells the two fixes apart.
func TestT004_05_ZeroExitStillPermits(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "permits", bindEveryWrite, map[string]string{
		"refuse.sh": "#!/bin/sh\ncat >/dev/null\necho 'chatter on stdout'\necho 'chatter on stderr' >&2\nexit 0\n",
	})

	got := e.Run(proj, "s-004-05", "write a note", Turns("done",
		Write("w1", "any/notes.md", "hello"),
	))

	if !permitted(got.Output) {
		t.Fatalf("a hook that exited zero was treated as refusing:\n%s", got.Output)
	}
}
