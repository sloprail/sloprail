package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

// T033_08: a write OUTSIDE the project tree is not the project structure's to
// decide. The structure governs the project; a scratch file in a temp dir is
// somewhere else. Measured in the onboarding eval: an agent's new structure
// refused its own scratch copy under /tmp, the very place the plugin tells
// agents to keep throwaway files.
func TestT033_08_WriteOutsideTheProjectIsNotGated(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.StructureGate(proj, structureYAML)

	scratch := filepath.Join(t.TempDir(), "scratch.txt")
	res := e.Run(proj, "s-033-08", "write a scratch file", Turns("done",
		Write("w1", scratch, "throwaway"),
	))

	if res.Refused() {
		t.Errorf("a write outside the project tree was refused by the project's structure:\n%s", res.Output)
	}
	if _, err := os.Stat(scratch); err != nil {
		t.Errorf("the scratch write outside the project did not land: %v", err)
	}
}

// T033_09: the control for T033_08 — an absolute path that is INSIDE the
// project is still the project's, and still refused when the structure does
// not allow it. Without this, "absolute means outside" would be a hole.
func TestT033_09_AbsolutePathInsideTheProjectIsStillGated(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.StructureGate(proj, structureYAML)

	res := e.Run(proj, "s-033-09", "write somewhere forbidden", Turns("done",
		Write("w1", filepath.Join(proj, "src", "main.go"), "package main"),
	))

	if !res.Refused() {
		t.Errorf("an absolute path inside the project, outside the allowlist, was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, "src/main.go") {
		t.Errorf("the forbidden write LANDED")
	}
}

// T033_10: a write into ANOTHER project that has its own .sloprail/ is decided
// by that project's structure — each project's structure governs its own tree.
// Not refused by this project's (it has no say there), and not let through
// unchecked either (the other project's rules still hold when written into
// from outside).
func TestT033_10_AnotherProjectsStructureGovernsItsTree(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.StructureGate(proj, structureYAML)

	other := e.Project()
	e.GitInit(other)
	e.StructureGate(other, "allow:\n  - glob: \"docs/**\"\n")

	res := e.Run(proj, "s-033-10a", "write into the other project", Turns("done",
		Write("w1", filepath.Join(other, "src", "main.go"), "package main"),
	))
	if !res.Refused() {
		t.Errorf("a write into another project, outside ITS structure, was not refused:\n%s", res.Output)
	}
	if _, err := os.Stat(filepath.Join(other, "src", "main.go")); err == nil {
		t.Errorf("the write into the other project landed despite its structure")
	}
	if !res.Saw(other) && !res.Saw("its own structure governs its tree") {
		t.Errorf("the refusal does not say which project's structure refused it:\n%s", res.Output)
	}

	res = e.Run(proj, "s-033-10b", "write into the other project", Turns("done",
		Write("w1", filepath.Join(other, "docs", "note.md"), "# note"),
	))
	if res.Refused() {
		t.Errorf("a write the other project's structure allows was refused:\n%s", res.Output)
	}
	if _, err := os.Stat(filepath.Join(other, "docs", "note.md")); err != nil {
		t.Errorf("the allowed write into the other project did not land: %v", err)
	}
}
