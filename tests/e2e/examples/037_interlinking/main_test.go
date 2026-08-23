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
// `match: context["people-linked"].active`, `require: context: people-linked`)
// reads that registry and checks each logged path — a created person must be
// found under updates/decisions, a deleted one must not.
//
// The composite now enforces end to end against the fixed example. The gate reads
// the context's registry with `sr-session state list --owner people-linked` (the
// read-only cross-guardrail read the engine gained), slurping the JSON-lines with
// `jq -s`; its link greps are anchored on $SR_WORKSPACE (a check's cwd is the
// guardrail folder, not the repo root); and its `match: context[...].active` makes
// it SKIP turns that touched no person rather than blocking them (an unmet
// `require: {context}` refuses, so the match is what scopes the gate — the require
// stays only to order the context's enter before this read).
//
// So these tests prove: the context activates on a people/*.md touch (create AND
// delete) and logs each path to its registry (distinctly for two people in one
// turn) [test_037_01]; a created-but-unlinked person and a deleted-but-referenced
// person are REFUSED with the gate's own words, while a linked person and an
// unreferenced delete ADMIT, and an unrelated turn is not blocked
// [test_037_02]. The registry a test reads via GuardrailState is the same
// per-guardrail state the gate reads via --owner.
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
