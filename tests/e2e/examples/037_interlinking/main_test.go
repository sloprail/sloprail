package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// This package is the end-to-end for the interlinking USE CASE (strategy unit
// 06): every person file must be linked from an update/decision, and a deleted
// person must leave no dangling links. A context (`people-linked`) logs each
// touched people/*.md into a per-cycle registry; a gate (`verify-linked`, Stop,
// `require: context: people-linked`) is meant to check each logged path — a
// created person must be found under updates/decisions, a deleted one must not.
//
// # The blocking example bug: the gate cannot read the context's registry
//
// The gate's check runs `sr-session state list --owner people-linked`, but
// `state list` has NO `--owner` flag — the state store is scoped to the CALLING
// guardrail (SR_GUARDRAIL, set by the engine per hook) by deliberate design, and
// there is no flag to name another rule's scope. Cobra rejects `--owner` (exit 1),
// the script swallows it (2>/dev/null), reads an EMPTY registry, and passes. So
// the enforcement is a silent no-op: neither the "created-but-unlinked" nor the
// "deleted-but-still-referenced" violation is caught.
//
// This is an EXAMPLE bug, not an engine bug: the sanctioned way for a gate to read
// a context's data is the context's PAYLOAD, which the gate's check payload carries
// as `.context["people-linked"].payload` — exactly what the known-good
// eval-loop-maxing example does (`.context["goal-tracking"].payload.goal`). The
// interlinking gate would need to carry each touched path in the context payload
// and read it there, not reach for cross-guardrail state via a flag the engine
// does not provide. (There is a second latent bug behind it: the gate's own greps
// `grep -r ... updates/ decisions/` are cwd-relative, and a check runs with cwd =
// the guardrail folder, so even a readable registry would look under the wrong
// tree; eval-loop-maxing anchors such paths on $SR_WORKSPACE.)
//
// So these tests prove what IS real — the context activates on a people/*.md touch
// (create AND delete), logs each touched path to its OWN registry (distinctly for
// two people in one turn), narrows to people/*.md, and the gate's require wiring
// runs it — and PIN the no-op: a clear created-but-unlinked violation is NOT
// refused, with the `--owner` cause named. The registry a test reads via
// GuardrailState is the SAME per-guardrail state the gate fails to reach.
var (
	New   = harness.New
	Turns = harness.Turns
	Write = harness.Write
	Bash  = harness.Bash
)

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

const exampleName = "interlinking"

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
