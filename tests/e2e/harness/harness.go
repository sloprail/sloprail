// Package harness drives end-to-end tests the production-faithful way.
//
// The agent is a10n-claude-mock, a drop-in `claude` that emits real Claude Code
// JSONL and fires the lifecycle hooks it finds in settings. The hooks that fire
// are THIS repo's plugin, loaded from THIS repo's marketplace — not a
// hand-written hooks block. So a test exercises the same wiring a user gets:
// the plugin maps the harness's lifecycle onto our subcommands, and the real
// binary decides.
//
// What a test controls is what the agent tries to do. What happens afterwards
// is the product.
package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	t        *testing.T
	BinDir   string // holds the built sloprail binary, prepended to PATH
	Home     string // an isolated HOME, so a test never reads the real one
	repoRoot string
}

var (
	buildOnce sync.Once
	builtDir  string
	buildErr  error
)

// repoRoot returns the directory holding go.mod.
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

// New stands up an isolated environment with the binary built and on PATH.
func New(t *testing.T) *Env {
	t.Helper()
	if _, err := exec.LookPath("a10n-claude-mock"); err != nil {
		t.Skip("harness: a10n-claude-mock not on PATH")
	}
	return &Env{
		t:        t,
		BinDir:   build(t),
		Home:     t.TempDir(),
		repoRoot: repoRoot(t),
	}
}

// Project creates a project directory with this repo's plugin enabled, exactly
// as a user would have it: a marketplace source and an enabled plugin, not a
// hand-written hooks block.
func (e *Env) Project() string {
	e.t.Helper()
	dir := e.t.TempDir()

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

	if err := os.WriteFile(filepath.Join(claudeDir, "settings.local.json"), []byte(settings), 0o644); err != nil {
		e.t.Fatalf("harness: write settings: %v", err)
	}
	return dir
}

// Guardrail writes a declaration and its hook script into a project.
func (e *Env) Guardrail(projDir, name, declaration string, scripts map[string]string) {
	e.t.Helper()
	dir := filepath.Join(projDir, ".sloprail", "guardrails", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		e.t.Fatalf("harness: mkdir guardrail: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "GUARDRAIL.md"), []byte(declaration), 0o644); err != nil {
		e.t.Fatalf("harness: write declaration: %v", err)
	}
	for name, body := range scripts {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
			e.t.Fatalf("harness: write script %s: %v", name, err)
		}
	}
}

// Hook invokes one of our hook points directly, the way the plugin does: the
// payload on stdin, nothing on the command line.
//
// This exercises the same code the plugin reaches, without waiting for a whole
// mocked conversation — used where a test is about what a hook decides rather
// than about the wiring that reaches it.
func (e *Env) Hook(projDir string, args []string, payload any) (stdout string, exitCode int) {
	e.t.Helper()

	b, err := json.Marshal(payload)
	if err != nil {
		e.t.Fatalf("harness: marshal payload: %v", err)
	}

	cmd := exec.Command(filepath.Join(e.BinDir, "sloprail"), args...)
	cmd.Dir = projDir
	cmd.Stdin = strings.NewReader(string(b))
	cmd.Env = append(os.Environ(),
		"HOME="+e.Home,
		"PATH="+e.BinDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)

	var stderr strings.Builder
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	// The engine reports its own trouble on stderr and permits the work; a test
	// that swallowed it would show a silent pass where the engine had actually
	// failed to run a rule at all.
	if s := stderr.String(); s != "" {
		e.t.Logf("sloprail stderr: %s", s)
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return string(out), exitErr.ExitCode()
	}
	if err != nil {
		e.t.Fatalf("harness: run %v: %v", args, err)
	}
	return string(out), 0
}
