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
