package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/sloprail/sloprail/internal/harness"
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

	// Plugins names further plugins of this checkout's marketplace to install
	// for the project alongside sloprail, e.g. [sloprail-tasks] — the way a
	// user adds one, so a plugin's own guardrails are what fires. Each must be
	// listed in .claude-plugin/marketplace.json.
	Plugins []string `yaml:"plugins"`

	// Setup names a script, relative to Dir, run in the project once the seed
	// (or repo), example rules and overlay are in place and before the
	// baseline commit. It is for state a fixture cannot hold as files because
	// it depends on the run: the project's absolute path, or the sha of a
	// commit made in it (an sr:invariant marker pinned to SPEC.md needs both).
	// It may commit; whatever it leaves uncommitted lands in the baseline
	// commit. A failing setup fails the run before the agent starts.
	Setup string `yaml:"setup"`

	// DisallowedTools names harness tools the agent-under-test does not have,
	// e.g. [WebSearch, WebFetch] — passed as the harness's own
	// --disallowed-tools, comma-joined (agentArgs, user.go). Each entry is a tool name,
	// optionally with a rule in parentheses (`Bash(gh search:*)`); LoadFixture
	// refuses any other shape. For a fixture whose rule governs one way of doing a
	// thing (research through gh), in a project that offers no other: the
	// environment steers the agent, the prompt never has to.
	DisallowedTools []string `yaml:"disallowedTools"`

	// Harnesses names the harnesses this fixture can run under (claude, codex,
	// cursor); empty means all of them. A run under another is skipped with the reason,
	// not failed: a fixture that needs a Stop hook, say, cannot run where Stop never fires
	// (Cursor's fires only in the TUI, and an eval runs headless).
	Harnesses []string `yaml:"harnesses"`

	// Model is the sr-agent --model set for the agent-under-test, e.g.
	// "claude-sonnet-5,size-md". Empty lets sr-agent's own default resolve —
	// which sr-agent refuses rather than silently picking one, so this is
	// REQUIRED in practice; left as a normal field (not defaulted here) so
	// that refusal is sr-agent's, in one place, rather than duplicated here.
	// Overridable per run with `sr-eval run --model`, so the same fixture can
	// be run across several models without editing this file.
	Model string `yaml:"model"`

	// User, when set, makes the run MULTI-TURN: after the agent answers
	// prompt.md, a simulated user (another agent, launched the way the
	// scorer's judge is — isolated, no plugins, no sloprail hooks, a cheap
	// model) reads the conversation so far and writes the next user message,
	// which is fed to the agent-under-test in the SAME session (resumed), so
	// the transcript holds real, separate user turns. Absent, a run is one
	// turn, exactly as before.
	User *SimulatedUser `yaml:"user"`

	// Variants names the ways this one fixture can be prepared, e.g. the same
	// project with and without its rules, so a comparison does not need two
	// copies of a fixture. `sr-eval run --variant <name>` picks one; it must be
	// declared here. The name reaches the setup script and the score script as
	// SR_EVAL_VARIANT, and the setup script does the preparing: sr-eval itself
	// only reads what a Variant declares. Without --variant a run is the
	// fixture as written, and SR_EVAL_VARIANT is empty.
	Variants map[string]Variant `yaml:"variants"`

	// Variant is the variant this run was asked for ("" when none): not a YAML
	// field, set by the run from --variant (UseVariant).
	Variant string `yaml:"-"`

	// Score names the script, relative to Dir, that judges the run. It
	// receives the environment documented in score.go and reports pass/fail
	// by exit code, exactly like a guardrail script check — the same
	// fail-closed contract, so a scorer that cannot run is a failed eval, not
	// a silently-skipped one.
	Score string `yaml:"score"`
}

// Variant is one way of preparing a fixture. See Fixture.Variants.
type Variant struct {
	// NoSloprail, when true, runs the agent-under-test with no sloprail at
	// all: the plugin is not installed (nor any of Plugins), and the example's
	// shipped rules are not applied even when ExampleSloprail is set. It is
	// the control of a with-and-without comparison. An overlay is still
	// applied as it is; removing rules an overlay carries is the setup
	// script's job.
	NoSloprail bool `yaml:"noSloprail"`
}

// UseVariant returns the fixture set to run as the variant name. An empty
// name is the fixture as written; any other must be declared in Variants.
func (f Fixture) UseVariant(name string) (Fixture, error) {
	if name == "" {
		return f, nil
	}
	if _, ok := f.Variants[name]; !ok {
		declared := make([]string, 0, len(f.Variants))
		for v := range f.Variants {
			declared = append(declared, v)
		}
		slices.Sort(declared)
		if len(declared) == 0 {
			return Fixture{}, fmt.Errorf("%s/fixture.yaml declares no variants, so --variant %q names nothing", f.Dir, name)
		}
		return Fixture{}, fmt.Errorf("%s/fixture.yaml has no variant %q — it declares %s", f.Dir, name, strings.Join(declared, ", "))
	}
	f.Variant = name
	return f, nil
}

// NoSloprail reports whether this run's variant runs without sloprail.
func (f Fixture) NoSloprail() bool { return f.Variants[f.Variant].NoSloprail }

// SimulatedUser configures the agent that plays the user after the first
// turn. See Fixture.User.
type SimulatedUser struct {
	// Brief names a file, relative to Dir, telling the simulated user who it
	// is and what it wants — e.g. "you asked for the bug fix; when asked
	// whether to commit, say yes". A file for the same reason prompt.md is
	// one: it is prose, read by a reviewer exactly as the model reads it.
	Brief string `yaml:"brief"`

	// MaxTurns caps the USER turns in the whole run, prompt.md included, so a
	// simulated user that never says it is done cannot run the eval forever.
	// Required, between 2 (one simulated reply) and maxUserTurns.
	MaxTurns int `yaml:"maxTurns"`

	// Model is the sr-agent --model set for the simulated user. Empty is
	// size-sm: playing a user from a short brief is not a hard judgment
	// call, the same reasoning the trajectory-health judge's model follows.
	Model string `yaml:"model"`
}

// maxUserTurns is the ceiling on SimulatedUser.MaxTurns — a bound on what one
// run can spend, not a number any current fixture comes near.
const maxUserTurns = 10

// defaultUserModel is SimulatedUser.Model's default.
const defaultUserModel = "size-sm"

// UserModel is the model set the simulated user runs on.
func (u SimulatedUser) UserModel() string {
	if u.Model == "" {
		return defaultUserModel
	}
	return u.Model
}

// promptPath is prompt.md beside fixture.yaml — the exact words the
// agent-under-test receives, unmodified by sr-eval. Kept as its own file
// rather than a YAML field so what the agent reads is exactly what a reviewer
// reads with no YAML-escaping between them.
func (f Fixture) promptPath() string { return filepath.Join(f.Dir, "prompt.md") }

// Supports reports whether the fixture can run under the harness; Harnesses empty is all.
func (f Fixture) Supports(id string) bool {
	return len(f.Harnesses) == 0 || slices.Contains(f.Harnesses, id)
}

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
	for _, id := range f.Harnesses {
		if _, ok := harness.Lookup(id); !ok {
			return Fixture{}, fmt.Errorf("%s/fixture.yaml: harnesses names %q — one of %s", abs, id, strings.Join(harness.Names(), ", "))
		}
	}
	for _, p := range f.Plugins {
		if p == "" || p == pluginName || strings.ContainsAny(p, "@/ ") {
			return Fixture{}, fmt.Errorf("%s/fixture.yaml: plugins names %q — a plugin of this checkout's marketplace other than %s, by its bare name", abs, p, pluginName)
		}
	}
	if _, err := os.Stat(filepath.Join(abs, f.Score)); err != nil {
		return Fixture{}, fmt.Errorf("%s/fixture.yaml: score %q: %w", abs, f.Score, err)
	}
	if f.User != nil {
		if f.User.Brief == "" {
			return Fixture{}, fmt.Errorf("%s/fixture.yaml: user.brief is required — the simulated user needs to be told who it is", abs)
		}
		if _, err := os.Stat(filepath.Join(abs, f.User.Brief)); err != nil {
			return Fixture{}, fmt.Errorf("%s/fixture.yaml: user.brief %q: %w", abs, f.User.Brief, err)
		}
		if f.User.MaxTurns < 2 || f.User.MaxTurns > maxUserTurns {
			return Fixture{}, fmt.Errorf("%s/fixture.yaml: user.maxTurns is %d — it counts every user turn including prompt.md, so it must be between 2 and %d", abs, f.User.MaxTurns, maxUserTurns)
		}
	}
	if f.Setup != "" {
		info, err := os.Stat(filepath.Join(abs, f.Setup))
		if err != nil {
			return Fixture{}, fmt.Errorf("%s/fixture.yaml: setup %q: %w", abs, f.Setup, err)
		}
		// Checked here, not left to the run: a setup without its execute bit
		// fails only after the workspace is built and the release compiled.
		if info.IsDir() || info.Mode().Perm()&0o111 == 0 {
			return Fixture{}, fmt.Errorf("%s/fixture.yaml: setup %q is not an executable file (chmod +x it)", abs, f.Setup)
		}
	}
	for _, tool := range f.DisallowedTools {
		if !toolRulePattern.MatchString(tool) {
			return Fixture{}, fmt.Errorf("%s/fixture.yaml: disallowedTools names %q — each entry is one tool name, optionally with a rule in parentheses (WebSearch, Bash(gh search:*)), with no commas or spaces outside them", abs, tool)
		}
	}

	return f, nil
}

// UserBrief returns the simulated user's brief. Empty when the fixture is
// single-turn.
func (f Fixture) UserBrief() (string, error) {
	if f.User == nil {
		return "", nil
	}
	p := filepath.Join(f.Dir, f.User.Brief)
	body, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", p, err)
	}
	return string(body), nil
}

// toolRulePattern is the shape of one disallowedTools entry: a tool name, and
// optionally a rule in parentheses. It is what keeps an entry from being split
// or merged on its way to the harness (the list is joined with commas; see
// agentArgs in user.go) — an empty entry, a stray space, two names in one, or
// a comma inside a rule's parentheses (which the join would split) would each
// remove something other than what the fixture says. It cannot tell a typo in
// a tool's name from a tool this harness has.
var toolRulePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*(\([^(),]*\))?$`)

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
	if f.NoSloprail() {
		return ""
	}
	if !f.ExampleSloprail {
		return ""
	}
	return f.exampleSloprailDir()
}
