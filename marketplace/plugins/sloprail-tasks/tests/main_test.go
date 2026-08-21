// Package e2e is the sloprail-tasks plugin's OWN end-to-end suite — the first
// instance of the "each use-case plugin self-tests" model. It drives the
// a10n-claude-mock through the SHARED harness (the same one tests/e2e/session/*
// and tests/e2e/examples/* use), installing THIS plugin's four guardrails into a
// project and asserting they refuse and permit the right writes and turn-ends.
//
// What fires during a test is exactly what a user installing this plugin gets:
// the base `sloprail` plugin's hooks (enabled by the harness's Project()) run the
// nature dispatch, which loads this plugin's .sloprail tree. A test controls only
// what the mock tries to do; the hook firing, the engine deciding, and the refusal
// travelling back all run as production would. See README.md for the model.
//
// The four guardrails and what each test covers:
//
//   - task-body-is-human-authored (file-guard, preventive, script+judge): a body
//     citing the human's words with a grounded [quote](jsonl) link PASSES; a slop
//     body the judge rejects is REFUSED; a body with no citation is refused by the
//     deterministic script before the judge.
//   - task-evidence-resolves (file-guard, preventive, script): a task with grounded
//     evidence PASSES; one whose [quote](jsonl) evidence does not resolve is
//     REFUSED; an invalid-frontmatter task (status: done) is refused by the schema.
//   - task-review (file-guard, after-check, script+judge): an in_review task's
//     review runs at the Post/Stop after-check; a not-substantiated one is BLOCKED
//     with the judge's rejection reaching the agent.
//   - no-unfinished-work-at-turn-end (gate, Stop, script): a turn ending with an
//     open (to_do/in_progress) task is BLOCKED at Stop; a turn with all tasks at a
//     resting status PERMITS.
//
// The judge verdict is a fixed stub (InstallJudgeClaude) exactly as the main
// suite's judge e2e do — the model call is the one thing a mock cannot supply for
// sr-agent's judge path (the mock refuses sr-agent's --model/--settings flags). The
// DETERMINISTIC halves — the citation script, cite's own grounding, the pre-flight,
// the gate's tree scan — are NOT stubbed and run for real against the transcript
// the mock streamed.
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

// The harness API, re-exported under short names so the tests read like the main
// suite's do.
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

// authPrompt is the human message every session starts from. The harness seeds it
// as the transcript's root user message at line 1, so a citation whose quote is a
// substring of this — grounded via `sr-session trajectory cite` — resolves to a
// real user line. Distinctive so a test can tell it from a fabricated quote.
const authPrompt = "Please migrate the auth module to the new token format."

// taskPath is a TASK.md at the two-level path the guardrails match:
// memories/tasks/<category>/<name>/TASK.md.
const taskPath = "memories/tasks/auth/migrate-tokens/TASK.md"

// installPluginTree copies the plugin's OWN .sloprail tree (file-guard/, gate/,
// schemas/) into the project, verbatim, preserving each file's mode — the scripts
// MUST keep their execute bit or the engine refuses them as unrunnable. This is
// what a consumer does when they install the plugin's guardrails into their own
// project; here it is done from the plugin's committed tree so the e2e drive the
// real shipped machinery, not a copy.
//
// The tree is then committed (commitInstalledTree) so it is part of the session
// BASELINE rather than the first cycle's diff. The base sloprail plugin ships
// authoring-slop, a preventive file-guard whose Stop after-check judges a
// guardrail's own .sh/.md.j2 machinery; an uncommitted guard tree reads as this
// cycle's writes, so that after-check would judge these scripts and, with no model
// in the e2e, fail closed. Production installs before the session (baseline), so it
// is never in the cycle diff — this reproduces that.
func installPluginTree(t *testing.T, projDir string) {
	t.Helper()
	src := filepath.Join(pluginRoot(t), ".sloprail")
	dst := filepath.Join(projDir, ".sloprail")
	info, err := os.Stat(src)
	if err != nil || !info.IsDir() {
		t.Fatalf("install plugin tree: %s is not a directory (%v)", src, err)
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
		t.Fatalf("install plugin tree: %v", err)
	}
	if copied == 0 {
		t.Fatalf("install plugin tree: %s held no files", src)
	}
	commitInstalledTree(t, projDir)
}

// commitInstalledTree stages and commits everything in proj so a freshly installed
// guardrail tree is part of the session baseline rather than the first cycle's
// diff. A no-op when proj is not a git repository.
func commitInstalledTree(t *testing.T, proj string) {
	t.Helper()
	if err := exec.Command("git", "-C", proj, "rev-parse", "--is-inside-work-tree").Run(); err != nil {
		return
	}
	if out, err := exec.Command("git", "-C", proj, "add", "-A").CombinedOutput(); err != nil {
		t.Fatalf("commitInstalledTree: git add: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", proj, "commit", "--allow-empty", "-m", "install sloprail-tasks guardrails").CombinedOutput(); err != nil {
		t.Fatalf("commitInstalledTree: git commit: %v\n%s", err, out)
	}
}

// pluginRoot is this plugin's install root — the directory holding .claude-plugin
// and .sloprail. The tests/ module sits one level below it, so the root is the
// parent of the tests/ directory the test binary runs from. Verified by asserting
// .sloprail is there, so a wrong path fails loudly rather than copying nothing.
func pluginRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// go test runs with the package dir as cwd (…/sloprail-tasks/tests), so the
	// plugin root is its parent.
	root := filepath.Dir(dir)
	if _, err := os.Stat(filepath.Join(root, ".sloprail")); err != nil {
		t.Fatalf("pluginRoot: no .sloprail under %s (cwd was %s): %v", root, dir, err)
	}
	return root
}

// cite builds a [quote](transcriptPath:line) markdown link — the citation format
// the guardrails ground with `sr-session trajectory cite`. The quote must be a
// substring of a real user message in the transcript for cite to resolve it; the
// tests pass a substring of authPrompt (a grounded citation) or a fabricated
// string (an ungrounded one).
func cite(quote, transcriptPath string, line int) string {
	if line > 0 {
		return "[" + quote + "](" + transcriptPath + ":" + itoa(line) + ")"
	}
	return "[" + quote + "](" + transcriptPath + ")"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// task assembles a TASK.md with the given frontmatter status/priority and body.
func task(status, priority, body string) string {
	return "---\nstatus: " + status + "\npriority: " + priority + "\n---\n\n" + body + "\n"
}

func containsStr(haystack, needle string) bool { return strings.Contains(haystack, needle) }
