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
// The depth gate now enforces end to end against the fixed verify-depth.sh:
//   - check 1 detects the clone from the `git clone` INVOCATION (re-derived as a
//     PreCommandInvoke event, read from the entry's flat `.invocations`), rather
//     than from an unreachable `.toolUseResult` — the tool_RESULT record carries
//     no uuid and is not re-emitted as a normalized entry, so the invocation is
//     the only re-derivable clone signal (and the honest one: a README fetch is a
//     different command).
//   - check 2 reads the gh invocations the same `.invocations` way, and
//     counts pages from `--paginate` (unbounded) or `--limit N` (reading N from
//     the flag value, or from argv when the value was space-separated).
//
// So these tests exercise: the context activates on #research (and NOT on other
// tags); a shallow run (no clone) is refused (check 1); a deep run (clone +
// paginated gh) ADMITS and the gate records a pass; a clone with no gh call is
// refused (check 2); and a clone + gh covering too few pages is refused with the
// page shortfall (page count). The gate reads the context via
// `context["research-run"].active` — no cross-guardrail state read here.
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
