package e2e

import (
	"strings"
	"testing"
)

// A file may carry several sr:moved-from markers (one per moved block), in any
// comment style. Each marker must reconcile against ITS OWN origin range; the
// reconcile used to read only the first marker and compare the whole file to it,
// and to strip only `//` marker lines, so a correct Python move with two or more
// markers never reconciled, in the gate or in the file-guard.

// pyOrigin is origin.py: two functions, lines 1-2 and 4-5.
const pyOrigin = "def a():\n    return 1\n\ndef b():\n    return 2\n"

func setupPyOrigin(t *testing.T) (env *scene, sha string) {
	t.Helper()
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	e.WriteFile(proj, "origin.py", pyOrigin)
	e.CommitAll(proj, "install + origin")
	return &scene{e: e, proj: proj}, e.Git(proj, "rev-parse", "HEAD")
}

func pyMoved(sha, a, b string) string {
	return "import os\n" +
		"# sr:moved-from origin.py@" + sha + ":1-2\n" + a + "\n" +
		"# sr:moved-from origin.py@" + sha + ":4-5\n" + b
}

const (
	pyA = "def a():\n    return 1\n"
	pyB = "def b():\n    return 2\n"
)

// T041_11: the gate refuses a two-marker Python move whose SECOND block was
// regenerated, then admits the same move carrying the origin's bytes.
func TestT041_11_GateReconcilesEachMarkerPython(t *testing.T) {
	env, sha := setupPyOrigin(t)
	e, proj := env.e, env.proj
	decl := "Refactoring. #refactor (no scope)"

	bad := pyMoved(sha, pyA, "def b():\n    return 999\n")
	res := e.Run(proj, "s-041-11", "move both", Turns("done",
		SayWrite("w1", decl, "dest.py", bad),
	).ThenCommit("write the files"))
	if !res.Refused() {
		t.Fatalf("a Python move with a regenerated second block was NOT refused:\n%s", res.Output)
	}
	if !res.Saw(reconcileRefusal) || !res.Saw(":4-5") {
		t.Errorf("the refusal did not name the second marker's range:\n%s", res.Output)
	}
	if e.Exists(proj, "dest.py") {
		t.Errorf("the non-reconciling write landed")
	}

	good := pyMoved(sha, pyA, pyB)
	res = e.Run(proj, "s-041-11", "move both, correctly", Turns("done",
		SayWrite("w2", decl, "dest.py", good),
	).ThenCommit("write the files"))
	if res.Refused() {
		t.Fatalf("a correct two-marker Python move was refused:\n%s", res.Output)
	}
	if !e.Exists(proj, "dest.py") {
		t.Errorf("the correct two-marker move did not land")
	}
}

// T041_12: the same for `//` markers: two blocks, each against its own range.
func TestT041_12_GateReconcilesEachMarkerGo(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	e.WriteFile(proj, "origin.go", "func A() int {\n\treturn 1\n}\n\nfunc B() int {\n\treturn 2\n}\n")
	e.CommitAll(proj, "install + origin")
	sha := e.Git(proj, "rev-parse", "HEAD")
	decl := "Refactoring. #refactor (no scope)"
	mv := func(b string) string {
		return "// sr:moved-from origin.go@" + sha + ":1-3\nfunc A() int {\n\treturn 1\n}\n\n" +
			"// sr:moved-from origin.go@" + sha + ":5-7\n" + b
	}

	res := e.Run(proj, "s-041-12", "move both", Turns("done",
		SayWrite("w1", decl, "dest.go", mv("func B() int {\n\treturn 999\n}\n")),
	).ThenCommit("write the files"))
	if !res.Refused() || !res.Saw(reconcileRefusal) {
		t.Fatalf("a Go move with a regenerated second block was NOT refused:\n%s", res.Output)
	}

	res = e.Run(proj, "s-041-12", "move both, correctly", Turns("done",
		SayWrite("w2", decl, "dest.go", mv("func B() int {\n\treturn 2\n}\n")),
	).ThenCommit("write the files"))
	if res.Refused() || !e.Exists(proj, "dest.go") {
		t.Fatalf("a correct two-marker Go move was refused:\n%s", res.Output)
	}
}

// T041_13: the FILE-GUARD (Stop, committed bytes) reconciles each marker too. The
// file is written by a command the engine does not parse as a write (python), so
// the gate never sees it and only the file-guard can refuse.
func TestT041_13_FileGuardReconcilesEachMarkerPython(t *testing.T) {
	env, sha := setupPyOrigin(t)
	e, proj := env.e, env.proj
	decl := "Refactoring. #refactor (no scope)"

	write := func(body string) string {
		return "python3 -c 'import sys; open(\"dest.py\", \"w\").write(sys.argv[1])' " + shq(body)
	}

	e.Run(proj, "s-041-13", "move both", Turns("done",
		SayWrite("w0", decl, "note.md", "declared"),
		Bash("b1", write(pyMoved(sha, pyA, "def b():\n    return 999\n"))),
	).ThenCommit("regenerated second block"))
	blocks := strings.Join(e.BlockingErrorsFrom(proj, "s-041-13", "Stop"), "\n")
	if !strings.Contains(blocks, reconcileRefusal) || !strings.Contains(blocks, ":4-5") {
		t.Fatalf("the file-guard did not refuse the regenerated second block:\n%s", blocks)
	}

	e.Run(proj, "s-041-13", "fix it", Turns("done",
		Bash("b2", write(pyMoved(sha, pyA, pyB))),
	).ThenCommit("carry the origin bytes"))
	if res := e.CheckRunRaw(proj, "s-041-13", e.RunBase("s-041-13"), "HEAD"); strings.Contains(res.Output, reconcileRefusal) {
		t.Fatalf("the file-guard still refused a correct two-marker Python move:\n%s", res.Output)
	}
}

func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
