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
// eval-loop-maxing example verbatim and prove it drives the loop end to end: a
// goal-tracking CONTEXT that tracks the active goal, and a goal-verify GATE that
// refuses the Stop until the goal's own verify.sh passes.
var (
	New   = harness.New
	Turns = harness.Turns
	Write = harness.Write
	Bash  = harness.Bash
)

// TestMain removes the binary build dir when this package's tests finish.
// Without it every e2e package leaks the build for the life of the machine.
func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

// exampleName is the folder the example ships under. Its whole .sloprail tree is
// installed into the project verbatim — the guardrail a user lifts, unedited.
const exampleName = "eval-loop-maxing"

// installExampleTree copies the shipped example's entire .sloprail/ tree into a
// project, verbatim, preserving directory structure and the execute bit on every
// script.
//
// Read off disk rather than restated as a const in this file. A test holding its
// own copy of the declaration proves that copy works and says nothing about the
// files a user would lift — which is the only thing an example-level test is for,
// and the two would drift the first time either was edited alone.
//
// The copy is RECURSIVE (WalkDir), not the flat single-directory copy the old 013
// test used: this example's .sloprail holds nested nature subtrees
// (context/goal-tracking/, gate/goal-verify/, …), and a flat copy would silently
// drop them. It copies examples/<name>/.sloprail — NOT the examples/guardrails/<name>
// path 013 reached for, which does not exist and is exactly why 013 was red.
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
		// The mode is carried over, not fixed at 0644. A hook script that arrives
		// without its execute bit is refused by the engine for being unrunnable,
		// and the test would then pass its refusal assertions for entirely the
		// wrong reason.
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

	// Commit the installed tree so it is in history BEFORE any cycle runs. The
	// sloprail plugin ships authoring-slop, a preventive file-guard whose Stop
	// after-check judges a guardrail's own `.sh`/`.md.j2` machinery. The example's
	// own `.sloprail/**` scripts would otherwise read as files THIS cycle created
	// (the baseline is the GitInit commit, taken before this install), so that
	// after-check would judge them — and with no model in the e2e, fail closed,
	// adding spurious blocking errors. In production the example is installed
	// before the session, so it is part of the baseline and never in the cycle
	// diff; committing here reproduces that. No-op when the project is not a git
	// repo (some tests install before GitInit, whose own commit then covers it).
	commitInstalledTree(t, proj)
}

// commitInstalledTree stages and commits everything in proj, so a freshly
// installed guardrail tree is part of the session baseline rather than the first
// cycle's diff. A no-op when proj is not a git repository.
func commitInstalledTree(t *testing.T, proj string) {
	t.Helper()
	if err := exec.Command("git", "-C", proj, "rev-parse", "--is-inside-work-tree").Run(); err != nil {
		return // not a repo yet; GitInit's own commit will baseline the tree
	}
	if out, err := exec.Command("git", "-C", proj, "add", "-A").CombinedOutput(); err != nil {
		t.Fatalf("commitInstalledTree: git add: %v\n%s", err, out)
	}
	// --allow-empty so a re-install that changed nothing still succeeds.
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

// containsAny reports whether needle appears in any of the strings.
func containsAny(lines []string, needle string) bool {
	for _, l := range lines {
		if strings.Contains(l, needle) {
			return true
		}
	}
	return false
}

// rawBlockRecords counts the UN-deduplicated hook_blocking_error records in a
// session's transcript whose text contains needle.
//
// BlockingErrorsFrom de-dupes by text and accumulates across cycles, so a gate
// that refuses with the SAME words on two different Stops reads as one entry
// there — which cannot tell "blocked once" from "blocked again". The record, by
// contrast, gains one entry per blocked Stop attempt, so a strict increase in
// this count across two cycles is proof the gate genuinely RE-FIRED and blocked
// the second Stop, not merely that its earlier verdict is still on file. Read
// straight off the transcript the harness wrote, independent of the code under
// test's own de-dup.
func rawBlockRecords(t *testing.T, e *harness.Env, proj, sess, needle string) int {
	t.Helper()
	b, err := os.ReadFile(e.TranscriptPath(proj, sess))
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("read transcript: %v", err)
	}
	n := 0
	for _, line := range strings.Split(string(b), "\n") {
		if strings.Contains(line, `"hook_blocking_error"`) && strings.Contains(line, needle) {
			n++
		}
	}
	return n
}
