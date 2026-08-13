package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// T002_01: init creates the directory guardrails go in.
func TestT002_01_InitCreatesGuardrailsDir(t *testing.T) {
	e := New(t)
	proj := e.Project()

	got := e.CLI(proj, "init")
	if got.Code != 0 {
		t.Fatalf("init exited %d:\n%s", got.Code, got.Output)
	}

	dir := filepath.Join(proj, ".sloprail", "guardrails")
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("want %s created, got: %v\n%s", dir, err, got.Output)
	}
	if !info.IsDir() {
		t.Fatalf("%s is not a directory", dir)
	}
}

// T002_02: init scaffolds no guardrail.
//
// The decision this pins down: rules are authored by an agent that has read how
// they work, and an example rule left in a project is a rule nobody chose,
// sitting there as though someone had. A later change that "helpfully" seeds one
// should fail here rather than ship.
func TestT002_02_InitScaffoldsNoGuardrail(t *testing.T) {
	e := New(t)
	proj := e.Project()

	e.CLI(proj, "init")

	entries, err := os.ReadDir(filepath.Join(proj, ".sloprail", "guardrails"))
	if err != nil {
		t.Fatalf("read guardrails dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("init left %d entries in guardrails/, want none: %v", len(entries), entries)
	}
}

// T002_03: an initialised project with nothing declared sees no difference.
//
// The property the whole command is for. A project that has adopted sloprail and
// declared nothing is an ordinary state, not a broken one — so a session in it
// has to be indistinguishable from a session in a project that never installed
// this. Driven through the mock, because "the engine loads cleanly" is only
// worth anything if it holds where the harness actually calls it.
func TestT002_03_InitialisedProjectPermitsEverything(t *testing.T) {
	e := New(t)
	proj := e.Project()

	e.CLI(proj, "init")

	got := e.Run(proj, "s-002-03", "write a note", Turns("done",
		Write("w1", "guarded/notes.md", "hello"),
	))

	if got.Saw("denied") || got.Saw("blocked") || got.Saw("sloprail:") {
		t.Fatalf("an initialised project with no guardrails was not silent:\n%s", got.Output)
	}
}

// T002_04: running init twice does not clobber a project that has guardrails.
//
// Someone re-running setup is how they check setup ran. An init that reset the
// directory would be a rule-deleting command wearing a setup command's name, and
// the loss would be silent.
func TestT002_04_InitIsIdempotent(t *testing.T) {
	e := New(t)
	proj := e.Project()

	e.CLI(proj, "init")
	e.Guardrail(proj, "already-here", "---\nhooks: {}\n---\n\nkeep me\n", nil)

	got := e.CLI(proj, "init")
	if got.Code != 0 {
		t.Fatalf("second init exited %d:\n%s", got.Code, got.Output)
	}

	body, err := os.ReadFile(filepath.Join(proj, ".sloprail", "guardrails", "already-here", "GUARDRAIL.md"))
	if err != nil {
		t.Fatalf("second init lost an existing guardrail: %v", err)
	}
	if string(body) != "---\nhooks: {}\n---\n\nkeep me\n" {
		t.Fatalf("second init rewrote an existing guardrail:\n%s", body)
	}
}

// T002_05: init fails when the guardrails path is occupied by a file.
//
// "Already exists, left as it is" would be a lie here: Load's ReadDir cannot
// read a file, so no guardrail could ever be declared. Setup reporting success
// over a project that cannot work is worse than setup failing, because the
// failure surfaces later and somewhere else.
func TestT002_05_InitRefusesWhenPathIsAFile(t *testing.T) {
	e := New(t)
	proj := e.Project()

	if err := os.MkdirAll(filepath.Join(proj, ".sloprail"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(proj, ".sloprail", "guardrails"), []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got := e.CLI(proj, "init")
	if got.Code == 0 {
		t.Fatalf("init reported success over a file where the directory belongs:\n%s", got.Output)
	}
	if !strings.Contains(got.Output, "not a directory") {
		t.Errorf("the error does not say what is wrong:\n%s", got.Output)
	}
}
