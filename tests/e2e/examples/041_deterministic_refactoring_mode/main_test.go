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
// (strategy unit 12): a refactor must be MECHANICAL, not regenerated. Two halves:
//
//   - a PreFileWrite gate (`gate/moved-content-reconciles`, PreToolUse) blocks any
//     write of a file carrying an `sr:moved-from` marker whose body does not
//     reconcile byte-for-byte (minus imports/whitespace) against the origin range
//     the marker pins. Relevant only inside the refactoring context (its match
//     reads `context["refactoring"].active`). The same-named file-guard
//     re-runs the reconcile at Stop on the committed file; it reads no context
//     (file-guards cannot see the session), only the `moved-from` marker. T041_01..05, T041_10.
//   - a COMPLETENESS check: a declared refactor's every promised move must land.
//     This BLOCKS, so it is a Stop GATE (`gate/refactor-complete`), NOT the
//     context's exit — a context's exit is pure lifecycle and cannot refuse a Stop
//     (services/sr-session/nature_context.go). The gate reads the refactoring
//     context's `declared_markers` and refuses at Stop if a declared move never
//     landed. T041_06..08.
//
// The `refactoring` context TRACKS the declaration: it activates on PreToolUse (so
// the gates' `.active` match holds before a write) and on PostTagWrite (which
// fires at Stop, once the turn is settled, so enter can read `#refactor scope=...`
// and populate declared_markers before the gate reads it). The declared scope
// names the ACTUAL move fqns (`<path>@<sha>:<lines>`), so a declared token IS a
// landed `sr:moved-from` marker's fqn — that literal correspondence is what lets
// the gate check a completed move by a plain search of the tree.
//
// This is the one composite in the wave that works end to end against the engine
// as shipped: it reads the context via the gates' `match:
// context["refactoring"].active` and stdin `.context`, and resolves the
// origin via `git show <sha>:<path>` (no cwd-relative grep). Its scripts DO ship
// with the execute bit.
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
	Bash     = harness.Bash
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
