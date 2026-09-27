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

	// ExampleSloprail, when true, copies the SHIPPED example's own .sloprail/
	// (examples/<name>/.sloprail/, two directories up from this fixture) into
	// the project BEFORE Overlay is applied — so a fixture proving the exact
	// shipped guardrail, unmodified, does not need its own byte-identical
	// copy of it under overlay/.sloprail/ (measured: ~13 fixtures were
	// carrying exactly that copy with zero delta from the shipped file,
	// pure duplication a change to the shipped guardrail would silently not
	// reach). Overlay is still applied on top and wins on any path collision
	// — a fixture proving a VARIANT or an unrelated new rule (a different
	// guardrail entirely, not a copy) leaves this false and supplies its own
	// overlay/.sloprail/ exactly as today, with no inherited shipped rules
	// at all.
	ExampleSloprail bool `yaml:"exampleSloprail"`

	// FreshMachine, when true, runs the agent-under-test as a newcomer who has
	// just run `/plugin install` and nothing else: the plugin is installed and
	// enabled for the project, but the agent gets a HOME of its own (see
	// workspace.freshHome) with no sr* binaries anywhere it or the plugin's
	// hook wrapper would look. The machine's own credentials (Claude Code's
	// login, gh, SSH, git identity) still work. Getting the binaries on is the
	// plugin's own job, at its first session start.
	//
	// The "release" they come from is this checkout, built: install.sh's
	// SLOPRAIL_RELEASE_URL points at archives freshHome builds, so what lands
	// is the code under test rather than whatever was last published.
	FreshMachine bool `yaml:"freshMachine"`

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
	if f.ExampleSloprail {
		if _, err := os.Stat(f.exampleSloprailDir()); err != nil {
			return Fixture{}, fmt.Errorf("%s/fixture.yaml: exampleSloprail is true but %s: %w", abs, f.exampleSloprailDir(), err)
		}
		// The whole point of exampleSloprail is that the fixture stops
		// carrying its own copy of the shipped .sloprail/ — a fixture that
		// sets the flag AND still has overlay/.sloprail/ is exactly the
		// duplication this field exists to remove, now silently doubled
		// instead: the shipped copy applies first, then the overlay's stale
		// copy applies on top and wins the collision, so a fix to the
		// shipped guardrail would stop reaching this fixture and nobody
		// would notice. Refuse to load rather than let that drift back in.
		if f.Overlay != "" {
			if _, err := os.Stat(filepath.Join(f.OverlayDir(), ".sloprail")); err == nil {
				return Fixture{}, fmt.Errorf("%s/fixture.yaml: exampleSloprail is true but %s/.sloprail still exists — delete it, the shipped example's .sloprail/ already covers it", abs, f.OverlayDir())
			}
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

// exampleSloprailDir is the shipped example's own .sloprail/ —
// examples/<name>/.sloprail/, two directories up from a fixture at
// examples/<name>/eval/<case>/ (Dir). Computed by path shape, not read from
// anywhere else, since a fixture always lives at exactly that depth.
func (f Fixture) exampleSloprailDir() string {
	return filepath.Join(f.Dir, "..", "..", ".sloprail")
}

// ExampleSloprailDir is exampleSloprailDir, exported for newWorkspace. Empty
// when the fixture does not declare ExampleSloprail.
//
// copyTree copies a source's CONTENTS into the destination (the convention
// OverlayDir relies on: overlay/ contains .sloprail/, .claude/, etc. as
// children, so copying overlay/'s contents into project/ correctly places
// project/.sloprail/). This path is .sloprail/ itself, so newWorkspace must
// NOT copyTree it straight into project/ — that would flatten its own
// children (file-guard/, gate/, …) into the project ROOT instead of under
// project/.sloprail/. newWorkspace copies it to a project/.sloprail/
// destination explicitly instead of reusing the plain copyTree(src, project)
// call Overlay uses.
func (f Fixture) ExampleSloprailDir() string {
	if !f.ExampleSloprail {
		return ""
	}
	return f.exampleSloprailDir()
}
