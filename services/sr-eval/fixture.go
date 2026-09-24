package main

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Fixture is one evaluation case: what to seed, what to ask, how to score.
//
// It is read from <dir>/fixture.yaml. The prompt and the scorer are separate
// files (prompt.md, score.sh) rather than inline YAML strings, because both
// want to be read and edited as what they are — prose and a script — not as a
// YAML block scalar indentation puzzle.
type Fixture struct {
	// Dir is the fixture directory — not a YAML field, filled by Load.
	Dir string `yaml:"-"`

	// Description is one line saying what this fixture proves. Shown in
	// output; not read by the agent-under-test.
	Description string `yaml:"description"`

	// Repo, when set, is a real git remote the agent-under-test runs against
	// — a genuinely large, noisy tree that gives a required skill somewhere
	// to hide. Exclusive with Seed. Cloned at Ref (a commit SHA; a branch
	// name is not pinned and makes a run unreproducible) and never pushed to
	// — the agent's work lands in a local clone that is discarded after
	// scoring.
	Repo string `yaml:"repo"`

	// Ref is the commit the fixture is pinned to — required whenever Repo is
	// set, for the same reason a10n-eval's own fixtures pin one: a fixture
	// that floats with a branch's HEAD stops being the same eval from one run
	// to the next, and a regression the fixture was written to catch can
	// disappear from under it without anyone changing this file.
	Ref string `yaml:"ref"`

	// Seed is a directory, relative to Dir, copied into the isolated project
	// before the agent runs, in place of Repo — for a fixture small enough
	// to be worth committing whole rather than pinning to an external
	// remote. Exclusive with Repo.
	Seed string `yaml:"seed"`

	// Overlay is a directory, relative to Dir, copied on TOP of Repo or Seed
	// after it is in place — the .sloprail/ rule(s) under test, the skill(s)
	// they require, and anything else the fixture adds that the base tree
	// does not already have. Optional: a fixture using Seed usually has no
	// need of it, since Seed already IS the whole tree the fixture wants.
	Overlay string `yaml:"overlay"`

	// Model is the sr-agent --model set for the agent-under-test, e.g.
	// "claude-sonnet-5,size-md". Empty lets sr-agent's own default resolve —
	// which sr-agent refuses rather than silently picking one, so this is
	// REQUIRED in practice; left as a normal field (not defaulted here) so
	// that refusal is sr-agent's, in one place, rather than duplicated here.
	// Overridable per run with `sr-eval run --model`, so the same fixture can
	// be run across several models without editing this file.
	Model string `yaml:"model"`

	// Score names the script, relative to Dir, that judges the run. It
	// receives the environment documented in score.go and reports pass/fail
	// by exit code, exactly like a guardrail script check — the same
	// fail-closed contract, so a scorer that cannot run is a failed eval, not
	// a silently-skipped one.
	Score string `yaml:"score"`
}

// promptPath is prompt.md beside fixture.yaml — the exact words the
// agent-under-test receives, unmodified by sr-eval. Kept as its own file
// rather than a YAML field so what the agent reads is exactly what a reviewer
// reads with no YAML-escaping between them.
func (f Fixture) promptPath() string { return filepath.Join(f.Dir, "prompt.md") }

// LoadFixture reads and validates a fixture directory.
func LoadFixture(dir string) (Fixture, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Fixture{}, fmt.Errorf("resolve fixture dir %s: %w", dir, err)
	}

	body, err := os.ReadFile(filepath.Join(abs, "fixture.yaml"))
	if err != nil {
		return Fixture{}, fmt.Errorf("read %s/fixture.yaml: %w", abs, err)
	}

	var f Fixture
	if err := yaml.Unmarshal(body, &f); err != nil {
		return Fixture{}, fmt.Errorf("parse %s/fixture.yaml: %w", abs, err)
	}
	f.Dir = abs

	if (f.Repo == "") == (f.Seed == "") {
		return Fixture{}, fmt.Errorf("%s/fixture.yaml: exactly one of repo or seed is required", abs)
	}
	if f.Repo != "" && f.Ref == "" {
		return Fixture{}, fmt.Errorf("%s/fixture.yaml: ref is required alongside repo — a fixture pinned to a moving branch is not the same eval from one run to the next", abs)
	}
	if f.Score == "" {
		return Fixture{}, fmt.Errorf("%s/fixture.yaml: score is required", abs)
	}
	if _, err := os.Stat(f.promptPath()); err != nil {
		return Fixture{}, fmt.Errorf("%s: prompt.md is required beside fixture.yaml: %w", abs, err)
	}
	if f.Seed != "" {
		if _, err := os.Stat(filepath.Join(abs, f.Seed)); err != nil {
			return Fixture{}, fmt.Errorf("%s/fixture.yaml: seed %q: %w", abs, f.Seed, err)
		}
	}
	if f.Overlay != "" {
		if _, err := os.Stat(filepath.Join(abs, f.Overlay)); err != nil {
			return Fixture{}, fmt.Errorf("%s/fixture.yaml: overlay %q: %w", abs, f.Overlay, err)
		}
	}
	if _, err := os.Stat(filepath.Join(abs, f.Score)); err != nil {
		return Fixture{}, fmt.Errorf("%s/fixture.yaml: score %q: %w", abs, f.Score, err)
	}

	return f, nil
}

// Prompt returns the exact text handed to the agent-under-test.
func (f Fixture) Prompt() (string, error) {
	body, err := os.ReadFile(f.promptPath())
	if err != nil {
		return "", fmt.Errorf("read %s: %w", f.promptPath(), err)
	}
	return string(body), nil
}

// SeedDir is the absolute path to the local tree copied into the isolated
// project. Empty when the fixture uses Repo instead.
func (f Fixture) SeedDir() string {
	if f.Seed == "" {
		return ""
	}
	return filepath.Join(f.Dir, f.Seed)
}

// OverlayDir is the absolute path to the tree copied on top of the base
// (Repo or Seed) once it is in place. Empty when the fixture declares none.
func (f Fixture) OverlayDir() string {
	if f.Overlay == "" {
		return ""
	}
	return filepath.Join(f.Dir, f.Overlay)
}

// ScorePath is the absolute path to the scorer script.
func (f Fixture) ScorePath() string { return filepath.Join(f.Dir, f.Score) }
