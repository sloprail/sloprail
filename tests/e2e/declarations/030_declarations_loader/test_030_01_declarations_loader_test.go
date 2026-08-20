package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTree writes a set of relative files under dir, creating parents. Used to
// stand up a `.sloprail` tree a test then loads.
func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
}

// T030_01: a project whose declarations are all sound loads clean and exits 0.
//
// This is the whole-binary counterpart to the loader's unit tests — it proves the
// command wired the loader to the real module registry and reports success with
// the status a caller reads, not just that the package function returns no
// Invalid.
func TestT030_01_ValidDeclarationsLoadCleanAndExitZero(t *testing.T) {
	e := New(t)
	proj := t.TempDir()

	writeTree(t, proj, map[string]string{
		".sloprail/gate/require-skill/gate.yaml": `
on:
  - event: PreFileWrite
    match: event.path startsWith "memories/topics/"
require:
  - skill: document-topic
`,
		".sloprail/context/refactoring/context.yaml": `
on:
  - event: PreToolUse
enter: ./enter.sh
exit: ./exit.sh
`,
		".sloprail/file-guard/pinned/file-guard.yaml": `
match: any(markers, .kind == "invariant")
checks:
  - script: ./check.sh
`,
		".sloprail/goal/accuracy/goal.yaml": `
enabled: true
script: verify.sh
`,
		".sloprail/file-guard/structure.yaml": `
allow:
  - glob: "memories/*.md"
`,
	})

	res := e.CLIDirect(proj, "sr-file", "declarations", proj)
	if res.Code != 0 {
		t.Fatalf("expected exit 0 for sound declarations, got %d:\n%s", res.Code, res.Output)
	}
	// The report names each nature's loaded declarations.
	for _, want := range []string{"require-skill", "refactoring", "pinned", "accuracy", "structure gate: present"} {
		if !strings.Contains(res.Output, want) {
			t.Errorf("expected the report to mention %q:\n%s", want, res.Output)
		}
	}
}

// T030_02: an invalid declaration is reported by name with its fault, and the
// command exits 1 — the status a CI step or hook reads. This is the load-time
// refusal the whole slice exists to produce, observed through the binary.
func TestT030_02_InvalidDeclarationIsReportedAndExitsOne(t *testing.T) {
	e := New(t)
	proj := t.TempDir()

	writeTree(t, proj, map[string]string{
		// A gate on a Post event — too late to gate, refused at load.
		".sloprail/gate/late/gate.yaml": `
on:
  - event: PostFileCreate
checks:
  - script: ./s.sh
`,
	})

	res := e.CLIDirect(proj, "sr-file", "declarations", proj)
	if res.Code != 1 {
		t.Fatalf("expected exit 1 for an invalid declaration, got %d:\n%s", res.Code, res.Output)
	}
	// The refusal names the gate and the offending kind.
	if !strings.Contains(res.Output, "gate/late") {
		t.Errorf("expected the report to name the invalid gate:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "PostFileCreate") {
		t.Errorf("expected the report to name the offending event kind:\n%s", res.Output)
	}
}

// T030_03: one bad declaration does not stop the others being reported as loaded.
// The engine loads every sound declaration and refuses only the broken one — a
// single typo must not disarm a whole project.
func TestT030_03_OneBadDoesNotDisarmTheRest(t *testing.T) {
	e := New(t)
	proj := t.TempDir()

	writeTree(t, proj, map[string]string{
		".sloprail/gate/good/gate.yaml": `
on:
  - event: Stop
checks:
  - script: ./s.sh
`,
		".sloprail/gate/bad/gate.yaml": `
on:
  - event: PostFileCreate
checks:
  - script: ./s.sh
`,
	})

	res := e.CLIDirect(proj, "sr-file", "declarations", proj)
	// Exits 1 because one is invalid, but the good one is still reported loaded.
	if res.Code != 1 {
		t.Fatalf("expected exit 1, got %d:\n%s", res.Code, res.Output)
	}
	if !strings.Contains(res.Output, "gates (1): good") {
		t.Errorf("the sound gate must still load and be reported:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "gate/bad") {
		t.Errorf("the broken gate must be reported invalid:\n%s", res.Output)
	}
}

// T030_04: the command accepts either a project root or the `.sloprail` path
// itself, naming the same tree — so an author need not remember which the command
// wanted.
func TestT030_04_AcceptsProjectRootOrDotDir(t *testing.T) {
	e := New(t)
	proj := t.TempDir()
	writeTree(t, proj, map[string]string{
		".sloprail/goal/accuracy/goal.yaml": "enabled: true\nscript: verify.sh\n",
	})

	viaRoot := e.CLIDirect(proj, "sr-file", "declarations", proj)
	viaDotDir := e.CLIDirect(proj, "sr-file", "declarations", filepath.Join(proj, ".sloprail"))

	if viaRoot.Code != 0 || viaDotDir.Code != 0 {
		t.Fatalf("both spellings should exit 0: root=%d dotdir=%d", viaRoot.Code, viaDotDir.Code)
	}
	if !strings.Contains(viaRoot.Output, "accuracy") || !strings.Contains(viaDotDir.Output, "accuracy") {
		t.Errorf("both spellings should load the goal:\n--- root ---\n%s\n--- dotdir ---\n%s", viaRoot.Output, viaDotDir.Output)
	}
}

// T030_05: every shipped new-format example loads clean through the binary. This
// is the reconciliation proof at the CLI level — pointing the real command at a
// real example's `.sloprail` exits 0, confirming the example grammar matches the
// spec-faithful scopes the loader enforces.
func TestT030_05_ShippedExamplesLoadCleanThroughTheBinary(t *testing.T) {
	e := New(t)
	repo := repoRootForExamples(t)

	// A representative spread across natures and both aliases: a gate with a
	// PreFileWrite alias (required-context-precondition), a context with a
	// PostFileWrite alias (eval-loop-maxing), a PostTagWrite context
	// (research-rigor), and a structure gate (completeness-artifact-on-trigger).
	for _, example := range []string{
		"required-context-precondition",
		"eval-loop-maxing",
		"research-rigor",
		"completeness-artifact-on-trigger",
		"interlinking",
		"marker-anchored-structure",
	} {
		example := example
		t.Run(example, func(t *testing.T) {
			dir := filepath.Join(repo, "examples", example)
			res := e.CLIDirect(dir, "sr-file", "declarations", dir)
			if res.Code != 0 {
				t.Fatalf("example %q must load clean through the binary, got exit %d:\n%s", example, res.Code, res.Output)
			}
		})
	}
}

// repoRootForExamples finds the module root so the shipped examples are reachable
// from the test's working directory.
func repoRootForExamples(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root (no go.mod up the tree)")
		}
		dir = parent
	}
}
