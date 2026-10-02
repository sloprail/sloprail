package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func initProject(t *testing.T) string {
	t.Helper()
	project := t.TempDir()
	gitOut(t, project, "init", "--quiet")
	return project
}

// The seed and the rules are two commits, the seed first. A rule's range starts
// at the parent of the commit that added its folder: one commit holding both
// made that the empty tree and every seeded file read as the agent's own.
func TestCommitSetup_SeedThenRulesAlone(t *testing.T) {
	project := initProject(t)
	writeIn(t, filepath.Join(project, "CHANGELOG.md"), "# changes\n")
	writeIn(t, filepath.Join(project, ".claude", "settings.json"), "{}\n")
	writeIn(t, filepath.Join(project, ".sloprail", "gate", "g", "gate.yaml"), "name: g\n")

	w := &workspace{root: t.TempDir(), project: project}
	if err := w.commitSetup(); err != nil {
		t.Fatal(err)
	}

	if n := gitOut(t, project, "rev-list", "--count", "HEAD"); n != "2" {
		t.Fatalf("want two commits (seed, then rules), got %s", n)
	}
	rules := strings.Fields(gitOut(t, project, "diff-tree", "--no-commit-id", "--name-only", "-r", "HEAD"))
	if len(rules) != 1 || rules[0] != ".sloprail/gate/g/gate.yaml" {
		t.Errorf("the second commit must hold .sloprail alone, got %v", rules)
	}
	seed := gitOut(t, project, "ls-tree", "-r", "--name-only", "HEAD~1")
	if strings.Contains(seed, ".sloprail") || !strings.Contains(seed, "CHANGELOG.md") || !strings.Contains(seed, ".claude/settings.json") {
		t.Errorf("the first commit must hold the seed and settings but not the rules, got:\n%s", seed)
	}
	if st := gitOut(t, project, "status", "--porcelain"); st != "" {
		t.Errorf("the tree must be clean, got %q", st)
	}
	if w.rulesCommit != gitOut(t, project, "rev-parse", "HEAD") || w.seedCommit != gitOut(t, project, "rev-parse", "HEAD~1") {
		t.Errorf("the setup commits' shas must be recorded for the scorer: seed %q rules %q", w.seedCommit, w.rulesCommit)
	}
}

// A fixture that is only rules still has a seed commit to be the rules' parent.
func TestCommitSetup_OnlyRulesStillGetsASeedCommit(t *testing.T) {
	project := initProject(t)
	writeIn(t, filepath.Join(project, ".sloprail", "context", "c", "context.yaml"), "name: c\n")

	w := &workspace{root: t.TempDir(), project: project}
	if err := w.commitSetup(); err != nil {
		t.Fatal(err)
	}
	if n := gitOut(t, project, "rev-list", "--count", "HEAD"); n != "2" {
		t.Fatalf("want an (empty) seed commit and the rules commit, got %s commits", n)
	}
	if got := gitOut(t, project, "ls-tree", "-r", "--name-only", "HEAD~1"); got != "" {
		t.Errorf("the seed commit must be empty, got %q", got)
	}
}

// No rules at all: one commit, no empty second one.
func TestCommitSetup_NoRulesIsOneCommit(t *testing.T) {
	project := initProject(t)
	writeIn(t, filepath.Join(project, "a.txt"), "a\n")
	w := &workspace{root: t.TempDir(), project: project}
	if err := w.commitSetup(); err != nil {
		t.Fatal(err)
	}
	if n := gitOut(t, project, "rev-list", "--count", "HEAD"); n != "1" {
		t.Fatalf("want one commit, got %s", n)
	}
}

// A setup.sh that commits part of the seed itself (goodwill-refund's does)
// keeps working: what it committed stays below, what it left (the overlay's
// rules included) is still split, seed first.
func TestSetUp_SetupThatCommitsStillGetsRulesLast(t *testing.T) {
	dir := newTestFixtureTree(t, false)
	mustWriteFile(t, filepath.Join(dir, "fixture.yaml"), "seed: seed\nmodel: haiku\nscore: score.sh\nsetup: setup.sh\n")
	mustWriteFile(t, filepath.Join(dir, "setup.sh"), "#!/bin/sh\nset -eu\n"+
		"echo spec > SPEC.md\ngit add SPEC.md\ngit commit -q --no-gpg-sign -m 'billing invariants'\n"+
		"echo code > charge.go\n")
	if err := os.Chmod(filepath.Join(dir, "setup.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	fx, err := LoadFixture(dir)
	if err != nil {
		t.Fatal(err)
	}
	project := initProject(t)
	writeIn(t, filepath.Join(project, ".sloprail", "gate", "g", "gate.yaml"), "name: g\n")
	env := append(baseAgentEnv(os.Environ(), t.TempDir(), t.TempDir(), false), "PATH="+os.Getenv("PATH"))

	w := &workspace{root: t.TempDir(), project: project}
	if err := w.setUp(context.Background(), fx, env); err != nil {
		t.Fatal(err)
	}

	if got := gitOut(t, project, "log", "--format=%s"); got != "sr-eval: install the rules (.sloprail)\nsr-eval: seed and overlay (project files, .claude/settings.json)\nbilling invariants" {
		t.Errorf("unexpected history:\n%s", got)
	}
	if st := gitOut(t, project, "status", "--porcelain"); st != "" {
		t.Errorf("the tree must be clean, got %q", st)
	}
	if got := gitOut(t, project, "ls-tree", "-r", "--name-only", "HEAD~1"); strings.Contains(got, ".sloprail") || !strings.Contains(got, "charge.go") {
		t.Errorf("seed commit wrong:\n%s", got)
	}
}

// install.sh's copy loop and sloprailBinaries must name the same binaries:
// buildRelease stages only what the list holds, and install.sh dies on `cp`
// for any it names that was not staged (sr-checks was missing, and sloprail
// never ran in a fresh-machine run).
func TestSloprailBinaries_CoverEverythingInstallShCopies(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Skipf("not inside a sloprail checkout: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(root, "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^for bin in ([^;]+); do$`).FindSubmatch(body)
	if m == nil {
		t.Fatal("install.sh has no `for bin in ...; do` loop; update this test with it")
	}
	want := strings.Fields(string(m[1]))
	have := map[string]bool{}
	for _, b := range sloprailBinaries {
		have[b] = true
	}
	for _, b := range want {
		if !have[b] {
			t.Errorf("install.sh copies %s but sloprailBinaries does not stage it", b)
		}
		delete(have, b)
	}
	for b := range have {
		t.Errorf("sloprailBinaries stages %s but install.sh does not copy it", b)
	}
	for _, b := range sloprailBinaries {
		if _, err := os.Stat(filepath.Join(root, "services", b)); err != nil {
			t.Errorf("sloprailBinaries names %s with no services/%s to build: %v", b, b, err)
		}
	}
}

func TestCheckFreeSpace(t *testing.T) {
	free := func(n uint64) func(string) (uint64, error) {
		return func(string) (uint64, error) { return n, nil }
	}
	if err := checkFreeSpace("/x", 3<<30, free(2<<30)); err == nil || !strings.Contains(err.Error(), "not enough free disk space") || !strings.Contains(err.Error(), minFreeMBEnv) {
		t.Errorf("below the threshold must refuse and say how to lower it, got %v", err)
	}
	if err := checkFreeSpace("/x", 3<<30, free(3<<30)); err != nil {
		t.Errorf("at the threshold must pass, got %v", err)
	}
	if err := checkFreeSpace("/x", 0, free(0)); err != nil {
		t.Errorf("a zero threshold disables the check, got %v", err)
	}
	if err := checkFreeSpace("/x", 1, func(string) (uint64, error) { return 0, errors.New("boom") }); err == nil {
		t.Error("an unmeasurable volume must refuse")
	}
	env := map[string]string{minFreeMBEnv: "10"}
	if got := minFreeBytes(func(k string) string { return env[k] }); got != 10<<20 {
		t.Errorf("override ignored: %d", got)
	}
	if got := minFreeBytes(func(string) string { return "" }); got != defaultMinFreeMB<<20 {
		t.Errorf("default wrong: %d", got)
	}
}

// A run that cannot start exits 2 with the reason on stdout AND stderr —
// never a silent exit that reads like a failed eval.
func TestRun_EarlyFailureIsExit2AndLoud(t *testing.T) {
	t.Setenv(minFreeMBEnv, "999999999")
	var stdout, stderr bytes.Buffer
	cmd := newRoot()
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"run", "--fixture", t.TempDir()})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("a run below the free-space bar must fail")
	}
	if code := exitCode(err); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(err.Error(), "not enough free disk space") {
		t.Errorf("the returned error (printed to stderr by main) must say why: %v", err)
	}
	if !strings.Contains(stdout.String(), "not enough free disk space") {
		t.Errorf("stdout must say why too: %q", stdout.String())
	}
}

// Any other failure before there is a transcript — here a fixture that does not
// load — is exit 2 as well, and says so.
func TestRun_UnloadableFixtureIsExit2(t *testing.T) {
	t.Setenv(minFreeMBEnv, "0")
	var stdout bytes.Buffer
	cmd := newRoot()
	cmd.SetOut(&stdout)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"run", "--fixture", t.TempDir()})
	err := cmd.Execute()
	if err == nil || exitCode(err) != 2 || !strings.Contains(err.Error(), "could not be completed") {
		t.Fatalf("want exit 2 with an explanation, got %v (code %d)", err, exitCode(err))
	}
}

// writeIn writes a file, creating its directories.
func writeIn(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, path, content)
}
