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

// build compiles the sloprail binary once per test process.
func build(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		root := repoRoot(t)
		dir, err := os.MkdirTemp("", "sloprail-e2e-bin-")
		if err != nil {
			buildErr = err
			return
		}
		cmd := exec.Command("go", "build", "-o", filepath.Join(dir, "sloprail"), "./services/sloprail")
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			buildErr = fmt.Errorf("build sloprail: %v\n%s", err, out)
			return
		}
		builtDir = dir
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

	claudeDir := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		e.t.Fatalf("harness: mkdir .claude: %v", err)
	}
	settings := fmt.Sprintf(`{
  "enabledPlugins": { %q: true },
  "extraKnownMarketplaces": {
    %q: { "source": { "source": "directory", "path": %q } }
  }
}`, pluginKey, marketplaceName, e.repoRoot)
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(settings), 0o644); err != nil {
		e.t.Fatalf("harness: write settings: %v", err)
	}
	return dir
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
