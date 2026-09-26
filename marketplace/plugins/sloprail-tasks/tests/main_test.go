// Package e2e is the sloprail-tasks plugin's OWN end-to-end suite — the first
// instance of the "each use-case plugin self-tests" model. It drives the
// a10n-claude-mock through the SHARED harness (the same one tests/e2e/session/*
// and tests/e2e/examples/* use), ENABLING this plugin as a real, discovered
// plugin (installPluginTree -> harness.EnableRealPlugin) and asserting its
// guardrails refuse and permit the right writes and turn-ends.
//
// What fires during a test is exactly what a user installing this plugin gets:
// the base `sloprail` plugin's hooks (enabled by the harness's Project()) run
// the nature dispatch, which discovers this plugin from its OWN root — nothing
// is copied into the project — and loads its .sloprail tree: six file-guards, two
// gates, and (since sloprail#29 landed composition) a structure gate scoped to
// memories/tasks/. A test controls only what the mock tries to do; the hook
// firing, the engine deciding, and the refusal travelling back all run as
// production would. See README.md for the model.
//
// The judge verdict is a fixed stub (InstallJudgeClaude) exactly as the main
// suite's judge e2e do — the model call is the one thing a mock cannot supply for
// sr-agent's judge path (the mock refuses sr-agent's --model/--settings flags). The
// DETERMINISTIC halves — the citation script, cite's own grounding, the pre-flight,
// the gate's tree scan — are NOT stubbed and run for real against the transcript
// the mock streamed.
package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// Env is the harness environment, aliased so helpers read like the main suite's.
type Env = harness.Env

// The harness API, re-exported under short names so the tests read like the main
// suite's do.
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

// authPrompt is the human message every session starts from. The harness seeds it
// as the transcript's root user message at line 1, so a citation whose quote is a
// substring of this — grounded via `sr-session trajectory cite` — resolves to a
// real user line. Distinctive so a test can tell it from a fabricated quote.
const authPrompt = "Please migrate the auth module to the new token format."

// taskPath is a TASK.md at the two-level path the guardrails match:
// memories/tasks/<category>/<name>/TASK.md.
const taskPath = "memories/tasks/auth/migrate-tokens/TASK.md"

// pluginName is what this plugin is enabled BY — must match
// .claude-plugin/plugin.json's own "name", since that is what a refusal
// attributes to ("… from plugin \"sloprail-tasks\"") and what the structure
// gate reports as the owner of its scope.
const pluginName = "sloprail-tasks"

// installPluginTree enables THIS plugin's own, already-on-disk .sloprail tree
// (file-guard/, gate/, and now structure.yaml) as a REAL, discovered plugin —
// harness.EnableRealPlugin, pointed at pluginRoot(t) — rather than copying its
// files into the project's own .sloprail. This is what a consumer installing
// the plugin gets: the guardrails, INCLUDING the structure gate, are found
// inside the plugin, never inside the project.
//
// EXCEPT task.cue. The schema is the one piece the plugin's own README and the
// schema file's own header both say is installed INTO the consumer's project
// — "$SR_WORKSPACE/.sloprail/schemas/task.cue — installing the plugin means
// placing this file there, the same as any project schema" — because a task
// lives in the CONSUMER's tree, not the plugin's, and every guard script
// resolves the schema under $SR_WORKSPACE for exactly that reason. So this is
// the one file still copied, into the project, mirroring the one manual step
// a real install performs; everything else the plugin ships is discovered,
// never copied.
//
// Nothing else is copied and nothing is committed. The old copy-then-commit
// shape existed for two reasons, both gone now that discovery is real for the
// guardrails:
//   - the scripts' execute bits had to survive a copy — moot for the
//     guardrails, since nothing but the schema is copied, and the schema
//     carries no execute bit to lose;
//   - a freshly copied, UNCOMMITTED tree inside the project read as this
//     session's own diff to authoring-slop's after-check, which would judge
//     the guard scripts with no model in the e2e and fail closed — moot too,
//     since the guardrails are never part of the PROJECT's diff at all, the
//     same property 026_guardrails_from_plugins relies on for the real
//     shipped authoring-slop. The lone copied schema file is inert prose to
//     that check (no .sh/.md.j2 shape), so it needs no such protection.
//
// The plugin's structure gate is no longer excluded (see the git history for
// when it was): composition landed (sloprail#29), so an installed
// structure.yaml now correctly governs only its own `scope`
// (memories/tasks/) rather than the whole project — see
// TestStructureGate_* for the enforcement-level proof.
func installPluginTree(t *testing.T, e *Env, projDir string) {
	t.Helper()
	root := pluginRoot(t)
	if _, err := os.Stat(filepath.Join(root, ".sloprail")); err != nil {
		t.Fatalf("install plugin tree: %s has no .sloprail (%v)", root, err)
	}
	e.EnableRealPlugin(projDir, pluginName, root)

	schema, err := os.ReadFile(filepath.Join(root, ".sloprail", "schemas", "task.cue"))
	if err != nil {
		t.Fatalf("install plugin tree: read task.cue: %v", err)
	}
	e.WriteFile(projDir, filepath.Join(".sloprail", "schemas", "task.cue"), string(schema))
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

// taskWithEvidence assembles a TASK.md carrying the DELIVERY evidence in its
// frontmatter — the observations and artifacts lists task-review judges — alongside
// the status, priority and body.
//
// observations and artifacts are the citation STRINGS (not markdown links): an
// observation is an absolute `<session.jsonl>:<ranges>`, an artifact a repo-relative
// `<file>:<ranges>`. They are rendered as inline JSON arrays (task.cue's spelling,
// which sr-file validate reads), so the test writes exactly the frontmatter a real
// in_review task carries. An empty list is omitted, so a test can build a task with
// only one kind to exercise the "both are mandatory" refusal.
func taskWithEvidence(status, priority, body string, observations, artifacts []string) string {
	fm := "---\nstatus: " + status + "\npriority: " + priority + "\n"
	if len(observations) > 0 {
		fm += "observations: [" + jsonList(observations) + "]\n"
	}
	if len(artifacts) > 0 {
		fm += "artifacts: [" + jsonList(artifacts) + "]\n"
	}
	fm += "---\n\n" + body + "\n"
	return fm
}

// jsonList renders items as a comma-separated list of JSON string literals — the
// inline-array body task.cue's observations/artifacts are written as.
func jsonList(items []string) string {
	var b strings.Builder
	for i, it := range items {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(`"` + strings.ReplaceAll(it, `"`, `\"`) + `"`)
	}
	return b.String()
}

// toolResultLine returns the 1-based physical line of the FIRST tool_result record
// in the transcript at path whose content contains marker, or 0 when none does — an
// independent witness (a plain file scan) of where the mock put a tool_result, so an
// observation citation can name its real line rather than a guessed one.
//
// It keys on the tool_result block shape a tool result lands as (`"type":"tool_result"`
// with the marker in its content) so it is not fooled by the marker appearing in the
// agent's prose or the user's prompt on some other line.
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

// answerEnvelopeLine returns the 1-based physical line of the AskUserQuestion answer
// envelope — a tool_result record whose body opens `The user answered:`. It matches
// the SAME `"type":"tool_result"` shape toolResultLine keys on (that structural
// twinning is exactly why an answer envelope can masquerade as a delivery result),
// so the distinguishing marker is the envelope's own prose.
func answerEnvelopeLine(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read transcript %s: %v", path, err)
	}
	for i, line := range strings.Split(string(b), "\n") {
		if strings.Contains(line, `"type":"tool_result"`) && strings.Contains(line, "The user answered:") {
			return i + 1
		}
	}
	return 0
}

// transcriptText returns the transcript file at path as a string, for a failure
// message when a line-derivation did not find what it expected.
func transcriptText(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return "(could not read " + path + ": " + err.Error() + ")"
	}
	return string(b)
}

func containsStr(haystack, needle string) bool { return strings.Contains(haystack, needle) }
