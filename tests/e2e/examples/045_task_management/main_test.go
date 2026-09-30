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

// These tests drive the SHIPPED task-management example — a PreFileWrite gate
// (ask-is-human-authored) over `**/tasks/*/*/ASK.md`, with a plain file-guard of
// the same name as the Stop after-check, that requires every write to cite the user's own
// words (`require: [{citation: {source_types: [user]}}]`), then asks a judge
// (resolve-cited-messages.sh + reference-is-true-and-only-this.md.j2) whether the
// ask is TRUE to the cited words and holds THAT AND NOTHING ELSE. Being a gate, it
// refuses a not-fine write at PRE-tool, before it lands. What fires is this
// repo's plugin against the example's own .sloprail tree, copied in verbatim.
//
// The judge's model verdict is a fixed stub (InstallJudgeClaude) — pass:false
// blocks the pre-tool write, pass:true admits it. What is NOT stubbed: the engine
// resolving each `sr-file --cite:user` quote against the transcript the mock
// streamed, and the prepare handing the resolved words to the template. The
// capturing shim proves that wiring directly (JudgePrompt).
//
// TODO(D3): drive the verdict via a10n-claude-mock once a10n-cli#470 lands and the
// new mock binary is on PATH; today the proven InstallJudgeClaude stub supplies
// the model verdict.
type Turn = harness.Turn

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

const exampleName = "task-management"

// installExampleTree copies examples/<exampleName>/.sloprail into the project,
// verbatim, preserving each file's mode. The scripts (has-message-reference.sh,
// resolve-referenced-message.sh) MUST keep their execute bit or the engine refuses
// them as unrunnable — which is why the mode is carried, not fixed at 0644.
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
	// file-guard whose Stop after-check judges a guardrail's own `.sh`/`.md.j2`
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

func readProj(proj, rel string) (string, error) {
	b, err := os.ReadFile(filepath.Join(proj, rel))
	return string(b), err
}
