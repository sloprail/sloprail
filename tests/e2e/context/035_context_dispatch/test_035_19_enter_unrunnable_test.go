package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An `enter` that cannot run is not a decline. A decline (the script ran and exited non-zero)
// leaves the context off on purpose; a script that is missing, not executable or has no shebang
// decided nothing, and reading it as "no" would leave the context off and every rule that reads
// it silently not firing, though the mode it enforces was meant to be on. So the event that
// triggered the enter is refused (a Pre* event denied, a Post* event refused at the Stop that
// handled it) naming the context, the script and the fix, and a Stop is refused while a declared
// enter or exit cannot run, even for a context that never triggered.

const onPreWrite = `on:
  - event: PreFileWrite
    match: event.path endsWith ".md"
enter: ./enter.sh
exit: ./exit.sh
`

const onPostWrite = `on:
  - event: PostFileWrite
    match: event.path endsWith ".md"
enter: ./enter.sh
exit: ./exit.sh
`

const enterActivates = "#!/bin/sh\ncat >/dev/null\nprintf '{\"on\":\"yes\"}'\nexit 0\n"
const enterDeclines = "#!/bin/sh\ncat >/dev/null\nexit 1\n"

func chmodScript(t *testing.T, proj, name, file string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(filepath.Join(proj, ".sloprail", "context", name, file), mode); err != nil {
		t.Fatalf("chmod: %v", err)
	}
}

func wantAll(t *testing.T, what, got string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("%s: missing %q in:\n%s", what, w, got)
		}
	}
}

// T035_19 (a): a non-executable enter on a Pre trigger refuses the write, naming the context,
// the script and the fix.
func TestT035_19_UnrunnableEnterRefusesThePreTrigger(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Context(proj, "guarded", onPreWrite, map[string]string{"enter.sh": enterActivates, "exit.sh": exitNever})
	chmodScript(t, proj, "guarded", "enter.sh", 0o644)

	got := e.Run(proj, "s-035-19", "write a note", Turns("done", Write("w1", "notes.md", "hello")))
	if !got.Refused() {
		t.Fatalf("an enter that could not run was read as a decline: the write was permitted:\n%s", got.Output)
	}
	wantAll(t, "the refusal", strings.Join(got.Refusals(), "\n"), "guarded", "enter.sh", "chmod +x", "could not be entered", "cannot be judged")
	if active, _ := e.ContextState(proj, "s-035-19", "guarded"); active {
		t.Errorf("the context is active though its enter never ran")
	}
}

// T035_20 (b), the control: an enter that RUNS and exits non-zero is a decline and refuses
// nothing.
func TestT035_20_DecliningEnterDoesNotRefuse(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Context(proj, "guarded", onPreWrite, map[string]string{"enter.sh": enterDeclines, "exit.sh": exitNever})

	got := e.Run(proj, "s-035-20", "write a note", Turns("done", Write("w1", "notes.md", "hello")))
	if got.Refused() {
		t.Fatalf("an enter that ran and declined refused the write:\n%s", got.Output)
	}
	if n := len(e.BlockingErrorsFrom(proj, "s-035-20", "Stop")); n != 0 {
		t.Errorf("a declining enter blocked the Stop: %v", e.BlockingErrorsFrom(proj, "s-035-20", "Stop"))
	}
	if active, _ := e.ContextState(proj, "s-035-20", "guarded"); active {
		t.Errorf("a declined enter activated the context")
	}
}

// A gate that reads the context in its match, bound to the same later event.
const gateReadsContext = `on:
  - event: PreFileWrite
    match: context["guarded"].active and event.path endsWith ".md"
checks:
  - script: ./refuse.sh
`

// T035_21 (c): the rule that reads the context is not silently skipped. With the enter broken,
// the write that triggered it is refused (it does not land unjudged); once the script is fixed
// the context enters and the gate that reads it refuses the next write with its own reason.
func TestT035_21_GateReadingTheContextIsNotSilentlySkipped(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Context(proj, "guarded", onPreWrite, map[string]string{"enter.sh": enterActivates, "exit.sh": exitNever})
	e.Gate(proj, "mode-gate", gateReadsContext, map[string]string{
		"refuse.sh": "#!/bin/sh\ncat >/dev/null\necho 'the guarded mode forbids notes' >&2\nexit 1\n",
	})
	chmodScript(t, proj, "guarded", "enter.sh", 0o644)

	sess := "s-035-21"
	first := e.Run(proj, sess, "write a note", Turns("done", Write("w1", "notes.md", "one")))
	if !first.Refused() {
		t.Fatalf("with the enter unrunnable the write went through unjudged:\n%s", first.Output)
	}
	wantAll(t, "first refusal", strings.Join(first.Refusals(), "\n"), "guarded", "enter.sh", "chmod +x")
	if _, err := os.Stat(filepath.Join(proj, "notes.md")); err == nil {
		t.Errorf("the refused write landed")
	}

	chmodScript(t, proj, "guarded", "enter.sh", 0o755)
	second := e.Run(proj, sess, "write a note again", Turns("done", Write("w2", "notes.md", "two")))
	if !second.Refused() || !second.Saw("the guarded mode forbids notes") {
		t.Fatalf("once the context could enter, the gate reading it did not refuse:\n%s", second.Output)
	}
	if active, _ := e.ContextState(proj, sess, "guarded"); !active {
		t.Errorf("the fixed enter did not activate the context")
	}
}

// T035_22 (d): a Post* trigger cannot deny (the work is done); the Stop that handled it is
// refused, naming context, script and fix, and the next Stop passes once the script is fixed.
func TestT035_22_UnrunnableEnterOnPostTriggerRefusesTheStop(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Context(proj, "guarded", onPostWrite, map[string]string{"enter.sh": enterActivates, "exit.sh": exitNever})
	e.CommitAll(proj, "before the session")
	chmodScript(t, proj, "guarded", "enter.sh", 0o644)

	sess := "s-035-22"
	e.Run(proj, sess, "write a note", Turns("done", Write("w1", "notes.md", "one")))
	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) == 0 {
		t.Fatalf("the Stop passed though the context's enter could not run")
	}
	wantAll(t, "the Stop refusal", strings.Join(blocks, "\n"), "guarded", "enter.sh", "chmod +x", "cannot be judged")
	before := len(e.AllBlockingErrorsFrom(proj, sess, "Stop"))

	chmodScript(t, proj, "guarded", "enter.sh", 0o755)
	e.Run(proj, sess, "write another", Turns("done", Write("w2", "more.md", "two")))
	if after := len(e.AllBlockingErrorsFrom(proj, sess, "Stop")); after != before {
		t.Errorf("the Stop kept refusing after the script was fixed: %v", e.AllBlockingErrorsFrom(proj, sess, "Stop"))
	}
	if active, _ := e.ContextState(proj, sess, "guarded"); !active {
		t.Errorf("the fixed enter did not activate the context")
	}
}

// T035_23: a context that is never triggered but whose enter (or exit) cannot run is still named
// at Stop, and the Stop passes after the fix.
func TestT035_23_NeverTriggeredBrokenScriptsRefuseTheStop(t *testing.T) {
	for _, role := range []string{"enter", "exit"} {
		t.Run(role, func(t *testing.T) {
			e := New(t)
			proj := e.Project()
			e.GitInit(proj)
			// Triggers on a tag nobody writes.
			e.Context(proj, "dormant", researchContext, map[string]string{"enter.sh": enterOnResearch, "exit.sh": exitNever})
			e.CommitAll(proj, "before the session")
			chmodScript(t, proj, "dormant", role+".sh", 0o644)

			sess := "s-035-23-" + role
			e.Run(proj, sess, "do nothing relevant", Turns("done", Write("w1", "a.txt", "x")))
			blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
			if len(blocks) == 0 {
				t.Fatalf("the Stop passed though the %s script could not run", role)
			}
			wantAll(t, "the Stop refusal", strings.Join(blocks, "\n"), "dormant", role+".sh", "chmod +x")
			before := len(e.AllBlockingErrorsFrom(proj, sess, "Stop"))

			chmodScript(t, proj, "dormant", role+".sh", 0o755)
			e.Run(proj, sess, "again", Turns("done", Write("w2", "b.txt", "y")))
			if after := len(e.AllBlockingErrorsFrom(proj, sess, "Stop")); after != before {
				t.Errorf("the Stop kept refusing after the %s script was fixed", role)
			}
		})
	}
}

// T035_24: a PostTagWrite trigger whose enter cannot run refuses the Stop too.
func TestT035_24_UnrunnableEnterOnTagRefusesTheStop(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Context(proj, "research-run", researchContext, map[string]string{"enter.sh": enterOnResearch, "exit.sh": exitNever})
	e.CommitAll(proj, "before the session")
	chmodScript(t, proj, "research-run", "enter.sh", 0o644)

	sess := "s-035-24"
	e.Run(proj, sess, "declare research", Turns("done", Say("m1", "I'll dig into this as a #research task.")))
	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) == 0 {
		t.Fatalf("the Stop passed though the tag-triggered enter could not run")
	}
	wantAll(t, "the Stop refusal", strings.Join(blocks, "\n"), "research-run", "enter.sh", "chmod +x")
}
