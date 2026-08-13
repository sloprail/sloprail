// Package harness drives end-to-end tests the way a session actually runs.
//
// The agent is a10n-claude-mock, a drop-in `claude` that streams Claude Code
// JSONL and fires the lifecycle hooks it finds in the project's settings. The
// hooks it fires are THIS repo's plugin, loaded from THIS repo's marketplace.
//
// A test therefore controls only what the agent tries to do. Everything after
// that — the hook firing, the plugin reaching our subcommand, the engine
// deciding, the refusal travelling back — runs as a user would get it. A test
// that invoked our binary directly would prove the engine decides correctly
// while proving nothing about whether anything ever asks it.
//
// The isolation and transcript-seeding here are ported from a10n's harness,
// which learned them the hard way: a "sandboxed" run must never read or write
// the host's own claude data.
package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
)

const (
	marketplaceName = "sloprail-marketplace"
	pluginName      = "sloprail"
	pluginKey       = pluginName + "@" + marketplaceName
)

// Env is one isolated end-to-end environment.
type Env struct {
	t         *testing.T
	binDir    string // the built sloprail binary, prepended to PATH so the plugin finds it
	home      string
	configDir string // an isolated stand-in for ~/.claude
	pluginDir string
	repoRoot  string
	mock      string
}

var (
	buildOnce sync.Once
	builtDir  string
	buildErr  error
)

func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("harness: locate repo root: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// gitDir is the repository's git directory — a real directory even when the
// worktree's own .git is a file pointing at it.
func gitDir(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--absolute-git-dir").Output()
	if err != nil {
		t.Fatalf("harness: locate git dir: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// Cleanup removes what build left behind. Call it from TestMain after m.Run,
// which is the only place that can: the binary is shared by every test in the
// process, so it must outlive each of them, and t.Cleanup would delete it out
// from under the second test to ask for it.
//
// Skipping this leaks 15M per test process, and there is one process per e2e
// package. That reached 337 directories and 5GB during one wave of work, and
// filled the disk mid-run — which fails as a build error in whichever test is
// unlucky, not as anything that names the real cause.
func Cleanup() {
	if builtDir != "" {
		os.RemoveAll(builtDir)
	}
}

// build compiles the sloprail binary once per test process.
func build(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		root := repoRoot(t)
		// Not the system temp dir. macOS reaps /var/folders/.../T/ on its own
		// schedule, and it does so mid-run: a suite that builds once and then
		// executes that binary across several minutes of tests finds it gone
		// partway through. That surfaces as `fork/exec ...: no such file or
		// directory` in whichever test was unlucky — an error that names the
		// binary and says nothing about the reaper, which cost one agent eight
		// failures in twenty-two runs before the cause was found.
		//
		// Under the git directory because it is inside the repo (so nothing
		// reaps it) but outside the working tree (so it cannot be mistaken for
		// a project file, and a test that walks the tree does not find a binary
		// in it).
		//
		// Asked of git rather than joined onto the root as ".git": in a linked
		// worktree that path is a FILE, and every agent on this project works
		// in one, so building the path by hand fails for all of them and
		// succeeds only in the main checkout.
		parent := filepath.Join(gitDir(t), "sloprail-e2e")
		if err := os.MkdirAll(parent, 0o755); err != nil {
			buildErr = err
			return
		}
		dir, err := os.MkdirTemp(parent, "bin-")
		if err != nil {
			buildErr = err
			return
		}
		// Recorded before the build, not after. A build that fails still leaves
		// the directory behind, and the failure that matters here is a full
		// disk — so the path that leaks is the one that runs when leaking is
		// already the problem.
		builtDir = dir
		cmd := exec.Command("go", "build", "-o", filepath.Join(dir, "sloprail"), "./services/sloprail")
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			buildErr = fmt.Errorf("build sloprail: %v\n%s", err, out)
			return
		}
	})
	if buildErr != nil {
		t.Fatalf("harness: %v", buildErr)
	}
	return builtDir
}

// New stands up an isolated environment.
func New(t *testing.T) *Env {
	t.Helper()
	mock, err := exec.LookPath("a10n-claude-mock")
	if err != nil {
		t.Skip("harness: a10n-claude-mock not on PATH — driving it is the whole point")
	}
	// A short root, not t.TempDir(): the encoded project-dir path below is a
	// 1:1 non-alphanumeric substitution with no shortening, and a long test
	// name pushes a single component past the filename limit.
	root, err := os.MkdirTemp("", "slop-e2e-")
	if err != nil {
		t.Fatalf("harness: temp root: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })

	e := &Env{
		t:         t,
		binDir:    build(t),
		home:      filepath.Join(root, "home"),
		configDir: filepath.Join(root, "claude-cfg"),
		pluginDir: filepath.Join(root, "plugins"),
		repoRoot:  repoRoot(t),
		mock:      mock,
	}
	for _, d := range []string{e.home, e.configDir, e.pluginDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("harness: mkdir %s: %v", d, err)
		}
	}
	return e
}

// Project creates a project with this repo's plugin enabled, exactly as a user
// would have it: a marketplace source and an enabled plugin, not a hand-written
// hooks block. What fires during a test is the same wiring anyone installing
// this would get.
func (e *Env) Project() string {
	e.t.Helper()
	dir, err := os.MkdirTemp("", "slop-proj-")
	if err != nil {
		e.t.Fatalf("harness: temp project: %v", err)
	}
	e.t.Cleanup(func() { os.RemoveAll(dir) })

	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
		e.t.Fatalf("harness: mkdir .claude: %v", err)
	}
	e.writeSettings(dir, nil)
	return dir
}

// ExtraHook attaches a raw lifecycle hook to a project, alongside the plugin.
//
// For the few properties that are about a hook POINT rather than about a
// guardrail: that a refusal at an after-the-fact point cannot prevent work that
// has already landed, for instance, is a claim about the lifecycle itself and
// has to be made where a guardrail binding cannot reach.
//
// The plugin's own wiring is rewritten from the same source as Project's, so
// this adds a hook to the ordinary arrangement rather than replacing it with a
// hand-written one — a test using this is still running the engine a user gets.
func (e *Env) ExtraHook(projDir, event, matcher, command string) {
	e.t.Helper()
	e.writeSettings(projDir, map[string]string{
		"event": event, "matcher": matcher, "command": command,
	})
}

// writeSettings writes the project's settings: the plugin as a user would
// install it, plus at most one extra lifecycle hook.
//
// One place builds this, so the marketplace wiring a test runs against cannot
// drift from the wiring Project documents.
func (e *Env) writeSettings(dir string, extra map[string]string) {
	e.t.Helper()
	settings := map[string]any{
		"enabledPlugins": map[string]any{pluginKey: true},
		"extraKnownMarketplaces": map[string]any{
			marketplaceName: map[string]any{
				"source": map[string]any{"source": "directory", "path": e.repoRoot},
			},
		},
	}
	if extra != nil {
		settings["hooks"] = map[string]any{
			extra["event"]: []any{map[string]any{
				"matcher": extra["matcher"],
				"hooks": []any{map[string]any{
					"type": "command", "command": extra["command"],
				}},
			}},
		}
	}
	body, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		e.t.Fatalf("harness: encode settings: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".claude", "settings.json"), body, 0o644); err != nil {
		e.t.Fatalf("harness: write settings: %v", err)
	}
}

// CLI runs the sloprail binary directly and returns what it produced.
//
// The session subcommands are not tested this way — those are invoked by a
// harness, and a test that called them itself would prove the engine decides
// correctly while proving nothing about whether anything ever asks it, which is
// the whole reason Run drives the mock instead.
//
// This is for the commands a PERSON or an authoring agent types: `init` and
// `guardrail help`. Nothing in a session invokes them, so there is no wiring
// for driving the mock to prove.
func (e *Env) CLI(dir string, args ...string) Result {
	e.t.Helper()
	cmd := exec.Command(filepath.Join(e.binDir, "sloprail"), args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HOME="+e.home)
	out, err := cmd.CombinedOutput()
	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		e.t.Fatalf("harness: run sloprail %v: %v\n%s", args, err, out)
	}
	return Result{Output: string(out), Code: code}
}

// Guardrail writes a declaration and its hook scripts into a project.
func (e *Env) Guardrail(projDir, name, declaration string, scripts map[string]string) {
	e.t.Helper()
	dir := filepath.Join(projDir, ".sloprail", "guardrails", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		e.t.Fatalf("harness: mkdir guardrail: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "GUARDRAIL.md"), []byte(declaration), 0o644); err != nil {
		e.t.Fatalf("harness: write declaration: %v", err)
	}
	for file, body := range scripts {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o755); err != nil {
			e.t.Fatalf("harness: write script %s: %v", file, err)
		}
	}
}

// Ledger returns the lines a guardrail's hooks appended to a file in their own
// folder, or nothing when the file was never created.
//
// This is how a test observes what DID NOT happen. A refusal travels back
// through the tool result and can be read off the stream, but "this hook never
// ran", "this extractor produced nothing" and "these two hooks ran in this
// order" leave no trace there — a hook that stays silent and a hook that never
// ran look identical from outside.
//
// So the hooks write. A hook is an ordinary shell script run with its working
// directory set to the guardrail's folder, so appending a line to a file there
// is the one channel that records a run without the engine's cooperation and
// without a test reaching inside the binary. An absent file is a real answer:
// nothing ran.
func (e *Env) Ledger(projDir, guardrail, file string) []string {
	e.t.Helper()
	body, err := os.ReadFile(filepath.Join(projDir, ".sloprail", "guardrails", guardrail, file))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		e.t.Fatalf("harness: read ledger %s/%s: %v", guardrail, file, err)
	}
	var lines []string
	for _, l := range strings.Split(string(body), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

var nonAlnumRe = regexp.MustCompile(`[^a-zA-Z0-9]`)

// encodeProjectDir mirrors how a harness encodes a working directory into a
// transcript path. Ported from a10n, which ported it from claude's own.
func encodeProjectDir(dir string) string { return nonAlnumRe.ReplaceAllString(dir, "-") }

// resolveWorkDir resolves symlinks so the encoded path matches what the mock
// itself will compute — macOS resolves /var to /private/var, and a mismatch
// puts the seeded transcript somewhere nothing looks.
func resolveWorkDir(dir string) string {
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		return resolved
	}
	return dir
}

// seedTranscript writes a minimal valid transcript for (cwd, sessionID): a root
// record with a uuid and a null parentUuid, which is the shape anything looking
// for a conversation's origin scans for.
func (e *Env) seedTranscript(cwd, sessionID, prompt string) {
	e.t.Helper()
	dir := filepath.Join(e.configDir, "projects", encodeProjectDir(resolveWorkDir(cwd)))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		e.t.Fatalf("harness: seed transcript: %v", err)
	}
	line := fmt.Sprintf(`{"type":"user","uuid":%q,"parentUuid":null,"cwd":%q,"message":{"role":"user","content":%q}}`+"\n",
		"e2e-root-"+sessionID, cwd, prompt)
	path := filepath.Join(dir, sessionID+".jsonl")
	if _, err := os.Stat(path); err == nil {
		return // already seeded, or the mock has started writing — never overwrite
	}
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		e.t.Fatalf("harness: seed transcript: %v", err)
	}
}

// ControlDecl and ControlScript are the positive control every revalidation
// test rests on: a hook that stores something under its own scope and reads it
// back on its next invocation.
//
// Here rather than in one scenario package because every scenario needs it and
// a Go test package cannot import another's helpers. Exported so the scenario
// that reports the control as a test of its own runs the SAME rule this package
// gates on — two copies could drift, and the copy the gate used would be the
// one nobody was reading. See RequireSessionStore.
const ControlDecl = `---
hooks:
  PreFileCreate:
    - hooks:
        - type: command
          command: ./probe.sh
  PreFileUpdate:
    - hooks:
        - type: command
          command: ./probe.sh
---

# Reads its own state back, and says what it found.
`

const ControlScript = `#!/bin/sh
cat >/dev/null
echo "before=[$(sloprail session state get seen 2>&1)]" >> "$PWD/log"
sloprail session state set seen yes >/dev/null 2>&1
exit 0
`

// SessionStoreOpens reports whether the engine can identify this session and
// open its state at all.
//
// This is the positive control, and it is why every skip test in this tree can
// be believed. A revalidation test asserts a hook did NOT run again — a claim
// that passes trivially when the hook never ran, and just as trivially when
// the engine could not identify the session and so had no record to skip
// against. Both failures are silent, and both make a green suite mean nothing.
//
// The branch that first wrote an e2e for this feature hit exactly that: the
// mock's main-session PreToolUse payload carries no transcript_path, session
// identity failed, no store was opened, every hook re-judged — and the test
// passed whether the skip worked or not. It deleted the test rather than bank
// a vacuous one.
//
// So this establishes the capability POSITIVELY: something is written in one
// hook invocation and read back in the next. Nothing about that can pass by
// accident. A store that never opened returns an error to the hook; one keyed
// differently per invocation returns "not found"; only a store that really
// opened, under one identity, across two separate hook processes, hands back
// what the earlier one put there.
//
// Deliberately a check of the observable capability rather than of the branch
// or the harness, so it keeps working unchanged when the missing piece lands
// rather than needing to be told that it did.
func (e *Env) SessionStoreOpens() (bool, string) {
	e.t.Helper()

	proj := e.Project()
	e.Guardrail(proj, "control", ControlDecl, map[string]string{"probe.sh": ControlScript})

	// Two DIFFERENT paths, so neither invocation can be exempted by the other.
	// The control must not be silenced by the very mechanism it exists to make
	// testable: two writes of one path would let the second be legitimately
	// skipped, and the control would report "unreachable" on a build where the
	// store works perfectly.
	e.Run(proj, "s-control", "write twice", Turns("done",
		Write("c1", "one.md", "first"),
		Write("c2", "two.md", "second"),
	))

	lines := e.Ledger(proj, "control", "log")
	if len(lines) != 2 {
		return false, "the control guardrail's hook did not run twice (got " +
			strings.Join(lines, " | ") + ") — nothing about session state can be concluded"
	}
	// The SECOND invocation is the one that matters. The first legitimately
	// finds nothing: it is what wrote the mark.
	if !strings.Contains(lines[1], "before=[yes]") {
		return false, "a hook could not read back what the previous hook in the same session stored: " +
			strings.Join(lines, " | ")
	}
	return true, ""
}

// RequireSessionStore skips the calling test, naming the branch that closes the
// gap, when the session store cannot be shown to open.
//
// Skipped rather than failed, and skipped rather than left to pass: this branch
// owns tests/e2e/ only, and the two pieces the store needs — a transcript path
// the mock does not send on PreToolUse, and the hook environment `session
// state` resolves its scope from — are both on impl/hook-env. A test asserting
// "the hook did not run again" while the store is unreachable would be green
// and worthless, which is the precise failure the control exists to prevent.
func RequireSessionStore(t *testing.T) {
	t.Helper()
	e := New(t)
	if ok, why := e.SessionStoreOpens(); !ok {
		t.Skipf("the session store does not open on this branch, so a skip cannot be observed "+
			"and a passing skip test would be vacuous — the fix (transcript path derived from "+
			"session id + cwd, and c.Env on the hook process) is on impl/hook-env: %s", why)
	}
}

// Fork makes a NEW session id that a conversation continues under, the way a
// harness re-forks one mid-conversation.
//
// This is the only way to test that state survives a re-fork, and it has to be
// built rather than asked for: the mock has no compaction or retry path that
// changes the id of a running session, so the transcript a fork would leave is
// written here instead. What is written is the shape the identity walk actually
// looks for — nothing about it is invented for the test's convenience:
//
//   - the new transcript's own root record is parentless, so it IS a root
//     within its file, exactly like any other transcript's first record;
//   - it carries logicalParentUuid naming a record in the OLD transcript,
//     which is what marks it a continuation rather than a new conversation;
//   - the record it names is really in the old file, so the walk crossing the
//     restart finds it where it says.
//
// The conversation's identity is therefore the OLD transcript's root uuid,
// reached by one hop, while the id the harness reports is the new one. That is
// precisely the situation the invariant is about: a store keyed on the reported
// id opens an empty database, and a store keyed on the origin finds the
// verdicts already recorded.
//
// The old session must have been Run (or seeded) first — a fork continuing a
// file that does not exist is not a fork, and the walk would fail rather than
// resolve to the wrong thing, which would make the test pass for the wrong
// reason.
func (e *Env) Fork(cwd, oldSessionID, newSessionID string) {
	e.t.Helper()

	dir := filepath.Join(e.configDir, "projects", encodeProjectDir(resolveWorkDir(cwd)))
	oldPath := filepath.Join(dir, oldSessionID+".jsonl")
	if _, err := os.Stat(oldPath); err != nil {
		e.t.Fatalf("harness: fork %s: the session being continued has no transcript at %s: %v",
			oldSessionID, oldPath, err)
	}

	// The record the new file continues FROM. seedTranscript's root is the one
	// record every seeded session is guaranteed to have, and it is genuinely in
	// the old file — asserted below rather than assumed, because a fork pointing
	// at a record that is not there resolves to nothing and the test would fail
	// for a reason that has nothing to do with the invariant.
	continued := "e2e-root-" + oldSessionID
	body, err := os.ReadFile(oldPath)
	if err != nil {
		e.t.Fatalf("harness: fork %s: read %s: %v", oldSessionID, oldPath, err)
	}
	if !strings.Contains(string(body), `"uuid":"`+continued+`"`) {
		e.t.Fatalf("harness: fork %s: %s does not hold the record %q the fork would continue from",
			oldSessionID, oldPath, continued)
	}

	// Parentless within its own file AND naming what it continues: both, which
	// is what a real re-forked transcript looks like and what makes the walk
	// take its second hop instead of stopping here.
	line := fmt.Sprintf(
		`{"type":"user","uuid":%q,"parentUuid":null,"logicalParentUuid":%q,"cwd":%q,"message":{"role":"user","content":"continued"}}`+"\n",
		"e2e-fork-"+newSessionID, continued, cwd)

	path := filepath.Join(dir, newSessionID+".jsonl")
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		e.t.Fatalf("harness: fork %s: %v", oldSessionID, err)
	}
}

// SessionIdentity is the identity the engine resolves for a session's
// transcript — the conversation's own origin, not the id the harness reports.
//
// Asked of the binary under test rather than derived here. The walk that
// crosses a re-fork is the thing under test, so a test computing it a second
// way would be comparing its own reimplementation against itself and would
// agree with a broken engine.
//
// Returns "" when the engine cannot resolve one, which is an answer rather than
// a failure: a test asserting that two transcripts resolve alike needs to be
// able to say that neither did.
func (e *Env) SessionIdentity(projDir, sessionID string) string {
	e.t.Helper()

	transcript := filepath.Join(e.configDir, "projects",
		encodeProjectDir(resolveWorkDir(projDir)), sessionID+".jsonl")
	payload := fmt.Sprintf(`{"transcript_path":%q,"cwd":%q}`, transcript, projDir)

	cmd := exec.Command(filepath.Join(e.binDir, "sloprail"), "session", "id")
	cmd.Dir = projDir
	cmd.Stdin = strings.NewReader(payload)
	cmd.Env = append(os.Environ(),
		"HOME="+e.home,
		"CLAUDE_CONFIG_DIR="+e.configDir,
	)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Result is what a run produced.
type Result struct {
	Output string
	Code   int
}

// Saw reports whether text appears anywhere in the stream — the tool results a
// refusal travels back in, or the agent's own output.
func (r Result) Saw(text string) bool { return strings.Contains(r.Output, text) }

// Run drives a scenario through the mock as a real session.
func (e *Env) Run(projDir, sessionID, prompt string, s Scenario) Result {
	e.t.Helper()

	e.seedTranscript(projDir, sessionID, prompt)

	scriptPath := filepath.Join(projDir, ".scenario.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\n"+s.script()+"\n"), 0o755); err != nil {
		e.t.Fatalf("harness: write scenario: %v", err)
	}

	cmd := exec.Command(e.mock,
		"-p", "--output-format", "stream-json",
		"--script", scriptPath,
		"--project-dir", projDir,
		"--config-dir", e.configDir,
		"--plugin-cache-dir", e.pluginDir,
		"--session-id", sessionID,
		prompt,
	)
	cmd.Dir = projDir
	cmd.Env = append(os.Environ(),
		"HOME="+e.home,
		"CLAUDE_CONFIG_DIR="+e.configDir,
		"CLAUDE_CODE_SESSION_ID="+sessionID,
		"CLAUDE_CODE_PLUGIN_CACHE_DIR="+e.pluginDir,
		// The plugin invokes `sloprail`; this is how the hook subprocess finds
		// the build under test rather than whatever happens to be installed.
		"PATH="+e.binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)

	out, err := cmd.CombinedOutput()
	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		e.t.Fatalf("harness: run mock: %v\n%s", err, out)
	}
	e.t.Logf("mock:\n%s", out)
	return Result{Output: string(out), Code: code}
}
