package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A follow-up turn names a file; one that does not exist is a load error, the
// same as a missing prompt.md, not a turn silently dropped from the run.
func TestLoadFixture_MissingFollowUpRefuses(t *testing.T) {
	dir := newTestFixtureTree(t, false)
	mustWriteFile(t, filepath.Join(dir, "fixture.yaml"),
		"seed: seed\nmodel: haiku\nscore: score.sh\nfreshMachine: true\nfollowUps: [turn-2.md]\n")

	_, err := LoadFixture(dir)
	if err == nil || !strings.Contains(err.Error(), "turn-2.md") {
		t.Fatalf("a missing follow-up must refuse, naming it; got: %v", err)
	}

	mustWriteFile(t, filepath.Join(dir, "turn-2.md"), "now the task\n")
	fx, err := LoadFixture(dir)
	if err != nil {
		t.Fatalf("with the follow-up present the fixture must load, got: %v", err)
	}
	turns, err := fx.FollowUpPrompts()
	if err != nil || len(turns) != 1 || turns[0] != "now the task\n" || !fx.FreshMachine {
		t.Fatalf("follow-ups must read back verbatim, freshMachine parsed; got %q, %v, fresh=%v", turns, err, fx.FreshMachine)
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
