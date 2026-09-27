package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestLoadFixture_FreshMachineParses(t *testing.T) {
	dir := newTestFixtureTree(t, false)
	mustWriteFile(t, filepath.Join(dir, "fixture.yaml"),
		"seed: seed\nmodel: haiku\nscore: score.sh\nfreshMachine: true\n")

	fx, err := LoadFixture(dir)
	if err != nil || !fx.FreshMachine {
		t.Fatalf("freshMachine must parse; got fresh=%v, err=%v", fx.FreshMachine, err)
	}
}

// freshPath drops every directory holding a sloprail binary — the property a
// fresh machine rests on — and, when that also drops the harness binary
// (claude and sr share ~/.local/bin on a real install), puts a link to it
// back first so the agent can still start.
func TestFreshPath_DropsSloprailKeepsHarness(t *testing.T) {
	shared := t.TempDir() // claude AND sr-session, like ~/.local/bin
	other := t.TempDir()
	mustWriteFile(t, filepath.Join(shared, "sr-session"), "")
	mustWriteFile(t, filepath.Join(shared, "claude"), "#!/bin/sh\n")
	if err := os.Chmod(filepath.Join(shared, "claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shared+string(os.PathListSeparator)+other)

	home := t.TempDir()
	got, err := freshPath(home)
	if err != nil {
		t.Fatal(err)
	}
	dirs := filepath.SplitList(got)
	for _, d := range dirs {
		if d == shared {
			t.Fatalf("a directory holding sr-session stayed on PATH: %s", got)
		}
	}
	if len(dirs) != 2 || dirs[1] != other {
		t.Fatalf("want [shim, %s], got %v", other, dirs)
	}
	if target, err := os.Readlink(filepath.Join(dirs[0], "claude")); err != nil || target != filepath.Join(shared, "claude") {
		t.Fatalf("the harness binary must be linked back first, got %q, %v", target, err)
	}
}

// TestBuildRelease_StagedBinariesSurvive pins the regression this fixed: the
// staged sloprail-<os>-<arch>/ directory buildRelease builds into must still
// exist once it returns, with every sr* binary directly inside it — an
// ordinary (non-FreshMachine) run's agentHome hands this exact directory back
// as agentEnv.binDir for the HOST to launch sr-agent through, and for a score
// script's SR_EVAL_BIN_DIR. buildRelease used to delete this directory right
// after archiving it (correct while only the FreshMachine path, which only
// needs the .tar.gz, called it); reusing it for an ordinary run's own launch
// broke the moment that directory was gone — "fork/exec ...: no such file or
// directory", measured on a real run.
func TestBuildRelease_StagedBinariesSurvive(t *testing.T) {
	if testing.Short() {
		t.Skip("builds every sr* binary from source; slow")
	}
	root, err := repoRoot()
	if err != nil {
		t.Skipf("not inside a sloprail checkout: %v", err)
	}

	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := buildRelease(ctx, root, dir); err != nil {
		t.Fatalf("buildRelease: %v", err)
	}

	stage := filepath.Join(dir, "sloprail-"+runtime.GOOS+"-"+runtime.GOARCH)
	for _, name := range sloprailBinaries {
		p := filepath.Join(stage, name)
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("%s must survive buildRelease, and be directly inside the staged dir: %v", p, err)
		}
		if info.Mode().Perm()&0o100 == 0 {
			t.Fatalf("%s exists but is not executable: %v", p, info.Mode())
		}
	}
}

// baseAgentEnv moves every temp root the agent can reach through its
// environment into the workspace — TMPDIR for its tools, CLAUDE_CODE_TMPDIR for
// Claude Code's own per-uid directory (the scratchpad), which on macOS ignores
// TMPDIR — and drops the caller's own values rather than leaving two.
func TestBaseAgentEnv_TempRootsLandInTheWorkspace(t *testing.T) {
	environ := []string{
		"HOME=/Users/op", "TMPDIR=/var/folders/op/T/", "CLAUDE_CODE_TMPDIR=/tmp/op",
		"PATH=/usr/bin", "KEEP=1",
	}
	got := baseAgentEnv(environ, "/ws/home", "/ws/tmp", false)

	want := map[string]string{
		"HOME": "/ws/home", "TMPDIR": "/ws/tmp", "CLAUDE_CODE_TMPDIR": "/ws/tmp", "KEEP": "1",
	}
	seen := map[string]int{}
	for _, kv := range got {
		key, val, _ := strings.Cut(kv, "=")
		seen[key]++
		if w, ok := want[key]; ok && val != w {
			t.Errorf("%s=%s, want %s", key, val, w)
		}
	}
	for key := range want {
		if seen[key] != 1 {
			t.Errorf("%s appears %d times, want exactly once: %v", key, seen[key], got)
		}
	}
	if seen["PATH"] != 0 {
		t.Errorf("PATH is the caller's to set, but baseAgentEnv kept one: %v", got)
	}
}

// A fresh machine also drops what would reach an existing sloprail install.
func TestBaseAgentEnv_FreshDropsInstallPointers(t *testing.T) {
	got := baseAgentEnv([]string{"GOPATH=/go", "SLOPRAIL_X=1", "KEEP=1"}, "/h", "/t", true)
	for _, kv := range got {
		if strings.HasPrefix(kv, "GOPATH=") || strings.HasPrefix(kv, "SLOPRAIL_") {
			t.Errorf("a fresh machine kept %s", kv)
		}
	}
}
