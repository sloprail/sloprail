package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The agent is a10n-claude-mock with this repo's plugin enabled, so what fires
// during a test is the wiring a user would get. These tests drive the SHIPPED
// business-invariants example (examples/business-invariants/.sloprail),
// installed verbatim, so a green run means those files work — a test carrying
// its own copy of the rule would keep passing after the shipped one broke.
type env = harness.Env

// Turn is one step of a scripted session.
type Turn = harness.Turn

var (
	Turns = harness.Turns
	Write = harness.Write
	Bash  = harness.Bash
)

// newEnv is harness.New behind a check that the PINNED mock is installed. The
// harness falls back to any a10n-claude-mock on PATH, and an older one there
// drives the session differently (no CLAUDE_CODE_SESSION_ID, so sr-file cannot
// find the session) — the tests then fail for a reason that is not theirs. With
// A10N_CLAUDE_MOCK unset and no .bin/ stamp for tests/e2e/harness/MOCK_VERSION,
// every test here that drives the mock is skipped with the fix. CI runs
// `make mock` and checks .bin/ itself, so it never takes this skip.
func newEnv(t *testing.T) *env {
	t.Helper()
	if msg := pinnedMockMissing(repoRoot(t), os.Getenv("A10N_CLAUDE_MOCK")); msg != "" {
		t.Skip(msg)
	}
	return harness.New(t)
}

// pinnedMockMissing says why the pinned mock is not installed, or "" when it is
// (or when A10N_CLAUDE_MOCK names a mock build of the caller's own).
func pinnedMockMissing(root, override string) string {
	if override != "" {
		return ""
	}
	version, err := os.ReadFile(filepath.Join(root, "tests", "e2e", "harness", "MOCK_VERSION"))
	if err != nil {
		return "harness: tests/e2e/harness/MOCK_VERSION unreadable: " + err.Error()
	}
	stamp := filepath.Join(root, ".bin", "a10n-claude-mock."+strings.TrimSpace(string(version)))
	if _, err := os.Stat(stamp); err != nil {
		return "the pinned a10n-claude-mock (" + strings.TrimSpace(string(version)) + ") is not in .bin/ — run `make mock`"
	}
	return ""
}

// TestMain removes the binary build dir when this package's tests finish.
// Without it every e2e package leaks 15M for the life of the machine.
func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// installExampleTree copies the WHOLE examples/<name>/.sloprail tree into a
// project VERBATIM, preserving each file's mode bits (a hook script ships
// executable, so it must arrive executable — the engine fail-closed-refuses a
// non-executable check) and recreating subdirectories.
//
// Read off disk rather than restated as consts. A test holding its own copy of
// the rule proves that copy works and says nothing about the file a user would
// lift; the two drift the first time either is edited alone. This is the
// examples-are-truth discipline the e2e suite rests on.
func installExampleTree(t *testing.T, projDir, name string) {
	t.Helper()
	src := filepath.Join(repoRoot(t), "examples", name, ".sloprail")
	dst := filepath.Join(projDir, ".sloprail")
	info, err := os.Stat(src)
	if err != nil || !info.IsDir() {
		t.Fatalf("install example tree: %s is not a directory (%v)", src, err)
	}
	walkErr := filepath.Walk(src, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if fi.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		// The mode is carried over, not fixed at 0644. A hook script that arrives
		// without its execute bit is refused by the engine for being unrunnable —
		// which the verbatim-install tests below deliberately observe.
		return os.WriteFile(target, body, fi.Mode().Perm())
	})
	if walkErr != nil {
		t.Fatalf("install example tree %s: %v", name, walkErr)
	}
}

// joinBlocks renders a slice of blocking-error texts for a log/assert message.
func joinBlocks(bs []string) string { return strings.Join(bs, "\n---\n") }

// containsAll reports whether every needle appears in haystack.
func containsAll(haystack string, needles ...string) bool {
	for _, n := range needles {
		if !strings.Contains(haystack, n) {
			return false
		}
	}
	return true
}
