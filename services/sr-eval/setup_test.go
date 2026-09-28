package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A setup runs in the agent's environment, not the operator's, with git pinned
// so the operator's own config cannot fail it: here a global config that signs
// every commit with a signer that always fails, and a hooks path whose
// pre-commit always refuses. The script commits WITHOUT --no-gpg-sign, as a
// fixture author reasonably would. It also leaves a file uncommitted, which must
// land in the baseline commit setUp makes after it.
func TestSetUp_RunsInTheAgentsEnvironmentWithGitPinned(t *testing.T) {
	hooks := t.TempDir()
	mustWriteFile(t, filepath.Join(hooks, "pre-commit"), "#!/bin/sh\necho 'operator hook ran' >&2\nexit 1\n")
	if err := os.Chmod(filepath.Join(hooks, "pre-commit"), 0o755); err != nil {
		t.Fatal(err)
	}
	global := filepath.Join(t.TempDir(), "gitconfig")
	mustWriteFile(t, global, "[commit]\n\tgpgsign = true\n[gpg]\n\tprogram = false\n[core]\n\thooksPath = "+hooks+"\n")
	// The operator's environment points at it; so does the agent's, which is
	// derived from the operator's.
	t.Setenv("GIT_CONFIG_GLOBAL", global)

	dir := newTestFixtureTree(t, false)
	mustWriteFile(t, filepath.Join(dir, "fixture.yaml"), "seed: seed\nmodel: haiku\nscore: score.sh\nsetup: setup.sh\n")
	mustWriteFile(t, filepath.Join(dir, "setup.sh"), "#!/bin/sh\nset -eu\n"+
		"echo x > a.txt\ngit add a.txt\ngit commit -q -m a\n"+
		"printf '%s' \"$HOME\" > home.txt\n"+
		"printf '%s' \"$SR_EVAL_PROJECT_DIR\" > project.txt\n")
	if err := os.Chmod(filepath.Join(dir, "setup.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	fx, err := LoadFixture(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	project := t.TempDir()
	if out, err := exec.Command("git", "-C", project, "init", "--quiet").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	agentHome := t.TempDir()
	env := baseAgentEnv(os.Environ(), agentHome, t.TempDir(), false)
	env = append(env, "PATH="+os.Getenv("PATH"))

	w := &workspace{project: project}
	if err := w.setUp(context.Background(), fx, env); err != nil {
		t.Fatalf("setUp under an operator config that signs and hooks every commit: %v", err)
	}

	if got := readTrimmed(t, filepath.Join(project, "home.txt")); got != agentHome {
		t.Errorf("setup ran with HOME %q, want the agent's %q — it inherited the operator's environment", got, agentHome)
	}
	if got := readTrimmed(t, filepath.Join(project, "project.txt")); got != project {
		t.Errorf("SR_EVAL_PROJECT_DIR was %q, want %q", got, project)
	}
	if out, err := exec.Command("git", "-C", project, "status", "--porcelain").Output(); err != nil || strings.TrimSpace(string(out)) != "" {
		t.Errorf("the tree is not clean after setUp — what setup left uncommitted is not in the baseline: %q, %v", out, err)
	}
	if out, err := exec.Command("git", "-C", project, "ls-tree", "--name-only", "HEAD").Output(); err != nil || !strings.Contains(string(out), "project.txt") {
		t.Errorf("the baseline commit does not hold the setup's uncommitted file: %q, %v", out, err)
	}
}

func readTrimmed(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

// A setup without its execute bit is a load error, not a failure found after the
// workspace is built.
func TestLoadFixture_SetupMustBeExecutable(t *testing.T) {
	dir := newTestFixtureTree(t, false)
	mustWriteFile(t, filepath.Join(dir, "fixture.yaml"), "seed: seed\nmodel: haiku\nscore: score.sh\nsetup: setup.sh\n")
	mustWriteFile(t, filepath.Join(dir, "setup.sh"), "#!/bin/sh\nexit 0\n")
	if _, err := LoadFixture(dir); err == nil || !strings.Contains(err.Error(), "not an executable") {
		t.Fatalf("a setup without its execute bit must be a load error naming it, got %v", err)
	}
}

// disallowedTools reaches the harness as ONE comma-joined --disallowed-tools
// value, so a rule with a space of its own survives as one entry.
func TestAgentArgs_DisallowedToolsAreCommaJoined(t *testing.T) {
	got := claudeArgsOf(t, agentArgs("haiku", "fix it", "sid-1", false, []string{"WebSearch", "WebFetch", "Bash(gh search:*)"}))
	if got["disallowed-tools"] != "WebSearch,WebFetch,Bash(gh search:*)" {
		t.Fatalf("agent args do not carry the tools comma-joined: %v", got)
	}
	if got["permission-mode"] != "bypassPermissions" || got["settings"] != "{}" {
		t.Fatalf("agent args lost the isolation override: %v", got)
	}
	none := claudeArgsOf(t, agentArgs("haiku", "fix it", "sid-1", false, nil))
	if _, ok := none["disallowed-tools"]; ok {
		t.Fatalf("no disallowedTools must pass no --disallowed-tools: %v", none)
	}
}

// Each disallowedTools entry is one tool (optionally with a rule); an entry that
// would be split or merged on its way to the harness is a load error.
func TestLoadFixture_DisallowedToolsShape(t *testing.T) {
	for _, c := range []struct {
		entry string
		ok    bool
	}{
		{"WebSearch", true},
		{"Bash(gh search:*)", true},
		{"mcp__github__search_code", true},
		{"", false},
		{" WebSearch", false},
		{"WebSearch,WebFetch", false},
		{"Web Search", false},
		{"Bash(gh search:*,gh api:*)", false},
	} {
		dir := newTestFixtureTree(t, false)
		mustWriteFile(t, filepath.Join(dir, "fixture.yaml"),
			"seed: seed\nmodel: haiku\nscore: score.sh\ndisallowedTools: ["+yamlQuote(c.entry)+"]\n")
		_, err := LoadFixture(dir)
		if c.ok && err != nil {
			t.Errorf("disallowedTools %q must load: %v", c.entry, err)
		}
		if !c.ok && (err == nil || !strings.Contains(err.Error(), "disallowedTools")) {
			t.Errorf("disallowedTools %q must be a load error naming disallowedTools, got %v", c.entry, err)
		}
	}
}

func yamlQuote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"` }
