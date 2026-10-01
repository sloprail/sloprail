package e2e

import "testing"

// A PEP8 file has blank lines around every def. Blank lines are not content, so a
// move that keeps (or re-spaces) them reconciles, while a regenerated body still
// does not — and the refusal prints the first differing line, origin against new.

const pepOrigin = "def a():\n    return 1\n\n\ndef b():\n    return 2\n"

// T041_14: a PEP8 move with blank lines is refused when a line differs (naming
// the first differing line), then admitted when it carries the origin's lines.
func TestT041_14_BlankLinesDoNotBlockReconcile(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	e.WriteFile(proj, "origin.py", pepOrigin)
	e.CommitAll(proj, "install + origin")
	sha := e.Git(proj, "rev-parse", "HEAD")
	decl := "Refactoring. #refactor (no scope)"
	marker := "# sr:moved-from origin.py@" + sha + ":1-6\n"

	bad := marker + "def a():\n    return 1\n\n\ndef b():\n    return 999\n"
	res := e.Run(proj, "s-041-14", "move both", Turns("done",
		SayWrite("w1", decl, "dest.py", bad),
	).ThenCommit("write the files"))
	if !res.Refused() || !res.Saw(reconcileRefusal) {
		t.Fatalf("a regenerated PEP8 move was not refused:\n%s", res.Output)
	}
	if !res.Saw("First differing line: origin: 'return 2' / new: 'return 999'") {
		t.Errorf("the refusal did not print the first differing line:\n%s", res.Output)
	}
	if e.Exists(proj, "dest.py") {
		t.Errorf("the non-reconciling write landed")
	}

	// Same lines, blank lines re-spaced: still the origin's content.
	good := marker + "def a():\n    return 1\n\ndef b():\n\n    return 2\n"
	res = e.Run(proj, "s-041-14", "move both, correctly", Turns("done",
		SayWrite("w2", decl, "dest.py", good),
	).ThenCommit("write the files"))
	if res.Refused() {
		t.Fatalf("a PEP8 move carrying the origin's lines was refused:\n%s", res.Output)
	}
	if !e.Exists(proj, "dest.py") {
		t.Errorf("the PEP8 move did not land")
	}
}
