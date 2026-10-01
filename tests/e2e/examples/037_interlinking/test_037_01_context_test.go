package e2e

import (
	"testing"
)

// This file proves the REAL, working half of interlinking: the people-linked
// context's lifecycle and its per-cycle registry — activation on create and
// delete, distinct logging of two people in one turn, and match-narrowing to
// people/*.md. The gate's enforcement is a separate (broken) matter, pinned in
// test_037_02.

// T037_01: the context activates on a people/*.md CREATE and logs the touched
// path (keyed by path, valued by the event kind) into its own registry.
//
// This is the incremental-registry design at the heart of the unit: rather than a
// full-repo scan, each touched person is logged as it is touched. Here one person
// is created; the context enters and records people/alice.md → PostFileCreate.
func TestT037_01_ContextLogsCreatedPerson(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	e.CommitAll(proj, "install")

	sess := "s-037-01"
	e.Run(proj, sess, "add a person", Turns("done",
		Write("w1", "people/alice.md", "# Alice\nA person."),
	))

	reg := e.GuardrailState(proj, sess, "people-linked", "")
	if got, ok := reg["people/alice.md"]; !ok {
		t.Fatalf("the context did not log the created person; registry=%v", reg)
	} else if got != "PostFileCreate" {
		t.Errorf("the created person was logged with kind %q, want PostFileCreate", got)
	}
}

// T037_02: TWO people created in one turn are BOTH logged, distinctly.
//
// The property the example's registry-over-payload design exists for (its own
// comment: "a two-file turn ends up with both entries logged, not just the last
// one's payload overwriting the first's"). enter fires on EVERY matching
// occurrence, so both people/alice.md and people/bob.md land in the registry — a
// gate that could read it would then check BOTH, not only the last.
func TestT037_02_TwoPeopleBothLogged(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	e.CommitAll(proj, "install")

	sess := "s-037-02"
	e.Run(proj, sess, "add two people", Turns("done",
		Write("w1", "people/alice.md", "# Alice"),
		Write("w2", "people/bob.md", "# Bob"),
	))

	reg := e.GuardrailState(proj, sess, "people-linked", "")
	if _, ok := reg["people/alice.md"]; !ok {
		t.Errorf("alice was not logged; registry=%v", reg)
	}
	if _, ok := reg["people/bob.md"]; !ok {
		t.Errorf("bob was not logged (only the last touch survived?); registry=%v", reg)
	}
}

// T037_03: the context activates on a people/*.md DELETE and logs it as
// PostFileDelete.
//
// The deletion half of the unit: a removed person is logged with the delete kind,
// so a gate could check that no dangling links remain. The person is committed at
// the baseline, then removed with `rm` — the engine derives PostFileDelete from
// the tree diff (baseline had it, now absent), no special delete builder needed.
func TestT037_03_ContextLogsDeletedPerson(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	// The person exists at the baseline.
	e.WriteFile(proj, "people/carol.md", "# Carol")
	e.CommitAll(proj, "install + carol")

	sess := "s-037-03"
	e.Run(proj, sess, "remove carol", Turns("done",
		Bash("d1", "rm people/carol.md"),
	))

	reg := e.GuardrailState(proj, sess, "people-linked", "")
	if got, ok := reg["people/carol.md"]; !ok {
		t.Fatalf("the context did not log the deleted person; registry=%v", reg)
	} else if got != "PostFileDelete" {
		t.Errorf("the deleted person was logged with kind %q, want PostFileDelete", got)
	}
}

// T037_04: the context does NOT activate for a path outside people/*.md.
//
// The control for the trigger's match (`event.path startsWith "people/" and
// event.path endsWith ".md"`). A file under a different directory, and a non-.md
// file under people/, must neither activate the context nor land in its registry.
func TestT037_04_DoesNotFireOutsideScope(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	e.CommitAll(proj, "install")

	sess := "s-037-04"
	e.Run(proj, sess, "write non-people files", Turns("done",
		Write("w1", "notes/idea.md", "not a person"),
		Write("w2", "people/readme.txt", "not a markdown person file"),
	))

	if active, _ := e.ContextState(proj, sess, "people-linked"); active {
		t.Errorf("the context activated for a path outside people/*.md")
	}
	reg := e.GuardrailState(proj, sess, "people-linked", "")
	if len(reg) != 0 {
		t.Errorf("the context logged something for an out-of-scope touch: %v", reg)
	}
}
