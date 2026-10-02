package splitrequire

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/sloprail/sloprail/internal/declaration"
)

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

const mixed = `# why
match: path startsWith "docs/"
# the requirement
require:
  - skill: authoring-guardrails
    files: [gate.md]
  - citation: {source_types: [user]}
    when: ./needs.sh
  - context: refactoring
checks:
  - script: ./check.sh
`

func TestRun_SplitsMixedGuardAndIsIdempotent(t *testing.T) {
	root := t.TempDir()
	g := filepath.Join(root, ".sloprail/file-guard/docs/file-guard.yaml")
	write(t, g, mixed)

	if _, err := Run([]string{root}, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(g); string(b) != mixed {
		t.Fatal("dry run changed the file-guard")
	}
	if _, err := os.Stat(filepath.Join(root, ".sloprail/gate/docs-requires")); err == nil {
		t.Fatal("dry run wrote the gate")
	}

	acts, err := Run([]string{root}, true, io.Discard)
	if err != nil || len(acts) != 1 || acts[0].Err != nil {
		t.Fatalf("apply: %+v %v", acts, err)
	}
	gate, err := os.ReadFile(filepath.Join(root, ".sloprail/gate/docs-requires/gate.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"event: PreFileWrite", `event.path startsWith "docs/"`, "skill: authoring-guardrails", "files: [gate.md]", "context: refactoring"} {
		if !strings.Contains(string(gate), want) {
			t.Errorf("gate lacks %q:\n%s", want, gate)
		}
	}
	if strings.Contains(string(gate), "citation") {
		t.Errorf("a citation moved to the gate:\n%s", gate)
	}
	left, _ := os.ReadFile(g)
	if strings.Contains(string(left), "skill:") || strings.Contains(string(left), "context:") {
		t.Errorf("session entries stayed on the file-guard:\n%s", left)
	}
	for _, want := range []string{"citation: {source_types: [user]}", "when: ./needs.sh", "checks:", "gate/docs-requires/gate.yaml"} {
		if !strings.Contains(string(left), want) {
			t.Errorf("file-guard lost %q:\n%s", want, left)
		}
	}
	var fg declaration.FileGuard
	if err := yaml.Unmarshal(left, &fg); err != nil {
		t.Fatal(err)
	}
	if problems := declaration.ValidateFileGuard(fg, declaration.Env{}); len(problems) > 0 {
		t.Errorf("converted file-guard does not validate: %+v", problems)
	}

	acts, err = Run([]string{root}, true, io.Discard)
	if err != nil || len(acts) != 0 {
		t.Fatalf("second run was not a no-op: %+v %v", acts, err)
	}
}

func TestRun_ReusesCoveringGateAndDropsEmptyGuard(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "file-guard/read-doc/file-guard.yaml"), "match: \"docs/*.md\"\nrequire:\n  - skill: s\n    files: [a.md]\n")
	write(t, filepath.Join(root, "gate/read-doc/gate.yaml"), "on:\n  - event: PreFileWrite\nrequire:\n  - skill: s\n    files: [a.md]\n")
	acts, err := Run([]string{root}, true, io.Discard)
	if err != nil || len(acts) != 1 || acts[0].NewGate != "" || !acts[0].Delete {
		t.Fatalf("%+v %v", acts, err)
	}
	if _, err := os.Stat(filepath.Join(root, "file-guard/read-doc")); err == nil {
		t.Error("an empty file-guard was left behind")
	}
}

func TestRun_GateWithDifferentRequireIsNotReused(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "file-guard/r/file-guard.yaml"), "match: \"docs/*.md\"\nrequire:\n  - skill: s\n")
	write(t, filepath.Join(root, "gate/r/gate.yaml"), "on:\n  - event: PreFileWrite\nrequire:\n  - skill: other\n")
	acts, err := Run([]string{root}, true, io.Discard)
	if err != nil || len(acts) != 1 || acts[0].NewGate == "" || acts[0].Err != nil {
		t.Fatalf("%+v %v", acts, err)
	}
	if _, err := os.Stat(filepath.Join(root, "gate/r-requires/gate.yaml")); err != nil {
		t.Error(err)
	}
}
