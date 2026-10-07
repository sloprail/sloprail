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

// The agent is a10n-claude-mock with this repo's plugin enabled, so what fires
// during a test is the wiring a user would get. These tests install the SHIPPED
// required-context-precondition example verbatim — BOTH gates — and prove each
// one refuses a write under its guarded prefix until the skill it names was
// loaded this session, admits it once the skill is loaded, leaves writes outside
// both prefixes alone, and does not accept the OTHER gate's skill.
//
// This supersedes the old 013 test, which reached for examples/guardrails/<name>
// (a path that does not exist) and was red for that reason. The shipped example
// lives at examples/<name>/.sloprail; that is what these tests install.
var (
	New   = harness.New
	Turns = harness.Turns
	Write = harness.Write
	Skill = harness.Skill
)

// TestMain removes the binary build dir when this package's tests finish.
func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

// exampleName is the folder the example ships under. Its whole .sloprail tree is
// installed into the project verbatim.
const exampleName = "required-context-precondition"

// installExampleTree copies the shipped example's entire .sloprail/ tree into a
// project, verbatim, preserving directory structure and any execute bits.
//
// Read off disk rather than restated as a const here: a test carrying its own
// copy of the gate.yaml would prove the copy works and say nothing about the file
// a user lifts, and the two would drift the first time either was edited alone.
//
// RECURSIVE (WalkDir): the example's .sloprail holds nested gate subtrees
// (gate/require-skill-topics/, gate/require-skill-decisions/), and a flat copy
// would drop them. It copies examples/<name>/.sloprail — NOT examples/guardrails/<name>,
// which does not exist and is why 013 was red.
func installExampleTree(t *testing.T, proj string) {
	t.Helper()

	src := filepath.Join(repoRoot(t), "examples", exampleName, ".sloprail")
	dst := filepath.Join(proj, ".sloprail")

	info, err := os.Stat(src)
	if err != nil || !info.IsDir() {
		t.Fatalf("install example tree: %s is not a directory (%v) — the shipped example is not where the test expects it", src, err)
	}

	copied := 0
	err = filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
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
		// The mode is carried over, not fixed at 0644 — a script arriving without
		// its execute bit would be refused as unrunnable, and the test would then
		// pass its refusal assertions for the wrong reason. (These gates ship no
		// scripts today; the copy preserves the bit regardless, so it stays correct
		// if the example gains one.)
		if err := os.WriteFile(target, body, fi.Mode().Perm()); err != nil {
			return err
		}
		copied++
		return nil
	})
	if err != nil {
		t.Fatalf("install example tree: %v", err)
	}
	if copied == 0 {
		t.Fatalf("install example tree: %s held no files", src)
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

// denyReason returns the reason of the first PreToolUse refusal in a run's
// stream, or "" if the run was not refused by a PreToolUse hook.
//
// Needed to assert on the REFUSAL's own words rather than on the whole stream:
// the stream also carries the agent's own tool_use turns, so a session that
// loaded document-topic and was then refused for want of document-strategy has
// "document-topic" in its stream from the agent's Skill call — and a whole-stream
// Saw check could not tell that from the refusal naming it. The refusal is the
// real tool_result "PreToolUse:<Tool> hook error: <reason>", read by the
// harness's Refusals.
func denyReason(output string) string {
	reasons := harness.Result{Output: output}.Refusals()
	if len(reasons) == 0 {
		return ""
	}
	return reasons[0]
}

// containsStr is a tiny substring helper kept local so the test file needs no
// import beyond testing.
func containsStr(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}
