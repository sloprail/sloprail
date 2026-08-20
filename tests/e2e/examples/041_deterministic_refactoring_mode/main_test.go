package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// This package is the end-to-end for the deterministic-refactoring-mode USE CASE
// (strategy unit 12): a refactor must be MECHANICAL, not regenerated. A context
// (`refactoring`) activates when the agent declares `#refactor scope=...` in its
// prose, and a PREVENTIVE file-guard (`moved-content-reconciles`, PreToolUse)
// then blocks any write of a file carrying an `sr:moved-from` marker whose body
// does not reconcile byte-for-byte (minus imports/whitespace) against the origin
// range the marker pins. The context's exit at Stop also refuses if a declared
// marker was never written.
//
// This is the one composite in the wave that works end to end against the engine
// as shipped: it reads the context via the file-guard's `match:
// context["refactoring"].active` (the sanctioned channel, not the non-existent
// `state list --owner`) and resolves the origin via `git show <sha>:<path>` (no
// cwd-relative grep). Its scripts DO ship with the execute bit.
//
// # The combined turn
//
// The refactor must be DECLARED (a #refactor tag in prose) BEFORE the marked
// write, and both must be in one session. A pure-text Say turn is terminal in
// a10n-claude-mock (nothing to get a result for → the stream ends), so `Say(tag)`
// then `Write(marked)` never runs the write. SayWrite carries the tag's prose in
// the SAME block-list message as the Write tool_use, so the tag lands in the
// trajectory atomically with the write it guards — see harness.SayWrite.
var (
	New      = harness.New
	Turns    = harness.Turns
	Write    = harness.Write
	SayWrite = harness.SayWrite
)

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

const exampleName = "deterministic-refactoring-mode"

// installExampleTree copies the shipped `examples/<name>/.sloprail` tree into the
// project VERBATIM, mode included — every shipped example script now carries the
// execute bit in git, so preserving fi.Mode().Perm() lands a runnable hook. See
// the 036 package's copy for the full rationale.
func installExampleTree(t *testing.T, projDir, name string) {
	t.Helper()
	src := filepath.Join(repoRoot(t), "examples", name, ".sloprail")
	dst := filepath.Join(projDir, ".sloprail")
	copyExampleTree(t, src, dst)
}

func copyExampleTree(t *testing.T, src, dst string) {
	t.Helper()
	ents, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("install example: read %s: %v", src, err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatalf("install example: mkdir %s: %v", dst, err)
	}
	for _, ent := range ents {
		s := filepath.Join(src, ent.Name())
		d := filepath.Join(dst, ent.Name())
		if ent.IsDir() {
			copyExampleTree(t, s, d)
			continue
		}
		body, err := os.ReadFile(s)
		if err != nil {
			t.Fatalf("install example: read %s: %v", s, err)
		}
		info, err := ent.Info()
		if err != nil {
			t.Fatalf("install example: stat %s: %v", s, err)
		}
		if err := os.WriteFile(d, body, info.Mode().Perm()); err != nil {
			t.Fatalf("install example: write %s: %v", d, err)
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
