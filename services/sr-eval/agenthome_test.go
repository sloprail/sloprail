package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
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
