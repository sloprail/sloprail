// Package e2e is the sloprail-content plugin's OWN end-to-end suite, mirroring
// sloprail-tasks/tests's model: it drives the a10n-claude-mock through the
// SHARED harness, installing THIS plugin's real .sloprail tree into a project
// and asserting its guardrails refuse and permit the right writes.
//
// What fires during a test is exactly what a user installing this plugin
// gets: the base `sloprail` plugin's hooks (enabled by the harness's
// Project()) run the nature dispatch, which loads this plugin's .sloprail
// tree. A test controls only what the mock tries to do; the hook firing, the
// engine deciding, and the refusal travelling back all run as production
// would.
//
// The four guardrails and what each test file covers:
//
//   - unit-satisfies-rules (file-guard, Stop after-check, script+judge): a
//     global judge rule applies to a unit with no channels; a channel rule
//     applies only when the channel matches; an over-limit X thread is
//     refused by the script rule and an in-limit one passes; a banned phrase
//     is refused.
//   - unit-publish-approved (file-guard, preventive, script): status:
//     published without approved: is refused; an approved: citation of the
//     agent's own output is refused; a valid approval plus published_url
//     passes.
//   - content-rule-is-grounded (file-guard, preventive, script): a rule with
//     no grounded transcript_paths is refused; a script rule with no real
//     refusal path is refused.
//
// The judge verdict is a fixed stub (InstallJudgeClaude) exactly as the main
// suite's judge e2e do — the model call is the one thing a mock cannot supply
// for sr-agent's judge path (the mock refuses sr-agent's --model/--settings
// flags). The DETERMINISTIC halves — the script rules, the citation
// grounding, the schema validation — are NOT stubbed and run for real.
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

// Env is the harness environment, aliased so helpers read like the main
// suite's.
type Env = harness.Env

var (
	New            = harness.New
	Turns          = harness.Turns
	Write          = harness.Write
	Bash           = harness.Bash
	ToolResult     = harness.ToolResult
	AnswerQuestion = harness.AnswerQuestion
)

func TestMain(m *testing.M) {
	code := m.Run()
	harness.Cleanup()
	os.Exit(code)
}

// authPrompt is the human message every session starts from. The harness
// seeds it as the transcript's root user message at line 1, so a citation
// whose quote is a substring of this — grounded via `sr-session trajectory
// cite` — resolves to a real user line.
const authPrompt = "Approve writing rules and publishing for the launch topic content."

// unitPath is a UNIT.md at the path unit-satisfies-rules and
// unit-publish-approved match: memories/topics/<topic>/units/<NN_unit>/UNIT.md.
const unitPath = "memories/topics/20260101_launch/units/01_announce/UNIT.md"

// draftPath is the unit's actual copy — also matched by unit-satisfies-rules
// (but NOT by unit-publish-approved, which is UNIT.md-only; see its
// file-guard.yaml).
const draftPath = "memories/topics/20260101_launch/units/01_announce/02_draft.md"

// installPluginTree copies the plugin's OWN .sloprail tree (file-guard/,
// schemas/) into the project, verbatim, preserving each file's mode — the
// scripts MUST keep their execute bit or the engine refuses them as
// unrunnable. Mirrors sloprail-tasks/tests's installPluginTree exactly. The
// tree is then committed so it is part of the session BASELINE rather than
// the first cycle's diff (the base plugin's authoring-slop after-check would
// otherwise judge these scripts with no model and fail closed).
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

// commitInstalledTree stages and commits everything in proj so a freshly
// installed guardrail tree is part of the session baseline rather than the
// first cycle's diff. A no-op when proj is not a git repository.
func commitInstalledTree(t *testing.T, proj string) {
	t.Helper()
	if err := exec.Command("git", "-C", proj, "rev-parse", "--is-inside-work-tree").Run(); err != nil {
		return
	}
	if out, err := exec.Command("git", "-C", proj, "add", "-A").CombinedOutput(); err != nil {
		t.Fatalf("commitInstalledTree: git add: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", proj, "commit", "--allow-empty", "-m", "install sloprail-content guardrails").CombinedOutput(); err != nil {
		t.Fatalf("commitInstalledTree: git commit: %v\n%s", err, out)
	}
}

// pluginRoot is this plugin's install root — the directory holding
// .claude-plugin and .sloprail. The tests/ module sits one level below it.
func pluginRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(dir)
	if _, err := os.Stat(filepath.Join(root, ".sloprail")); err != nil {
		t.Fatalf("pluginRoot: no .sloprail under %s (cwd was %s): %v", root, dir, err)
	}
	return root
}

// cite builds a [quote](transcriptPath:line) markdown link — the citation
// format unit-publish-approved's approved: and (indirectly) a task body both
// ground with `sr-session trajectory cite`.
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

// unitFrontmatter assembles a UNIT.md/02_draft.md with the given frontmatter
// fields and body. fields is rendered as raw YAML lines (already formatted by
// the caller), so a test can build exactly the shape it needs — a plain unit,
// one with channels/tags, or one carrying approved:/published_url:.
func unitFrontmatter(fields, body string) string {
	return "---\n" + fields + "---\n\n" + body + "\n"
}

// toolResultLine returns the 1-based physical line of the FIRST tool_result
// record in the transcript at path whose content contains marker, or 0 when
// none does.
func toolResultLine(t *testing.T, path, marker string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read transcript %s: %v", path, err)
	}
	for i, line := range strings.Split(string(b), "\n") {
		if strings.Contains(line, `"type":"tool_result"`) && strings.Contains(line, marker) {
			return i + 1
		}
	}
	return 0
}

func transcriptText(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return "(could not read " + path + ": " + err.Error() + ")"
	}
	return string(b)
}

func containsStr(haystack, needle string) bool { return strings.Contains(haystack, needle) }
