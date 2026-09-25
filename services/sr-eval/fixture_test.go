package main

import (
	"os"
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
