package main

// CLI-docs generation lives as a test so it can call the unexported newRoot()
// without exporting it or adding a command to the shipped binary. It does
// nothing in a normal test run; with GEN_CLI_DOCS=<path> it writes this
// binary's Cobra command tree as JSON to that path. Driven by tools/clidocs/gen.sh.

import (
	"os"
	"testing"

	"github.com/sloprail/sloprail/internal/clidoc"
)

func TestGenerateCLIDocs(t *testing.T) {
	out := os.Getenv("GEN_CLI_DOCS")
	if out == "" {
		t.Skip("set GEN_CLI_DOCS=<file> to generate the CLI reference JSON")
	}
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := clidoc.Emit(newRoot(), f); err != nil {
		t.Fatal(err)
	}
}
