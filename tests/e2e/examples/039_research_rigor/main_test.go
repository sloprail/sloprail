package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// This package is the end-to-end for the research-rigor USE CASE (strategy unit
// 01): a research run declared with a `#research` tag must prove depth — a real
// `git clone`, enough `gh` search pages, and (if a sub-agent) no sibling
// trajectories. A context (`research-run`) activates on the `#research` tag; a
// gate (`depth-check`, Stop, `match: context["research-run"].active`, `require:
// context: research-run`) runs the depth checks and blocks the Stop on a
// shortfall.
//
// # What is exercisable, and the one blocking example bug
//
// The gate reads the context via `context["research-run"].active` (the sanctioned
// channel — no `state list --owner`), so the context→gate wiring works. The
// FIRST depth check — "did a git clone happen" — is however unsatisfiable as
// shipped: it looks for the clone entry's `.toolUseResult`, but that field lives
// on the tool_RESULT record, while the entry carrying the clone's PreCommandInvoke
// event is the assistant tool_use record, which has no toolUseResult. So the clone
// is never detected and check 1 refuses even when a `git clone` really ran — which
// makes the depth gate's PASS path, and every later check (pages, sub-agent),
// unreachable. This is a defect in verify-depth.sh (it reads the field off the
// wrong entry), not a mock limitation: the same jq reads null in a real session.
//
// So these tests exercise: the context activates on #research (and NOT on other
// tags); the depth gate refuses a shallow run with its own words (check 1); the
// gate does NOT run outside a research turn; and — pinned as a bug — even a run
// WITH a real clone + gh is refused because check 1 cannot see the clone.
var (
	New     = harness.New
	Turns   = harness.Turns
	Bash    = harness.Bash
	Say     = harness.Say
	SayBash = harness.SayBash
)

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

const exampleName = "research-rigor"

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
		perm := info.Mode().Perm()
		if strings.HasSuffix(ent.Name(), ".sh") {
			perm = 0o755
		}
		if err := os.WriteFile(d, body, perm); err != nil {
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
