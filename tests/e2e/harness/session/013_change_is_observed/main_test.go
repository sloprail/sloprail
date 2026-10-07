package e2e

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// Env is the harness environment, aliased so scenario helpers in this package
// can take one without naming the import at every call site.
type Env = harness.Env

var (
	Turns = harness.Turns
	Write = harness.Write
	Bash  = harness.Bash
)

// writeFile puts a file into the project directly, without going through the
// agent — the project's own pre-existing content, as opposed to anything the
// cycle did.
func writeFile(t *testing.T, projDir, name, content string) {
	t.Helper()
	path := filepath.Join(projDir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("seed %s: %v", name, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("seed %s: %v", name, err)
	}
}

// TestMain removes the binary build dir when this package's tests finish.
// Without it every e2e package leaks 15M for the life of the machine.
func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

// New is harness.New with the plugin's authoring file-guards switched off: this package
// is about other rules, and the authoring guards would judge the rules' own files.
func New(t *testing.T) *Env { return harness.New(t, harness.WithoutShippedFileGuards()) }
