package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// This package is the end-to-end for the completeness-artifact-on-trigger USE
// CASE (strategy unit 09): when a turn declares an #update/#decision it MUST
// produce the artifact that tag demands; a turn that declares #skip needs none; a
// turn that declares no tag at all is refused. Three natures cooperate:
//
//   - context `tag-declared` — activates on #update/#decision/#skip OR on a write
//     under memories/updates|decisions/, accumulating tag: and artifact: entries.
//   - gate `tag-required` (Stop, `match: not context["tag-declared"].active`) —
//     refuses when NO tag was declared. NO script-registry read; its match IS the
//     "no tag" fact. This gate WORKS (no --owner).
//   - gate `verify-artifact-produced` (Stop, `match: context["tag-declared"].active`,
//     require context) — refuses a tag with no artifact. Its check reads the
//     registry with `sr-session state list --owner tag-declared` (the read-only
//     cross-guardrail read the engine gained), slurping the JSON-lines with `jq
//     -s`; #skip early-exits (needs no artifact).
//   - file-guard `structure.yaml` — a path allowlist (an update is memories/
//     updates/*.md; a decision is memories/decisions/<YYYYMMDD_slug>/*.md).
//
// The composite now enforces end to end against the fixed example. These tests
// prove: a no-tag turn is REFUSED (tag-required, whose match reads the context's
// absence — no registry needed); the context accumulates tag: and artifact:
// entries; the structure allowlist admits in-list / refuses out-of-list writes; a
// #update+artifact turn and a #skip turn ADMIT; and a #update with NO artifact is
// REFUSED with the gate's own words.
var (
	Turns = harness.Turns
	Write = harness.Write
	Say   = harness.Say
	// SayWrite lets a tag be declared atomically with a file write, needed because
	// a pure-text tag turn is terminal in the mock (see harness.SayWrite).
	SayWrite = harness.SayWrite
)

// New stands the environment up without the sloprail plugin's authoring file-guards:
// this package is about another rule, and the commit that adds the example's rule puts
// its own .sh/.md.j2 files in the range, which authoring-slop would judge (and, with no
// model in the e2e, leave unjudged). WithoutShippedFileGuards is the sanctioned switch.
func New(t *testing.T) *harness.Env { return harness.New(t, harness.WithoutShippedFileGuards()) }

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

const exampleName = "completeness-artifact-on-trigger"

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
