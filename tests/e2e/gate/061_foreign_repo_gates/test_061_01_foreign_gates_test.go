package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

type Env = harness.Env

const refuseCheck = `#!/bin/sh
cat >/dev/null
echo '{"reason":"sibling-rule: nothing is written under locked/ here"}'
exit 1
`

const lockedGate = `on:
  - event: PreFileWrite
    match: event.path startsWith "locked/"
checks:
  - script: ./check.sh
`

const noCommitGate = `on:
  - event: PreCommandInvoke
    match: any(event.invocations, .bin == "git" and "commit" in .argv)
checks:
  - script: ./check.sh
`

// sibling makes a git repo B beside the session's project A, with the given gate.
func sibling(e *Env, withGate bool, gate string) string {
	b := e.Project()
	e.GitInit(b)
	if withGate {
		e.Gate(b, "sibling-rule", gate, map[string]string{"check.sh": refuseCheck})
	}
	return b
}

func projectA(e *Env) string {
	a := e.Project()
	e.GitInit(a)
	return a
}

// T061_01: a Write into B from a session in A is refused with B's gate reason, naming B.
func TestT061_01_WriteIntoSiblingIsRefusedByItsGate(t *testing.T) {
	e := New(t)
	a, b := projectA(e), sibling(e, true, lockedGate)
	res := e.Run(a, "s-061-01", "write into the sibling", Turns("done",
		Write("w1", filepath.Join(b, "locked", "x.txt"), "no"),
	))
	if !res.Refused() {
		t.Fatalf("a write into B's locked/ was not refused:\n%s", res.Output)
	}
	if !res.Saw("sibling-rule: nothing is written under locked/") || !res.Saw(filepath.Base(b)) {
		t.Errorf("the refusal does not carry B's reason and name B:\n%s", res.Output)
	}
	if _, err := os.Stat(filepath.Join(b, "locked", "x.txt")); err == nil {
		t.Errorf("the refused write landed")
	}
}

// T061_02: a write into B the gate does not select is permitted.
func TestT061_02_WriteElsewhereInSiblingIsPermitted(t *testing.T) {
	e := New(t)
	a, b := projectA(e), sibling(e, true, lockedGate)
	res := e.Run(a, "s-061-02", "write into the sibling", Turns("done",
		Write("w1", filepath.Join(b, "open", "x.txt"), "fine"),
	))
	if res.Refused() {
		t.Fatalf("a write outside locked/ was refused:\n%s", res.Output)
	}
	if _, err := os.Stat(filepath.Join(b, "open", "x.txt")); err != nil {
		t.Errorf("the permitted write did not land: %v", err)
	}
}

// T061_03: `git -C B commit` hits B's Bash gate, and `cd B && git commit` does too.
func TestT061_03_CommandTargetingSiblingHitsItsBashGate(t *testing.T) {
	for i, command := range []string{
		"git -C %s commit -q --allow-empty -m x",
		"cd %s && git commit -q --allow-empty -m x",
	} {
		e := New(t)
		a, b := projectA(e), sibling(e, true, noCommitGate)
		res := e.Run(a, "s-061-03-"+string(rune('a'+i)), "commit in the sibling", Turns("done",
			Bash("c", fmt.Sprintf(command, b)),
		))
		if !res.Refused() {
			t.Fatalf("%q was not refused by B's gate:\n%s", command, res.Output)
		}
		if !res.Saw("sibling-rule") || !res.Saw(filepath.Base(b)) {
			t.Errorf("the refusal does not carry B's reason and name B:\n%s", res.Output)
		}
	}
}

// T061_04: a command in A that does not target B is untouched by B's gate.
func TestT061_04_CommandInOwnProjectIsNotJudgedByTheSibling(t *testing.T) {
	e := New(t)
	a := projectA(e)
	sibling(e, true, noCommitGate)
	res := e.Run(a, "s-061-04", "commit in own project", Turns("done",
		Bash("c", "git commit -q --allow-empty -m x"),
	))
	if res.Refused() {
		t.Fatalf("a commit in A was refused by B's gate:\n%s", res.Output)
	}
}

// T061_05: a sibling repo with no .sloprail is untouched.
func TestT061_05_SiblingWithoutDotSloprailIsUntouched(t *testing.T) {
	e := New(t)
	a, b := projectA(e), sibling(e, false, "")
	res := e.Run(a, "s-061-05", "write and commit in the sibling", Turns("done",
		Write("w1", filepath.Join(b, "locked", "x.txt"), "fine"),
		Bash("c", fmt.Sprintf("git -C %s commit -q --allow-empty -m x", b)),
	))
	if res.Refused() {
		t.Fatalf("a sibling with no .sloprail refused:\n%s", res.Output)
	}
}

// T061_06: a sibling whose .sloprail cannot be loaded refuses, naming it.
func TestT061_06_UnreadableSiblingDeclarationsRefuse(t *testing.T) {
	e := New(t)
	a, b := projectA(e), sibling(e, true, lockedGate)
	e.WriteFile(b, ".sloprail/config.yaml", "disabled: [unclosed\n  - : :\n")
	res := e.Run(a, "s-061-06", "write into the sibling", Turns("done",
		Write("w1", filepath.Join(b, "open", "x.txt"), "fine"),
	))
	if !res.Refused() {
		t.Fatalf("a write into a sibling with a broken .sloprail was not refused:\n%s", res.Output)
	}
	if !res.Saw(filepath.Base(b)) || !res.Saw("could not be read") {
		t.Errorf("the refusal does not name the sibling and say it could not be read:\n%s", res.Output)
	}
}

// T061_07: a command line that runs git in B and ALSO commits in A is judged for B only by the
// part that runs in B: A's commit is not B's to refuse.
func TestT061_07_OnlyTheInvocationsInTheSiblingAreItsToJudge(t *testing.T) {
	e := New(t)
	a, b := projectA(e), sibling(e, true, noCommitGate)
	res := e.Run(a, "s-061-07", "status in the sibling, commit here", Turns("done",
		Bash("c", fmt.Sprintf("git -C %s status -q && git commit -q --allow-empty -m x", b)),
	))
	if res.Refused() {
		t.Fatalf("A's own commit was refused by B's gate:\n%s", res.Output)
	}
}

// T061_08: one gate in B that does not load refuses a Write and a Bash command into B, naming B.
func TestT061_08_OneBrokenSiblingGateRefuses(t *testing.T) {
	e := New(t)
	a, b := projectA(e), sibling(e, true, lockedGate)
	e.Gate(b, "broken", "on: [this is not a gate\n", nil)
	res := e.Run(a, "s-061-08", "write into the sibling", Turns("done",
		Write("w1", filepath.Join(b, "open", "x.txt"), "fine"),
	))
	if !res.Refused() {
		t.Fatalf("a write into a sibling with a broken gate was not refused:\n%s", res.Output)
	}
	if !res.Saw(filepath.Base(b)) || !res.Saw("could not be loaded") {
		t.Errorf("the refusal does not name the sibling and the unloaded declaration:\n%s", res.Output)
	}
	res = e.Run(a, "s-061-08b", "commit in the sibling", Turns("done",
		Bash("c", fmt.Sprintf("git -C %s commit -q --allow-empty -m x", b)),
	))
	if !res.Refused() {
		t.Fatalf("a command into a sibling with a broken gate was not refused:\n%s", res.Output)
	}
}

const noDeleteGate = `on:
  - event: PreFileDelete
    match: event.path startsWith "locked/"
checks:
  - script: ./check.sh
`

// T061_09: project A with no declarations at all does not let a write into B skip B's structure
// gate (the session's own dispatch has nothing to do and returns early).
func TestT061_09_SiblingStructureGateAppliesWhenTheSessionProjectHasNoDeclarations(t *testing.T) {
	e := New(t)
	a, b := projectA(e), sibling(e, false, "")
	e.StructureGate(b, "allow:\n  - glob: \"docs/**\"\n")
	res := e.Run(a, "s-061-09", "write into the sibling", Turns("done",
		Write("w1", filepath.Join(b, "src", "main.go"), "package main"),
	))
	if !res.Refused() {
		t.Fatalf("a write outside B's structure was not refused:\n%s", res.Output)
	}
	if _, err := os.Stat(filepath.Join(b, "src", "main.go")); err == nil {
		t.Errorf("the refused write landed")
	}
	res = e.Run(a, "s-061-09b", "write into the sibling", Turns("done",
		Write("w1", filepath.Join(b, "docs", "note.md"), "# note"),
	))
	if res.Refused() {
		t.Fatalf("a write B's structure allows was refused:\n%s", res.Output)
	}
}

// T061_10: an Edit of a file in B outside B's structure is refused by B's structure gate, with
// project A declaring nothing.
func TestT061_10_EditIntoSiblingIsRefusedByItsStructure(t *testing.T) {
	e := New(t)
	a, b := projectA(e), sibling(e, false, "")
	e.StructureGate(b, "allow:\n  - glob: \"docs/**\"\n")
	e.WriteFile(b, "src/x.txt", "old\n")
	res := e.Run(a, "s-061-10", "edit in the sibling", Turns("done",
		Edit("e1", filepath.Join(b, "src", "x.txt"), "old", "new"),
	))
	if !res.Refused() {
		t.Fatalf("an Edit outside B's structure was not refused:\n%s", res.Output)
	}
	if !res.Saw(filepath.Base(b)) {
		t.Errorf("the refusal does not name B:\n%s", res.Output)
	}
	if body, _ := os.ReadFile(filepath.Join(b, "src", "x.txt")); string(body) != "old\n" {
		t.Errorf("the refused edit landed: %q", body)
	}
}

// T061_11: deleting a file in B's locked/ is refused by B's PreFileDelete gate.
func TestT061_11_DeleteInSiblingIsRefusedByItsGate(t *testing.T) {
	e := New(t)
	a, b := projectA(e), sibling(e, true, noDeleteGate)
	e.WriteFile(b, "locked/x.txt", "keep\n")
	res := e.Run(a, "s-061-11", "delete in the sibling", Turns("done",
		Bash("d", fmt.Sprintf("rm %s", filepath.Join(b, "locked", "x.txt"))),
	))
	if !res.Refused() {
		t.Fatalf("a delete in B's locked/ was not refused:\n%s", res.Output)
	}
	if _, err := os.Stat(filepath.Join(b, "locked", "x.txt")); err != nil {
		t.Errorf("the refused delete removed the file: %v", err)
	}
}

// T061_12: a write through a symlink into B is B's: refused by B's structure gate, with project A
// declaring nothing.
func TestT061_12_SymlinkedPathIntoSiblingIsRefusedByItsStructure(t *testing.T) {
	e := New(t)
	a, b := projectA(e), sibling(e, false, "")
	e.StructureGate(b, "allow:\n  - glob: \"docs/**\"\n")
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(b, link); err != nil {
		t.Fatal(err)
	}
	res := e.Run(a, "s-061-12", "write through a symlink into the sibling", Turns("done",
		Write("w1", filepath.Join(link, "src", "x.txt"), "no"),
	))
	if !res.Refused() {
		t.Fatalf("a write through a symlink outside B's structure was not refused:\n%s", res.Output)
	}
	if _, err := os.Stat(filepath.Join(b, "src", "x.txt")); err == nil {
		t.Errorf("the refused write landed")
	}
	res = e.Run(a, "s-061-12b", "write through a symlink into the sibling", Turns("done",
		Write("w1", filepath.Join(link, "docs", "n.md"), "# n"),
	))
	if res.Refused() {
		t.Fatalf("a write B's structure allows, through a symlink, was refused:\n%s", res.Output)
	}
}

// T061_13: an Edit of a file in B's locked/ is refused by B's gate.
func TestT061_13_EditIntoSiblingIsRefusedByItsGate(t *testing.T) {
	e := New(t)
	a, b := projectA(e), sibling(e, true, lockedGate)
	e.WriteFile(b, "locked/x.txt", "old\n")
	res := e.Run(a, "s-061-13", "edit in the sibling", Turns("done",
		Edit("e1", filepath.Join(b, "locked", "x.txt"), "old", "new"),
	))
	if !res.Refused() {
		t.Fatalf("an Edit of B's locked/ file was not refused:\n%s", res.Output)
	}
	if !res.Saw("sibling-rule") || !res.Saw(filepath.Base(b)) {
		t.Errorf("the refusal does not carry B's reason and name B:\n%s", res.Output)
	}
	if body, _ := os.ReadFile(filepath.Join(b, "locked", "x.txt")); string(body) != "old\n" {
		t.Errorf("the refused edit landed: %q", body)
	}
}

// T061_14: a symlinked path into B is B's: the gate refuses a write through the link.
func TestT061_14_SymlinkedPathIntoSiblingIsRefusedByItsGate(t *testing.T) {
	e := New(t)
	a, b := projectA(e), sibling(e, true, lockedGate)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(b, link); err != nil {
		t.Fatal(err)
	}
	res := e.Run(a, "s-061-14", "write through a symlink into the sibling", Turns("done",
		Write("w1", filepath.Join(link, "locked", "x.txt"), "no"),
	))
	if !res.Refused() {
		t.Fatalf("a write through a symlink into B's locked/ was not refused:\n%s", res.Output)
	}
	if _, err := os.Stat(filepath.Join(b, "locked", "x.txt")); err == nil {
		t.Errorf("the refused write landed")
	}
}
