package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Harness is one agent CLI sr-agent knows how to run.
//
// It is a string rather than an enum because it is also what `--harness` takes
// from a caller, and a caller naming an unsupported one must get back the name
// it typed in the refusal. An enum would have had to map the unknown case to a
// zero value and lose it.
type Harness string

// ClaudeCode is the only harness supported today. Codex and Cursor are separate
// tasks; what makes them cheap is that everything harness-shaped in this binary
// is reached through the registry below rather than written inline.
const ClaudeCode Harness = "claude-code"

// SizeAlias is one of the harness-agnostic sizes a model set may name.
//
// Spelled `size-md` rather than bare `md`, and the prefix is load-bearing: a
// harness could ship a model literally called `md`, and without the prefix an
// entry could not be classified without knowing every harness's catalogue.
// With it, an entry says which kind it is on sight — which is what lets
// classification be a pure function of the string, with no catalogue lookup and
// no ambiguity for a reader either.
type SizeAlias string

// The six sizes. This is the whole set: an alias outside it is not a size that
// resolves differently, it is a concrete model name that happens to start with
// `size-`, and it is treated as one.
const (
	SizeXS  SizeAlias = "size-xs"
	SizeSM  SizeAlias = "size-sm"
	SizeMD  SizeAlias = "size-md"
	SizeLG  SizeAlias = "size-lg"
	SizeXL  SizeAlias = "size-xl"
	SizeXXL SizeAlias = "size-xxl"
)

// sizeAliases is every valid alias, in ascending order of size. The order is
// what `--help` and the diagnostics print; nothing resolves by index.
var sizeAliases = []SizeAlias{SizeXS, SizeSM, SizeMD, SizeLG, SizeXL, SizeXXL}

// harnessSpec is everything that differs between one harness and the next.
//
// Every harness-specific fact lives in one of these values rather than in a
// switch somewhere, so supporting Codex is writing another value and not
// finding every place a harness is named.
type harnessSpec struct {
	// name is the harness as `--harness` spells it.
	name Harness

	// binary is the executable to run.
	binary string

	// detect reports whether an environment names this harness. It is given a
	// lookup rather than reading os.Getenv itself, so detection is testable
	// without mutating the process environment — which matters under -race,
	// where a test setting a variable races every other test reading one.
	detect func(getenv func(string) string) bool

	// sizes maps every alias to a model this harness offers. Every alias must
	// be present: an alias that did not resolve would break the property the
	// whole design rests on, and aliasesComplete pins that at startup.
	sizes map[SizeAlias]string

	// offers reports whether this harness has a model by that exact name. This
	// is what makes a concrete entry harness-specific — the same string is a
	// match under one harness and a skip under another.
	offers func(model string) bool

	// argsFlag is the `--<harness>-args` flag carrying this harness's own
	// settings. Named here so that passing one harness's flag while another is
	// running can be reported precisely rather than generically.
	argsFlag string

	// baseArgs are harness-specific arguments sr-agent ALWAYS passes this harness,
	// ahead of any caller-supplied ones — the settings a run needs whatever the
	// caller asked, so a caller (a judge check, most of all) does not have to know
	// them. For Claude Code this is the isolation `--settings` that keeps the
	// launched agent from re-triggering the very hooks/plugins/mcp servers that
	// launched it; a rule firing on RULE.md whose judge is itself an agent would
	// otherwise recurse. nil when the harness needs none.
	baseArgs []string

	// grantWrite returns the arguments that let the agent write into a
	// directory outside the one it was started in, or nil if the harness needs
	// no such permission. `extraTools` are the caller's own requested tools (a
	// judge's `allowed_tools`), which the harness MERGES with the tool grant the
	// write itself needs, so the two do not arrive as two competing flags.
	//
	// --stop-script puts the agent's output file outside the working tree on
	// purpose, and a harness that sandboxes writes will refuse it. This is the
	// seam for saying so per-harness rather than assuming every harness has
	// Claude Code's permission model.
	grantWrite func(dir string, extraTools []string) []string
}

// claudeCodeSpec is Claude Code.
//
// The alias targets are the FAMILY aliases claude's own `--help` documents as
// "an alias for the latest model" — `haiku`, `sonnet`, `opus`, `fable` — not
// pinned versions like `claude-sonnet-4-5`. A pinned version is a promise this
// binary cannot keep: `claude-3-5-haiku-latest` is already retired and refused,
// and `claude-sonnet-4-7` never existed at all, so a table of pins would decay
// into a table of refusals with each release. A family alias is the harness's
// own answer to "the latest of this size", kept current by the harness rather
// than by this file. All four were confirmed to run.
//
// There are six sizes and four families, so two sizes double up. Claude Code
// offers nothing below Haiku, so `size-xs` and `size-sm` are both Haiku; the
// top two are Opus and Fable, which is where the largest models are. Doubling
// up is honest — the alias asks for a size and gets the closest thing this
// harness has — and it keeps every alias resolving, which is the property that
// makes an alias always end the search.
var claudeCodeSpec = harnessSpec{
	name:   ClaudeCode,
	binary: "claude",
	detect: func(getenv func(string) string) bool {
		// CLAUDECODE is what Claude Code sets on every session; the entrypoint
		// variable is checked too so that a session which sets only one of them
		// is still recognised. Both were confirmed present in a live session.
		return getenv("CLAUDECODE") != "" || getenv("CLAUDE_CODE_ENTRYPOINT") != ""
	},
	sizes: map[SizeAlias]string{
		SizeXS:  "haiku",
		SizeSM:  "haiku",
		SizeMD:  "sonnet",
		SizeLG:  "opus",
		SizeXL:  "opus",
		SizeXXL: "fable",
	},
	// A concrete name is offered when it looks like one of this harness's own.
	//
	// This is a PREFIX test, not a catalogue, and the difference is deliberate.
	// A hardcoded list of exact names would be wrong the day a new model ships:
	// a set naming a real, runnable model would be refused because this binary
	// had not heard of it, and the author would be told their model does not
	// exist when it does. The harness itself is the authority on its catalogue
	// and it already answers — it exits non-zero with a message naming the
	// model. So this decides only what a name is ABOUT, and lets the harness
	// decide whether it exists.
	//
	// The cost is that `claude-sonnet-4-7`, which does not exist, is treated as
	// a match here and refused by claude rather than skipped. That is the right
	// side to err on: skipping it silently would run some other model in its
	// place, which is the accountability failure the refusal rule exists to
	// prevent.
	offers: func(model string) bool {
		return strings.HasPrefix(model, "claude-") ||
			isClaudeFamilyAlias(model)
	},
	argsFlag: "--claude-args",

	// The isolation settings sr-agent ALWAYS gives Claude Code. Empty
	// hooks/mcpServers/enabledPlugins means the launched agent carries none of the
	// caller's session wiring: no sloprail hooks fire inside it, no plugins load,
	// no MCP servers connect. This is what the hand-rolled judge scripts passed as
	// `--settings '{"hooks":{},"mcpServers":{},"enabledPlugins":{}}'` before the
	// judge-check migration — lifted here so a judge check gets the isolation for
	// free rather than every rule restating it. Without it a judge that is itself
	// an agent (a rule-quality judge firing on RULE.md) would re-trigger the guard
	// on its own child's writes and recurse.
	baseArgs: []string{"--settings", `{"hooks":{},"mcpServers":{},"enabledPlugins":{}}`},

	// Writing the answer file takes BOTH of these, which was measured rather
	// than guessed and is not obvious from the help text.
	//
	// The output file is deliberately outside the working tree, and Claude Code
	// gates that twice. `--add-dir` grants the PATH: without it the agent reads
	// what it was asked to read, computes the right answer, and then says "I
	// need permission to write to that file", leaving the verifier an empty
	// file — a correct judgement recorded as a failure. `--allowed-tools Write`
	// grants the TOOL: with --add-dir alone the refusal is identical, which is
	// what makes this pair easy to get half-right.
	//
	// The Write grant is unscoped because a scoped one does not work here.
	// `Write(<path>)` is rejected outright — claude answers that only Edit
	// rules are matched by file permission checks — and `Edit(<path>)` was
	// measured not to cover creating the file, nor writing an already-created
	// empty one. So the narrowing that IS available is --add-dir, which is what
	// confines these writes to the one temporary directory sr-agent owns.
	//
	// The caller's extra tools (a judge's `allowed_tools`, e.g. Read) are unioned
	// into the SAME `--allowed-tools` argument as the Write the answer file needs.
	// claude's `--allowed-tools` is comma/space-separated, so one argument carrying
	// `Write Read` grants both; passing two `--allowed-tools` flags would make one
	// variadic group swallow the other's values. Write goes first so the grant the
	// mechanism requires is never dropped by a caller that named only its own tools.
	grantWrite: func(dir string, extraTools []string) []string {
		tools := append([]string{"Write"}, extraTools...)
		return []string{"--add-dir", dir, "--allowed-tools", strings.Join(tools, " ")}
	},
}

// isClaudeFamilyAlias reports whether a name is one of claude's own bare family
// aliases. They carry no `claude-` prefix, so the prefix test alone would miss
// them and a set naming `sonnet` outright would be skipped under the very
// harness that offers it.
func isClaudeFamilyAlias(model string) bool {
	switch model {
	case "haiku", "sonnet", "opus", "fable":
		return true
	}
	return false
}

// harnesses is the registry. Adding a harness is adding an entry here.
var harnesses = []harnessSpec{claudeCodeSpec}

// ErrNoHarness is returned when the environment names no harness this binary
// knows.
var ErrNoHarness = errors.New("no supported harness detected")

// ErrUnknownHarness is returned when `--harness` names one this binary does not
// support.
var ErrUnknownHarness = errors.New("unsupported harness")

// lookupSpec finds the registry entry for a named harness.
func lookupSpec(name Harness) (harnessSpec, bool) {
	for _, spec := range harnesses {
		if spec.name == name {
			return spec, true
		}
	}
	return harnessSpec{}, false
}

// supportedNames is every harness this binary supports, for diagnostics.
func supportedNames() []string {
	names := make([]string, 0, len(harnesses))
	for _, spec := range harnesses {
		names = append(names, string(spec.name))
	}
	sort.Strings(names)
	return names
}

// DetectHarness works out which harness is running, or refuses.
//
// An environment naming nothing known is an ERROR, not a default. The default
// would be Claude Code, and being wrong about it means running a different
// agent than the rule asked for while reporting success — a verdict attributed
// to a model that never saw the question. A refusal naming what was looked for
// is worth more than a guess that cannot be audited.
//
// The environment is read through a lookup so a test can supply one directly.
func DetectHarness(getenv func(string) string) (harnessSpec, error) {
	for _, spec := range harnesses {
		if spec.detect(getenv) {
			return spec, nil
		}
	}
	return harnessSpec{}, fmt.Errorf(
		"%w: nothing in the environment names one of %s. "+
			"Pass --harness to name it explicitly, which is what a hook running outside any harness must do",
		ErrNoHarness, strings.Join(supportedNames(), ", "))
}

// ResolveHarness picks the harness to run: the one `--harness` names, or the
// one the environment names when it names none.
//
// An override that names an unsupported harness is refused rather than falling
// through to detection. A caller who asked for Codex and silently got Claude
// Code is in exactly the position the refusal rule exists to prevent, and it
// would be worse here than in detection because the caller was explicit.
func ResolveHarness(override string, getenv func(string) string) (harnessSpec, error) {
	if override == "" {
		return DetectHarness(getenv)
	}
	spec, ok := lookupSpec(Harness(override))
	if !ok {
		return harnessSpec{}, fmt.Errorf(
			"%w: %q. Supported: %s",
			ErrUnknownHarness, override, strings.Join(supportedNames(), ", "))
	}
	return spec, nil
}

// aliasesComplete checks that every harness maps every alias.
//
// An alias ALWAYS resolving is the property the model-set design rests on: it
// is what makes an alias end the search and what makes anything after one
// unreachable. A harness missing one entry would break that quietly — a set
// naming that alias would fall through to the entries after it, which the
// author wrote believing they were unreachable. Checked here so a registry
// entry with a hole fails immediately rather than at the first set that names
// the missing size.
func aliasesComplete() error {
	for _, spec := range harnesses {
		for _, alias := range sizeAliases {
			if spec.sizes[alias] == "" {
				return fmt.Errorf(
					"harness %s maps no model for %s: every harness must map every size alias, "+
						"because an alias that did not resolve would make the entries after it reachable",
					spec.name, alias)
			}
		}
	}
	return nil
}
