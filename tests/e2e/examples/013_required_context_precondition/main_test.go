package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The agent is a10n-claude-mock with this repo's plugin enabled, so what fires
// during a test is the wiring a user would get.
var (
	New   = harness.New
	Turns = harness.Turns
)

// TestMain removes the binary build dir when this package's tests finish.
// Without it every e2e package leaks 15M for the life of the machine.
func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

// exampleName is the folder the example ships under, and therefore the
// guardrail's name — the engine takes a guardrail's name from its folder.
const exampleName = "required-context-precondition"

// installExample copies the shipped example into a project, verbatim.
//
// Read off disk rather than restated as a const in this file. A test holding
// its own copy of the declaration proves that copy works and says nothing about
// the file a user would lift, which is the only thing this example is for — and
// the two would drift the first time either was edited alone.
func installExample(t *testing.T, projDir string) {
	t.Helper()

	src := filepath.Join(repoRoot(t), "examples", "guardrails", exampleName)
	dst := filepath.Join(projDir, ".sloprail", "guardrails", exampleName)
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatalf("install example: mkdir: %v", err)
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("install example: read %s: %v", src, err)
	}
	if len(entries) == 0 {
		t.Fatalf("install example: %s is empty", src)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		body, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatalf("install example: read %s: %v", e.Name(), err)
		}
		info, err := e.Info()
		if err != nil {
			t.Fatalf("install example: stat %s: %v", e.Name(), err)
		}
		// The mode is carried over, not fixed at 0644. A hook script that
		// arrives without its execute bit is refused by the engine for being
		// unrunnable, and the test would then pass its refusal assertions for
		// entirely the wrong reason.
		if err := os.WriteFile(filepath.Join(dst, e.Name()), body, info.Mode().Perm()); err != nil {
			t.Fatalf("install example: write %s: %v", e.Name(), err)
		}
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}
	return strings.TrimSpace(string(out))
}
