package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A fixture directory shaped like examples/<name>/eval/<case>/, with the
// shipped .sloprail/ two levels up, so exampleSloprailDir()'s path-shape
// assumption holds exactly as it does for a real fixture.
func newTestFixtureTree(t *testing.T, withOverlaySloprail bool) string {
	t.Helper()
	exampleRoot := t.TempDir()
	fixtureDir := filepath.Join(exampleRoot, "eval", "case")

	mustMkdirAll(t, filepath.Join(exampleRoot, ".sloprail", "gate", "some-rule"))
	mustWriteFile(t, filepath.Join(exampleRoot, ".sloprail", "gate", "some-rule", "gate.yaml"), "on: []\n")

	mustMkdirAll(t, fixtureDir)
	mustWriteFile(t, filepath.Join(fixtureDir, "prompt.md"), "do the thing\n")
	mustWriteFile(t, filepath.Join(fixtureDir, "score.sh"), "#!/bin/sh\nexit 0\n")
	mustMkdirAll(t, filepath.Join(fixtureDir, "seed"))

	overlayDir := filepath.Join(fixtureDir, "overlay")
	mustMkdirAll(t, overlayDir)
	if withOverlaySloprail {
		mustMkdirAll(t, filepath.Join(overlayDir, ".sloprail", "gate", "stale-copy"))
		mustWriteFile(t, filepath.Join(overlayDir, ".sloprail", "gate", "stale-copy", "gate.yaml"), "on: []\n")
	} else {
		// A non-empty overlay with something OTHER than .sloprail/ — a skill,
		// say — so Overlay is genuinely declared without tripping the check
		// it is not testing.
		mustMkdirAll(t, filepath.Join(overlayDir, ".claude", "skills", "some-skill"))
		mustWriteFile(t, filepath.Join(overlayDir, ".claude", "skills", "some-skill", "SKILL.md"), "---\nname: some-skill\n---\n")
	}

	fixtureYAML := "seed: seed\noverlay: overlay\nmodel: haiku\nscore: score.sh\nexampleSloprail: true\n"
	mustWriteFile(t, filepath.Join(fixtureDir, "fixture.yaml"), fixtureYAML)

	return fixtureDir
}

func mustMkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// exampleSloprail: true alongside a still-present overlay/.sloprail/ is
// exactly the duplication the field exists to remove, now silently doubled
// — LoadFixture must refuse rather than let it load.
func TestLoadFixture_ExampleSloprailWithOverlaySloprailRefuses(t *testing.T) {
	dir := newTestFixtureTree(t, true)

	_, err := LoadFixture(dir)
	if err == nil {
		t.Fatal("LoadFixture must refuse when exampleSloprail is true and overlay/.sloprail/ still exists, but it did not")
	}
	if got := err.Error(); !strings.Contains(got, "exampleSloprail") || !strings.Contains(got, ".sloprail still exists") {
		t.Fatalf("refusal must name the actual problem (exampleSloprail + stale overlay/.sloprail/), got: %s", got)
	}
}

// The same fixture, with overlay/.sloprail/ actually removed, loads cleanly
// — the check must not fire on a correctly-migrated fixture.
func TestLoadFixture_ExampleSloprailWithoutOverlaySloprailLoads(t *testing.T) {
	dir := newTestFixtureTree(t, false)

	fx, err := LoadFixture(dir)
	if err != nil {
		t.Fatalf("a correctly-migrated fixture (exampleSloprail: true, no overlay/.sloprail/) must load, got: %v", err)
	}
	if !fx.ExampleSloprail {
		t.Fatal("ExampleSloprail must be true, parsed from fixture.yaml")
	}
	if got := fx.ExampleSloprailDir(); got == "" {
		t.Fatal("ExampleSloprailDir() must resolve when ExampleSloprail is true")
	}
}

// setup names a script beside fixture.yaml; a missing one is a load error, and
// the script runs in the project with a git identity it can commit with.
func TestSetup_RunsInProjectAndMayCommit(t *testing.T) {
	dir := newTestFixtureTree(t, false)
	mustWriteFile(t, filepath.Join(dir, "fixture.yaml"), "seed: seed\nmodel: haiku\nscore: score.sh\nsetup: missing.sh\n")
	if _, err := LoadFixture(dir); err == nil || !strings.Contains(err.Error(), "setup") {
		t.Fatalf("a setup that does not exist must be a load error naming it, got %v", err)
	}

	mustWriteFile(t, filepath.Join(dir, "fixture.yaml"), "seed: seed\nmodel: haiku\nscore: score.sh\nsetup: setup.sh\n")
	mustWriteFile(t, filepath.Join(dir, "setup.sh"),
		"#!/bin/sh\nset -eu\necho x > a.txt\ngit add a.txt\ngit commit -q --no-gpg-sign -m a\npwd > where.txt\n")
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
	w := &workspace{project: project}
	if err := w.runSetup(context.Background(), fx, os.Environ()); err != nil {
		t.Fatalf("runSetup: %v", err)
	}
	where, err := os.ReadFile(filepath.Join(project, "where.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := filepath.EvalSymlinks(strings.TrimSpace(string(where))); got != mustEval(t, project) {
		t.Fatalf("setup ran in %q, want the project %q", got, project)
	}
	if out, err := exec.Command("git", "-C", project, "log", "--format=%an", "-1").Output(); err != nil || strings.TrimSpace(string(out)) != "sr-eval" {
		t.Fatalf("setup's commit must carry the sr-eval identity: %q, %v", out, err)
	}

	mustWriteFile(t, filepath.Join(dir, "setup.sh"), "#!/bin/sh\necho broke >&2\nexit 3\n")
	if err := w.runSetup(context.Background(), fx, os.Environ()); err == nil || !strings.Contains(err.Error(), "broke") {
		t.Fatalf("a failing setup must fail with its own output, got %v", err)
	}
}

func mustEval(t *testing.T, p string) string {
	t.Helper()
	got, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// plugins names further marketplace plugins by their bare name; sloprail itself
// is always installed, and a key with a marketplace or a path is refused.
func TestLoadFixture_Plugins(t *testing.T) {
	for name, tc := range map[string]struct {
		plugins string
		ok      bool
	}{
		"a marketplace plugin": {"[sloprail-tasks]", true},
		"sloprail itself":      {"[sloprail]", false},
		"a qualified key":      {"[sloprail-tasks@sloprail-marketplace]", false},
		"a path":               {"[../evil]", false},
		"an empty name":        {`[""]`, false},
	} {
		t.Run(name, func(t *testing.T) {
			dir := newTestFixtureTree(t, false)
			mustWriteFile(t, filepath.Join(dir, "fixture.yaml"),
				"seed: seed\nmodel: haiku\nscore: score.sh\nplugins: "+tc.plugins+"\n")
			fx, err := LoadFixture(dir)
			if tc.ok != (err == nil) {
				t.Fatalf("plugins %s: err = %v", tc.plugins, err)
			}
			if tc.ok && len(fx.Plugins) != 1 {
				t.Fatalf("plugins not parsed: %v", fx.Plugins)
			}
		})
	}
}
