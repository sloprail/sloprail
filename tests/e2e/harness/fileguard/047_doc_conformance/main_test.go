package e2e

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// These tests drive the SHIPPED doc-conformance example — a file-guard matched by
// a MARKER, not a path: any file carrying an `sr:docs` marker (its fqn a remote
// doc URL — mirroring the a10n-cli convention of citing a doc section as
// `a10n:docs <URL>` in a comment) is judged, and the judge (no prepare, no script
// tier) is asked to visit that URL and rule on whether the marked code conforms
// to what the doc currently says. What fires is this repo's plugin against the
// example's own .sloprail tree, copied in verbatim.
//
// The judge's model verdict is a fixed stub (InstallJudgeClaude) — pass:false
// blocks at Stop, pass:true admits. The model is exactly where the real URL-fetch
// and conformance judgement would happen, so in these tests that step is the stub;
// what is NOT stubbed is everything up to it: the marker scan that selects the file
// by its `docs` kind, and the template render that puts the marker's URL and the
// marked file's content into the prompt. The capturing shim proves that wiring
// directly (JudgePrompt) — the URL the agent WOULD visit reaches the prompt.
//
// TODO(D3): drive the verdict via a10n-claude-mock once a10n-cli#470 lands and the
// new mock binary is on PATH; today the proven InstallJudgeClaude stub supplies
// the model verdict.
var (
	New   = harness.New
	Turns = harness.Turns
	Write = harness.Write
)

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

const exampleName = "doc-conformance"

// The fixture under testdata/ is a verbatim copy of sloprail-community examples/doc-conformance (.sloprail renamed sloprail).

// installExampleTree copies examples/<exampleName>/.sloprail into the project,
// verbatim, preserving each file's mode. See 042/043 for the rationale (lift the
// real file, keep the exec bit).
func installExampleTree(t *testing.T, projDir string) {
	t.Helper()
	src := filepath.Join("testdata", exampleName, "sloprail")
	dst := filepath.Join(projDir, ".sloprail")
	info, err := os.Stat(src)
	if err != nil || !info.IsDir() {
		t.Fatalf("install example tree: %s is not a directory (%v)", src, err)
	}
	copied := 0
	err = filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		copied++
		return os.WriteFile(target, body, fi.Mode().Perm())
	})
	if err != nil {
		t.Fatalf("install example tree: %v", err)
	}
	if copied == 0 {
		t.Fatalf("install example tree: %s held no files", src)
	}

	// Commit the installed tree so it is part of the session baseline, not the
	// first cycle's diff. The sloprail plugin ships authoring-slop, a gate plus
	// a file-guard whose Stop after-check judges a guardrail's own `.sh`/`.md.j2`
	// machinery; an uncommitted example tree reads as this cycle's writes, so that
	// after-check would judge the example's own scripts and, with no model in the
	// e2e, fail closed. Production installs before the session (baseline), so it is
	// never in the cycle diff — this reproduces that. No-op when proj is not a repo.
	harness.CommitInstalled(t, projDir)
}

func containsStr(haystack, needle string) bool { return strings.Contains(haystack, needle) }
