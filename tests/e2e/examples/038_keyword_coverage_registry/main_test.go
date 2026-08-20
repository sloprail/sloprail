package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// This package is the end-to-end for the keyword-coverage-registry USE CASE
// (strategy unit 10): each declared, ACTIVE scanner's keyword set must ALL appear
// together in one `gh` call somewhere in the run's trajectory. A context
// (`scanner-declared`) activates when a scanners/<name>/scanner.yaml is written
// and logs the scanner's keyword set into a registry; a gate
// (`verify-scanner-coverage`, Stop, `match: context["scanner-declared"].active`,
// `require: context: scanner-declared`) is meant to read that registry and, for
// each declared scanner, confirm one gh call covered all its keywords.
//
// # The blocking example bug: the gate cannot read the context's registry
//
// The gate's check runs `sr-session state list --owner scanner-declared`, and
// `--owner` is not a real flag (state is scoped to the calling guardrail; the
// engine provides no cross-guardrail read). Cobra rejects it, the script swallows
// the error, `declared` comes back empty, `declared_count` is 0, and the gate's
// own backstop `exit 0` passes. So no coverage shortfall is ever caught — a silent
// no-op, exactly as for interlinking. The sanctioned channel is the context
// PAYLOAD (`.context["scanner-declared"].payload`), which the gate's check payload
// carries — the pattern eval-loop-maxing uses. (The gate also collects gh calls
// via `sr-session trajectory normalize --whole-session`, which DOES work — the
// coverage machinery is real; only the "which scanners were declared" read is
// broken.)
//
// So these tests prove the REAL half — the context activates on a scanner.yaml
// write, logs the keyword set to its own registry (readable via GuardrailState),
// activates only for `active: true`, and narrows to scanners/<name>/scanner.yaml —
// and PIN the no-op: a declared scanner whose keywords were never searched is NOT
// refused, with the `--owner` cause named. Unlike interlinking, this gate has a
// `match: context[...].active`, so it does NOT block unrelated turns — a second
// control proves that.
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

const exampleName = "keyword-coverage-registry"

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
