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
// is copied into the project — and loads its .sloprail tree: six file-guards, eight
// gates (six of them the PreFileWrite prevention halves of those file-guards), and (since sloprail#29 landed composition) a structure gate scoped to
// memories/tasks/. A test controls only what the mock tries to do; the hook
// firing, the engine deciding, and the refusal travelling back all run as
// production would. See README.md for the model.
//
// The judge verdict is a fixed stub (InstallJudgeClaude) exactly as the main
// suite's judge e2e do — the model call is the one thing a mock cannot supply for
// sr-agent's judge path (the mock refuses sr-agent's --model/--settings flags). The
// DETERMINISTIC halves — sr-file's own citation resolution against the transcript
// the mock streamed, the body-change script, the evidence check, the pre-flight,
// the gate's tree scan — are NOT stubbed and run for real.
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
	CitesUser      = harness.CitesUser
	CitesTool      = harness.CitesTool
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

// Turn is one scripted agent action, aliased so helpers can return them.
type Turn = harness.Turn

// askQuote is the user's own words the tests ground a task's body in — a substring
// of authPrompt, which the harness seeds as the transcript's root user message. The
// session resolves it against the `user` pool when a write cites it.
//
// Cite it only in a session's FIRST Run: a repeat Run on the same session resumes
// with its prompt appended as another user message, and a quote matching two
// entries resolves to neither. A later Run that needs a fresh user citation passes
// a prompt of its own and quotes that.
const askQuote = "migrate the auth module"

// askBody is a task body stating the ask in derived text — the words of the ask,
// no transcript path, no link. The grounding rides on the write, not in the file.
const askBody = "Migrate the auth module to the new token format."

// proofMarker is the distinctive text a delivery tool run prints, and what an
// in_review write cites with --cite:tool_result. It appears in exactly one tool
// result per session (deliveryTurns runs once), so the quote resolves uniquely.
const proofMarker = "TESTS-PASSED-42"

// proofOutput is the whole line the delivery tool run prints. Its non-quoted part
// ("ok  sloprail/auth") lets a test prove the reviewer is handed the FULL tool
// result, not only the words the agent quoted.
const proofOutput = "ok  sloprail/auth  0.42s  " + proofMarker

// shq single-quotes s for a POSIX shell, so a Bash turn passes it verbatim.
func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// citeUser / citeTool render one sr-file citation flag: the user's own words, or
// a tool's output.
func citeUser(quote string) string { return "--cite:user " + shq(quote) }
func citeTool(quote string) string { return "--cite:tool_result " + shq(quote) }

// srWrite is a Bash turn that writes path with sr-file — the Write tool's
// semantics plus citations on the command. Run on its own in the line, so the
// pre-tool hook resolves it exactly (resultKnown true) and the file events carry
// the citations the session resolved.
func srWrite(id, path, content string, cites ...string) Turn {
	return Bash(id, "sr-file write "+path+" --content "+shq(content)+" "+strings.Join(cites, " "))
}

// srEdit is a Bash turn that edits path with sr-file — the Edit tool's old/new
// string replacement plus citations on the command.
func srEdit(id, path, oldS, newS string, cites ...string) Turn {
	return Bash(id, "sr-file edit "+path+" --old-string "+shq(oldS)+" --new-string "+shq(newS)+" "+strings.Join(cites, " "))
}

// deliveryTurns is the work that happens BEFORE a task claims it: the artifact
// file lands in the tree (a Write the mock executes), and a real tool run prints
// proofOutput, so its tool_result is on the transcript for an in_review write to
// cite. Both are tool_use turns, so a scenario can continue after them.
func deliveryTurns(artifactPath string) []Turn {
	return []Turn{
		Write("wf", artifactPath, "package auth\n\n// migrated to the new token format\nfunc Migrate() error {\n\treturn nil\n}\n"),
		Bash("b0", "echo "+shq(proofOutput)),
	}
}

// then appends more turns to a slice of them, so a scenario reads
// Turns("done", then(deliveryTurns(p), srWrite(...))...).
func then(first []Turn, more ...Turn) []Turn { return append(append([]Turn{}, first...), more...) }

// task assembles a TASK.md with the given frontmatter status/priority and body.
func task(status, priority, body string) string {
	return "---\nstatus: " + status + "\npriority: " + priority + "\n---\n\n" + body + "\n"
}

// taskWithArtifacts assembles a TASK.md whose frontmatter names its artifacts —
// where the result of the work is, repo-relative `<file>:<ranges>` — rendered as an
// inline JSON array (task.cue's spelling, which sr-file validate reads). An empty
// list is omitted. The proof that the work happened is NOT here: it rides on the
// write as --cite:tool_result.
func taskWithArtifacts(status, priority, body string, artifacts []string) string {
	fm := "---\nstatus: " + status + "\npriority: " + priority + "\n"
	if len(artifacts) > 0 {
		fm += "artifacts: [" + jsonList(artifacts) + "]\n"
	}
	return fm + "---\n\n" + body + "\n"
}

// jsonList renders items as a comma-separated list of JSON string literals — the
// inline-array body task.cue's artifacts are written as.
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

// readFile returns a project file's bytes, or "" when it is absent.
func readFile(t *testing.T, proj, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(proj, rel))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

func containsStr(haystack, needle string) bool { return strings.Contains(haystack, needle) }
