package e2e

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// These tests drive the SHIPPED content-de-layering example — a file-guard over
// files under updates/ or branding/ whose one check is a JUDGE (no prepare, no
// script tier): the model rules on whether the file restates a fact that already
// lives in another file's home. What fires is this repo's plugin against the
// example's own .sloprail tree, copied in verbatim.
//
// The judge's model verdict is a fixed stub (InstallJudgeClaude) — pass:false
// blocks the turn at Stop, pass:true admits — the same substitution the file-guard
// judge template tests T034_09/10 make. What is NOT stubbed is the file event: the
// guard renders the SETTLED file's own content (event.newContent) and path
// (event.path) into the template, so varying the file the agent writes varies the
// prompt the judge is asked. Because this example has no prepare, the wiring that
// must be proven real is event.newContent/event.path -> template; the capturing
// shim proves it directly (JudgePrompt).
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

const exampleName = "content-de-layering"

// installExampleTree copies examples/<exampleName>/.sloprail into the project,
// verbatim, preserving each file's mode. The whole tree read off disk, not a
// restated const, so the test lifts the file a user lifts; the mode is carried so
// a check/template that arrives without its execute bit is not refused for the
// wrong reason. See 042's copy for the same rationale.
func installExampleTree(t *testing.T, projDir string) {
	t.Helper()
	src := filepath.Join(repoRoot(t), "examples", exampleName, ".sloprail")
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
	commitInstalledTree(t, projDir)
}

// commitInstalledTree stages and commits everything in proj so a freshly
// installed guardrail tree is part of the session baseline rather than the first
// cycle's diff. A no-op when proj is not a git repository.
func commitInstalledTree(t *testing.T, proj string) {
	t.Helper()
	if err := exec.Command("git", "-C", proj, "rev-parse", "--is-inside-work-tree").Run(); err != nil {
		return
	}
	if out, err := exec.Command("git", "-C", proj, "add", "-A").CombinedOutput(); err != nil {
		t.Fatalf("commitInstalledTree: git add: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", proj, "commit", "--allow-empty", "-m", "install example tree").CombinedOutput(); err != nil {
		t.Fatalf("commitInstalledTree: git commit: %v\n%s", err, out)
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

func containsStr(haystack, needle string) bool { return strings.Contains(haystack, needle) }
