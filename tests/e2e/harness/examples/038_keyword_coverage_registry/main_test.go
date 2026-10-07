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
// `require: context: scanner-declared`) reads that registry and, for each declared
// scanner, confirms one gh call covered all its keywords.
//
// The composite now enforces end to end against the fixed example. The gate reads
// the declared-scanner registry with `sr-session state list --owner
// scanner-declared` (the read-only cross-guardrail read the engine gained),
// slurping the JSON-lines with `jq -s`; and it collects the run's gh invocations
// off `sr-session trajectory normalize`, reading them from each event's
// flat `.invocations` — the same shape a live check reads under `.event`.
//
// So these tests prove: the context activates on a scanner.yaml write, logs the
// keyword set to its registry, ignores an inactive scanner, and narrows to
// scanners/<name>/scanner.yaml [test_038_01]; a declared-but-unsearched scanner is
// REFUSED with the gate's own words, keywords covered in ONE gh call ADMIT,
// keywords split across two calls are REFUSED (the "all keywords in 1 call" rule),
// and a no-scanner turn is skipped by the gate's match [test_038_02]; a scanner's
// keywords hold [test_038_03]; GitHub research outside gh (WebSearch, WebFetch or
// curl of GitHub) and a gh search before any scanner is declared are refused,
// however the command line wraps it [test_038_04]; and deleting a declared
// scanner is refused, while one deleted unseen stays owed its search
// [test_038_05].
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

// NewUncited is New with the commit-time sloprail/gate/cite-before-commit switched off, for a scenario about
// what Stop or `sr-checks run` does with a commit that carries no (or no resolving) citation: with
// the gate on, the agent could not make that commit at all. The gate is exercised in
// tests/e2e/harness/gate/058_cite_before_commit.
func NewUncited(t *testing.T) *harness.Env {
	return harness.New(t, harness.WithoutShipped("sloprail/gate/cite-before-commit"))
}
