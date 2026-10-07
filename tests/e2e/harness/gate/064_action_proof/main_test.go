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

// These tests drive the SHIPPED action-proof example — a gate that wakes on Stop
// and, if the turn took an auditable action (a form fill, an invoice download),
// demands proof of it through a prepare + judge check. What fires is this repo's
// plugin against the example's own .sloprail tree, copied in verbatim; nothing
// here restates the gate.yaml, the prepare, or the template.
//
// The judge's model verdict is a fixed stub (InstallJudgeClaude), the same
// substitution the template tests T032_08/09 make — pass:false blocks, pass:true
// admits. What is NOT stubbed is the prepare: a real `sr-session trajectory
// normalize` reads the trajectory the mock streamed, so varying the ACTION the
// agent took varies the additionalContext the template renders. The capturing
// shim proves that wiring directly (JudgePrompt).
//
// TODO(D3): drive the verdict via a10n-claude-mock once a10n-cli#470 lands and the
// new mock binary is on PATH; today the proven InstallJudgeClaude stub supplies
// the model verdict.
var (
	New   = harness.New
	Turns = harness.Turns
)

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

// exampleName is the folder the example ships under, which is where the gate's
// name and its relative script paths resolve.
const exampleName = "action-proof"

// The fixture under testdata/ is a verbatim copy of sloprail-community examples/action-proof.
// installExampleTree copies testdata/<exampleName>/sloprail into the project,
// verbatim, preserving each file's mode.
//
// The WHOLE tree, read off disk, not a restated const: a test carrying its own
// copy of the gate.yaml/prepare/template would prove that copy works and say
// nothing about the files a user lifts, and the two would drift the moment either
// was edited alone. The example's .sloprail is nested (gate/<name>/…), so the copy
// is recursive; the mode is carried over because a prepare or check that arrives
// without its execute bit is refused by the engine as unrunnable, and a refusal
// asserted on for that reason would be the wrong reason entirely.
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

	// Committed into the baseline every caller already expects (installExampleTree
	// is always called right after GitInit): the sloprail plugin ships gates and
	// file-guards that judge a guardrail's OWN files (authoring-slop's script/template
	// check, and the shipped read-*-doc rules requiring the skill be read before
	// writing one), and an uncommitted copy of the example's gate/prepare/template
	// reads as THIS cycle's write to those rules, not only to the gate under test —
	// the same reason 037_fileguard_pure_require's own commitGuards commits.
	if out, err := exec.Command("git", "-C", projDir, "add", ".sloprail").CombinedOutput(); err != nil {
		t.Fatalf("install example tree: git add .sloprail: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", projDir, "commit", "-m", "baseline .sloprail").CombinedOutput(); err != nil {
		t.Fatalf("install example tree: git commit: %v\n%s", err, out)
	}
}

// containsStr reports whether needle is in haystack — a tiny local helper so a
// test asserts on refusal text without importing strings everywhere.
func containsStr(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}
