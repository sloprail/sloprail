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
//   - unit-md-first (gate + file-guard, script): a file other than
//     UNIT.md written into a unit folder before UNIT.md exists is refused;
//     UNIT.md itself, and any file written after UNIT.md exists, are
//     admitted; deleting a file inside an existing unit folder is not
//     refused (see test_unit_md_first_test.go).
//   - unit-satisfies-rules (file-guard, Stop after-check, one judge check): a
//     global rule applies to a unit with no tags; a tag-scoped rule applies
//     only when the tag matches; a rule written to ask for a deterministic
//     measurement (character limit) is judged (via a stub — see
//     test_unit_rules_test.go's header for what this can and cannot prove,
//     now that there is no separate script-rule stage).
//   - unit-publish-approved (gate + file-guard, script): a write moving
//     a unit INTO status: published without a citation of the user's words
//     on the action is refused; a quote the user never said is refused; a
//     cited transition plus published_urls (a list) passes; a write that is
//     not a transition into published needs no citation.
//   - content-rule-is-grounded (gate + file-guard, require citation +
//     script + judge): an uncited rule write (the Write tool) is refused by
//     require, naming sr-file; a quote the user never said is refused; a
//     cited rule the judge accepts passes, the judge having been handed the
//     cited quote; one the judge rejects as adding untraceable scope ("and
//     nothing else") is refused.
//
// CITATIONS RIDE ON THE ACTION. A grounded change is made with
// `sr-file write|edit ... --cite:user '<exact quote>'` in a Bash turn (the
// harness's Bash turns really execute), and the quote is words from the
// scenario's own prompt — the harness seeds it as the session's root user
// message, so the session resolves it for real. Nothing in a rule or unit
// carries a transcript link.
//
// The judge verdict is a fixed stub (InstallJudgeClaude) exactly as the main
// suite's judge e2e do — the model call is the one thing a mock cannot supply
// for sr-agent's judge path (the mock refuses sr-agent's --model/--settings
// flags). The DETERMINISTIC halves — citation resolution, the require, the
// scripts, the schema validation — are NOT stubbed and run for real.
package e2e

import (
	"io/fs"
	"os"
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
// unrunnable. Mirrors sloprail-tasks/tests's installPluginTree, with ONE
// deliberate difference: `.sloprail/file-guard/structure.yaml` is EXCLUDED
// from this copy and installed separately, as a genuine PLUGIN structure
// (installPluginStructure), because copying it into the project's own
// `.sloprail/` would make it the PROJECT's structure gate — which must never
// declare `scope` (a project structure with a `scope` is refused at load).
// This plugin's structure.yaml DOES declare `scope` (it is a plugin's own
// piece), so it belongs inside a plugin root, discovered as this plugin's
// contribution — never copied flat into the project's own tree.
//
// The tree is then committed so it is part of the session BASELINE rather
// than the first cycle's diff (the base plugin's authoring-slop after-check
// would otherwise judge these scripts with no model and fail closed).
func installPluginTree(t *testing.T, projDir string) {
	t.Helper()
	src := filepath.Join(pluginRoot(t), ".sloprail")
	dst := filepath.Join(projDir, ".sloprail")
	info, err := os.Stat(src)
	if err != nil || !info.IsDir() {
		t.Fatalf("install plugin tree: %s is not a directory (%v)", src, err)
	}
	structureSrc := filepath.Join(src, "file-guard", "structure.yaml")
	copied := 0
	err = filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == structureSrc {
			// EXCLUDED here on purpose — see the function comment. Installed
			// as a plugin structure by installPluginStructure instead.
			return nil
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
	harness.CommitInstalled(t, projDir)
}

// installPluginStructure installs THIS plugin's own real
// `.sloprail/file-guard/structure.yaml` — read verbatim off disk, not
// reauthored inline — as a genuine plugin structure gate via the harness's
// EnablePluginShippingStructure, so it is discovered as plugin
// "sloprail-content"'s own scoped contribution (the exact same discovery
// path a real install goes through), not copied flat into the project's own
// `.sloprail/` the way installPluginTree handles the file-guards. Must be
// called before Run/RunFrom, like installPluginTree.
func installPluginStructure(t *testing.T, e *Env, projDir string) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(pluginRoot(t), ".sloprail", "file-guard", "structure.yaml"))
	if err != nil {
		t.Fatalf("installPluginStructure: read structure.yaml: %v", err)
	}
	e.EnablePluginShippingStructure(projDir, "sloprail-content", string(body))
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

// shq single-quotes s for a POSIX shell, so a quote or a string with spaces,
// newlines or apostrophes reaches sr-file verbatim.
func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// srFileWrite is the grounded way to write a guarded file: `sr-file write`
// with the content on a quoted heredoc and one `--cite:user` per quote (none
// when quotes is empty). The line holds nothing but the sr-file call, so the
// pre-tool hook resolves its result exactly (resultKnown: true).
func srFileWrite(path, content string, quotes ...string) string {
	cmd := "sr-file write " + shq(path)
	for _, q := range quotes {
		cmd += " --cite:user " + shq(q)
	}
	return cmd + " <<'SR_FILE_EOF'\n" + strings.TrimSuffix(content, "\n") + "\nSR_FILE_EOF"
}

// srFileEdit is the grounded way to edit a guarded file: `sr-file edit` with
// the Edit tool's old/new strings and one `--cite:user` per quote.
func srFileEdit(path, oldString, newString string, quotes ...string) string {
	cmd := "sr-file edit " + shq(path) + " --old-string " + shq(oldString) + " --new-string " + shq(newString)
	for _, q := range quotes {
		cmd += " --cite:user " + shq(q)
	}
	return cmd
}

// readProj returns a project file's bytes, failing the test when it cannot be
// read.
func readProj(t *testing.T, proj, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(proj, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// judgePromptFile is where InstallJudgeClaudeCapturing records the rendered
// judge prompt, relative to the project.
const judgePromptFile = ".judge-prompt.txt"

// unitFrontmatter assembles a UNIT.md/02_draft.md with the given frontmatter
// fields and body. fields is rendered as raw YAML lines (already formatted by
// the caller), so a test can build exactly the shape it needs — a plain unit,
// one with tags, or one carrying published_urls: in frontmatter.
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
