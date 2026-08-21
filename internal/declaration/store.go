package declaration

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/sloprail/sloprail/internal/module"
)

// This file is the loader: it reads a project's `.sloprail/` root into the typed,
// validated declarations the future dispatch slice consumes.
//
// It mirrors internal/guardrail's store.go in shape and philosophy — a Store
// rooted at a directory, a Load that returns the sound declarations AND the ones
// that could not be loaded (rather than failing on the first bad one), and an
// Invalid carrying every fault so one typo cannot disarm a project — but reads
// the new declaration formats (file-guard, gate, context, and the structure
// singleton) rather than the old one-folder-per-guardrail GUARDRAIL.md.
//
// The YAML library is gopkg.in/yaml.v3, the same the old loader and the rest of
// the repo use (see go.mod and internal/guardrail/store.go). The declarations are
// whole-YAML files, not frontmatter, so there is no fence to split — yaml.Unmarshal
// reads the whole file.

// Store reads the declarations in force for a project: the ones it declares under
// its own `.sloprail/` directory, and the ones it installed, which live inside the
// plugins that ship them.
//
// The two are read the same way and validated by the same code, deliberately —
// the exact stance the old format's now-deleted internal/guardrail.Store took. A
// plugin's declaration is not a second kind of thing with its own rules; it is the
// same declaration in a different place, and the moment the two paths diverge is
// the moment a rule can behave one way for its author and another for the project
// that installed it.
//
// Rooted at the `.sloprail` dir itself (not the project root): the store owns every
// path beneath it, and a caller passes typed identifiers (a nature, a name), never
// a built path. A plugin's own `.sloprail` sits at `<pluginRoot>/.sloprail`, the
// same relative layout a project uses, so a rule can be developed in a project and
// shipped in a plugin without being rewritten.
type Store struct {
	root string

	// plugins are the installed plugins whose declarations this store also reads,
	// in precedence order. Empty for a store built with New, which is every caller
	// that has no business knowing about plugins — `sr-file validate` reads one
	// project's declarations and nothing else. The plugins are supplied ALREADY
	// RESOLVED (name and directory both) rather than discovered here: finding out
	// which plugins a project installed is internal/harness's job, and this package
	// takes the set of places to read from as an input. See NewWithPlugins.
	plugins []Origin
}

// New returns a store rooted at a project's `.sloprail` directory, reading only
// the declarations the project itself declares.
func New(root string) *Store { return &Store{root: root} }

// NewWithPlugins returns a store that reads a project's own declarations AND the
// ones shipped by the installed plugins given.
//
// projectRoot is the project's own `.sloprail` directory. The plugins are the
// installation ROOTS of the enabled plugins (the directory holding a plugin's
// `.sloprail/`, `hooks/`, `skills/`), each paired with the plugin NAME the user
// enabled it by — an Origin. This division mirrors guardrail.NewWithPlugins
// exactly: discovering which plugins a project installed means reading a specific
// harness's settings files and cache layout, knowledge that belongs in
// internal/harness and not spread into the loader. This package's job is to read
// declarations; it takes the set of places to read them from as an input.
//
// The name arrives as part of the Origin rather than being derived from the
// directory, because the name a user knows a plugin by is the one they wrote in
// their settings, and a cache directory's name is an artefact they never see.
// Deriving it here would mean guessing, and the guess would appear in refusal
// messages and in the disable list — the two places a wrong name costs most.
func NewWithPlugins(projectRoot string, plugins []Origin) *Store {
	return &Store{root: projectRoot, plugins: plugins}
}

// Directory names beneath `.sloprail`, one per file-backed nature. The store owns
// these — a caller never spells them.
const (
	dirFileGuard = "file-guard"
	dirGate      = "gate"
	dirContext   = "context"
)

// File names within a per-name folder, and the structure singleton. Named
// constants so the loader and any test agree on the on-disk spelling.
const (
	fileFileGuard = "file-guard.yaml"
	fileGate      = "gate.yaml"
	fileContext   = "context.yaml"
	fileStructure = "structure.yaml"
)

// Loaded is everything a load produced: the declarations in force, by nature, and
// the ones that could not be loaded.
//
// One field per nature rather than a single heterogeneous list, because the
// dispatch slice consumes them by nature — it matches file events against
// file-guards, wakes gates on their triggers, enters contexts on theirs — and a
// typed field per nature is what lets it take each without a type switch. The
// structure gate is a single optional value, not a slice: there is at most one
// per project.
type Loaded struct {
	// FileGuards are the loaded file-guard declarations, sorted by name.
	FileGuards []FileGuard

	// Gates are the loaded gate declarations, sorted by name.
	Gates []Gate

	// Contexts are the loaded context declarations, sorted by name.
	Contexts []Context

	// Structure is the tree-wide structure gate, or nil when the project declares
	// none. A pointer rather than a value with a "present" flag, so "no structure
	// gate" and "an empty structure gate" are distinct — the first is nil, the
	// second is a non-nil value the validator would already have refused.
	Structure *StructureGate

	// Invalid are the declarations that could not be loaded, across every nature,
	// sorted by their qualified name. Reported rather than fatal: the engine loads
	// every sound declaration and refuses only the ones that are not.
	Invalid []Invalid

	// Shadowed are plugin declarations a declaration of the same (nature, name)
	// displaced — the project's own, or an earlier-listed plugin's. Returned rather
	// than applied silently, for the reason guardrail.Resolution.Shadowed is: a
	// project that believes it has two protections and has one is the failure this
	// product exists to prevent. The precedence IS applied (the winner is what
	// loads); the consumer is merely told which rule they displaced and where it
	// lives, so overriding stays a choice rather than an accident. See Shadow.
	Shadowed []Shadow
}

// Shadow is one plugin declaration that a declaration of the same (nature, name)
// took precedence over. It mirrors guardrail.Shadow, extended by the nature axis
// the new format keys on (a plugin gate `x` does not shadow a project context
// `x`).
//
// # Why the project wins
//
// A project must be able to override a rule it did not write. The alternative is
// that installing a plugin can impose a rule the project cannot adjust except by
// uninstalling the whole plugin — which makes the plugin all-or-nothing and
// guarantees the first rule anyone disagrees with takes the useful ones down with
// it.
//
// # Why it is REPORTED
//
// Silently is the failure this product exists to prevent. Both declarations load,
// both are well-formed, and the tree looks exactly as it would if both enforced.
// So the shadowing travels back to the caller as a fact to announce.
type Shadow struct {
	// Nature and Name are the (nature, name) both declarations share.
	Nature Nature
	Name   string

	// Plugin is the plugin whose declaration was displaced, and PluginDir is where
	// that displaced declaration sits — the path an author needs to read the rule
	// they have overridden.
	Plugin    string
	PluginDir string

	// WinnerDir is the folder that took precedence — the project's own, or an
	// earlier-listed plugin's.
	WinnerDir string

	// WinnerPlugin is the plugin the winning declaration came from, empty when the
	// project's own won. What distinguishes "you overrode this" from "one of your
	// two plugins is quietly not enforcing", which need different remedies.
	WinnerPlugin string
}

// Qualified is the shadowed declaration's disable key, so a report can tell the
// consumer exactly what to write to silence the losing side if that is what they
// meant. Keyed on the displaced plugin's origin.
func (sh Shadow) Qualified() string {
	return Origin{Plugin: sh.Plugin, Root: sh.PluginDir}.Qualified(sh.Nature, sh.Name)
}

// describeName renders the shadowed (nature, name) for a message — "file-guard
// \"x\"" for a named nature, or bare "structure" for the singleton.
func (sh Shadow) describeName() string {
	if sh.Name == "" {
		return string(sh.Nature)
	}
	return string(sh.Nature) + " " + quoteName(sh.Name)
}

// Message renders a shadow for a person, naming both sides and what to do about
// it. One wording, here, because every hook point reports this and two copies
// would drift — the same single-source rule guardrail.Shadow.Message follows.
//
// The two cases are worded apart because the remedy differs. A project overriding
// a shipped rule is usually deliberate and needs only to be visible. One plugin
// displacing another's rule is nobody's decision — the consumer installed both and
// got an ordering they never chose — so that message says which one is live, or
// the reader cannot tell which rule is running.
func (sh Shadow) Message() string {
	if sh.WinnerPlugin == "" {
		return fmt.Sprintf(
			"%s in this project takes precedence over the one plugin %q ships, "+
				"so the plugin's version (%s) is not enforcing. "+
				"Rename one of them if both were meant to run, or remove the project's copy to go back to the plugin's.",
			sh.describeName(), sh.Plugin, sh.PluginDir)
	}
	return fmt.Sprintf(
		"two installed plugins ship a %s: plugin %q is enforcing (%s) and plugin %q is not (%s). "+
			"The earlier-installed one wins. Disable whichever you do not want with `disabled: [%s]` in %s.",
		sh.describeName(), sh.WinnerPlugin, sh.WinnerDir, sh.Plugin, sh.PluginDir, sh.Qualified(), configFile)
}

// Invalid is a declaration that could not be read or could not do what it says,
// and why. Modelled on internal/guardrail's Invalid: a broken rule needs
// attribution (which nature, which name) and every fault found, not the first, so
// an author fixing a declaration sees all of it at once rather than one reload per
// mistake.
type Invalid struct {
	// Nature says which of the formats this was — so a diagnostic can say
	// "gate X" rather than a bare name that collides across natures (a gate and a
	// context may both be named `people-linked`).
	Nature Nature

	// Name is the declaration's name, from its folder. Empty for the structure
	// singleton, which has no per-name folder.
	Name string

	// Origin says where this unloadable declaration was found. A broken rule needs
	// attribution more than a working one does, not less: the refusal it causes
	// tells an author to go and fix a file, and for a plugin's rule that file is not
	// in their project and not theirs to fix. The zero value is a project's own.
	// Mirrors guardrail.Invalid.Origin.
	Origin Origin

	// Path is where the unloadable declaration was found, so an author can open
	// the exact file.
	Path string

	// Problems is every fault found, each carrying a Kind a caller can branch on
	// with errors.Is.
	Problems []Problem

	// Reasons is Problems rendered one line each, and Reason is those joined —
	// derived at construction so they cannot drift from the list they summarise.
	Reasons []string
	Reason  string
}

// newInvalidWithOrigin builds an Invalid from the problems found, tagging it with
// the origin the declaration was read from, and keeping every view of the problems
// in step. The only place an Invalid is made.
//
// The origin is carried so a report about a rule that will not load names where
// the file lives (Attribution) and a consumer can switch it off from their own
// config (Qualified) — for a plugin's broken rule, the two facts they most need,
// exactly as guardrail.Invalid carries its Origin.
func newInvalidWithOrigin(nature Nature, name, path string, origin Origin, problems ...Problem) Invalid {
	reasons := Messages(problems)
	return Invalid{
		Nature:   nature,
		Name:     name,
		Origin:   origin,
		Path:     path,
		Problems: problems,
		Reasons:  reasons,
		Reason:   strings.Join(reasons, "; "),
	}
}

// Qualified names this declaration as its disable key — "<nature>/<name>" for a
// project's own, "<plugin>/<nature>/<name>" for a shipped one — the stable key a
// diagnostic and a sort use, and the one a consumer writes to switch a broken
// plugin declaration off (see Config). Unique across natures where a bare name is
// not, and across plugins where a bare nature/name is not.
//
// Delegates to Origin.Qualified so a broken declaration and a sound one of the
// same identity produce the SAME key — the disable list must reach a declaration
// whose contents never parsed, so its key cannot depend on anything inside the
// file, only on its origin, nature and folder name (all known from the path).
func (iv Invalid) Qualified() string {
	return iv.Origin.Qualified(iv.Nature, iv.Name)
}

// Attribution is this broken declaration's name as a refusal or diagnostic should
// carry it — bare for a project's own, plus " from plugin X" for a shipped one,
// so a report about a rule that will not load names where the file lives. Mirrors
// guardrail.Invalid.Attribution.
func (iv Invalid) Attribution() string {
	if iv.Name == "" {
		// The structure singleton has no name to quote; the origin alone says
		// where it came from.
		return string(iv.Nature) + iv.Origin.Describe()
	}
	return quoteName(iv.Name) + iv.Origin.Describe()
}

// describeName renders this broken declaration's (nature, name) for a message —
// "gate \"x\"" for a named nature, bare "structure" for the singleton — the same
// shape Shadow.describeName uses, so the invalid and shadow reports name a rule
// the one way.
func (iv Invalid) describeName() string {
	if iv.Name == "" {
		return string(iv.Nature)
	}
	return string(iv.Nature) + " " + quoteName(iv.Name)
}

// has reports whether any of this declaration's problems is of the given kind —
// the errors.Is form of the store_test hasKind helper, used by Remedy to branch
// on whether the file could be PARSED at all.
func (iv Invalid) has(kind error) bool {
	for _, p := range iv.Problems {
		if p.Is(kind) {
			return true
		}
	}
	return false
}

// Remedy is what to tell someone whose declaration could not load, and it differs
// by WHO OWNS THE FILE — the origin-aware repair guidance the old format carried
// on session_pre_tool.go's remedy, restored here beside Shadow.Message so the two
// diagnostics point at the same `disabled: [...]` mechanism with one wording.
//
// # Why it varies by owner
//
// For a project's own rule the advice has always been "fix the declaration, or
// disable it" and both halves are actionable: the file is in the tree, the author
// wrote it, and switching it off is one edit away.
//
// For a plugin's rule that advice is a trap. The declaration sits in an install
// cache the consumer did not write and must not edit — an edit there is silently
// undone by the next reinstall, so an author who followed it would fix the report,
// upgrade, and have the fault come back with no explanation. So a plugin's rule
// gets the mechanism that is genuinely theirs: `disabled: [<qualified>]` in their
// own config, the same key and file Shadow.Message quotes for a shipped rule they
// meant to switch off. The exact line is quoted, because advice the reader has to
// go and look up is advice they skip.
//
// # Why it varies again by whether it PARSED
//
// A project's declaration that merely failed validation is a rule with a mistake
// in it, so the advice is to fix or disable it. One that could not be PARSED might
// not be a declaration at all — a stray file, a note left in the folder — so
// "remove that folder if it is not a declaration" is a real way out the
// merely-invalid case does not have. A plugin's rule collapses both: the consumer
// can neither edit nor remove a file inside an install cache, so the disable list
// is the one remedy either way.
//
// This text accompanies a REPORT, not a refusal — an invalid declaration blocks
// nothing. That makes the wording matter more, not less: it is the only thing
// between a rule that silently stopped enforcing and a person who fixes it, so it
// has to name a remedy the reader can actually perform.
func (iv Invalid) Remedy() string {
	if !iv.Origin.FromPlugin() {
		if iv.has(ErrMalformed) {
			return fmt.Sprintf(
				"fix the %s in %s, or remove that folder if it is not a declaration.",
				iv.describeName(), dotDirName)
		}
		return fmt.Sprintf(
			"fix the %s in %s, or disable it with `disabled: [%s]` in %s if it is not ready.",
			iv.describeName(), dotDirName, iv.Qualified(), configFile)
	}

	// A plugin's rule, where neither editing the file nor removing its folder is
	// available: the consumer owns none of it and a reinstall would restore
	// anything they deleted. The disable list on their side of the boundary is the
	// one remedy that survives, quoted for both the unparseable and the
	// merely-invalid case — the same key and config file Shadow.Message names.
	return fmt.Sprintf(
		"this rule is not yours to fix — it ships inside plugin %q, at %s. "+
			"Report it to that plugin, or switch it off for this project with `disabled: [%s]` in %s.",
		iv.Origin.Plugin, iv.Origin.Root, iv.Qualified(), configFile)
}

// Load reads every declaration in force — the project's own and the plugins' —
// validating each against the event vocabulary the given registry declares, and
// resolving precedence and the project's disable list.
//
// A project with no `.sloprail` directory, or one with none of a given nature's
// folders, has no declarations of that kind — not an error, the ordinary state of
// a project that has not adopted them. A plugin root with no `.sloprail` is the
// same: most plugins ship none.
//
// # The order of operations, mirroring guardrail.Store.Resolve
//
//  1. PARSE every root — the project's own first, then each plugin's in order —
//     tagging each declaration with its Origin. Parsing is separated from
//     validating so the FULL set of context names (across all roots) is known
//     before any declaration is validated: a `require: [{context: X}]` may name a
//     context declared in another root, and it resolves as long as SOME root
//     declares X, independent of who wins precedence.
//  2. VALIDATE each parsed declaration against that environment. A declaration
//     with any disabling problem becomes an Invalid.
//  3. RESOLVE precedence: the project's declarations claim their (nature, name)
//     first, so they win; between two plugins the earlier-listed wins. A
//     displaced declaration is recorded as Shadowed rather than loaded — first
//     writer wins, and it is never loaded even briefly.
//  4. DISABLE: the project's own config (`.sloprail/config.yaml` `disabled:`) is
//     applied last, filtering both the loaded set and the Invalid set — the
//     consumer's final say over everything above, and the only way to switch off
//     a plugin declaration that will not load (its file is in an install cache
//     they must not edit). This is the exact shape guardrail.Store.Resolve uses.
//
// A nil registry parses and validates everything EXCEPT trigger `match`
// expressions, which have no kind declaration to compile against — the same
// position internal/guardrail.Load (versus LoadWith) takes. Every caller about to
// act on what it loaded passes modules.Registry.
func (s *Store) Load(reg *module.Registry) (Loaded, error) {
	// The project's disable list, read first so a config that exists and cannot be
	// parsed refuses the whole load rather than silently re-enabling every rule the
	// project switched off — the fail-closed guardrail.LoadConfig takes, for the
	// same reason.
	cfg, err := loadConfig(s.root)
	if err != nil {
		return Loaded{}, err
	}

	// -- 1. parse every root, project first, tagging origin --
	//
	// The origins to read, in precedence order: the project's own `.sloprail`
	// (empty Origin) ahead of every plugin's, so the project's declarations are the
	// ones already claimed when a plugin offers the same (nature, name).
	roots := s.rootsInPrecedenceOrder()

	var (
		parsedFileGuards []FileGuard
		parsedGates      []Gate
		parsedContexts   []Context
		parsedStructures []StructureGate // at most one per root; precedence picks the winner
		parseInvalid     []Invalid
	)
	for _, r := range roots {
		fgs, fgInvalid, err := parseFileGuards(r.dir, r.origin)
		if err != nil {
			return Loaded{}, err
		}
		gates, gateInvalid, err := parseGates(r.dir, r.origin)
		if err != nil {
			return Loaded{}, err
		}
		contexts, ctxInvalid, err := parseContexts(r.dir, r.origin)
		if err != nil {
			return Loaded{}, err
		}
		structure, structInvalid, err := parseStructure(r.dir, r.origin)
		if err != nil {
			return Loaded{}, err
		}
		parsedFileGuards = append(parsedFileGuards, fgs...)
		parsedGates = append(parsedGates, gates...)
		parsedContexts = append(parsedContexts, contexts...)
		if structure != nil {
			parsedStructures = append(parsedStructures, *structure)
		}
		parseInvalid = append(parseInvalid, fgInvalid...)
		parseInvalid = append(parseInvalid, gateInvalid...)
		parseInvalid = append(parseInvalid, ctxInvalid...)
		parseInvalid = append(parseInvalid, structInvalid...)
	}

	// The set of declared context names, from the contexts that PARSED across ALL
	// roots. A prerequisite naming a context declared in any root resolves — the
	// name existing is what a require checks, and precedence only decides which
	// declaration of that name wins, not whether the name exists. A context whose
	// YAML did not parse contributes no name (correctly: a context the engine could
	// not read is one it cannot order against).
	contextNames := make(map[string]bool, len(parsedContexts))
	for _, c := range parsedContexts {
		contextNames[c.Name] = true
	}
	env := Env{Registry: reg, Contexts: contextNames}

	// -- 2. validate; the sound ones go forward to precedence, the broken to Invalid --
	var (
		soundFileGuards []FileGuard
		soundGates      []Gate
		soundContexts   []Context
		soundStructures []StructureGate
	)
	invalid := append([]Invalid(nil), parseInvalid...)
	for _, g := range parsedFileGuards {
		if problems := ValidateFileGuard(g, env); Disabling(problems) {
			invalid = append(invalid, newInvalidWithOrigin(NatureFileGuard, g.Name, fileGuardPath(originRoot(s.root, g.Origin), g.Name), g.Origin, problems...))
			continue
		}
		soundFileGuards = append(soundFileGuards, g)
	}
	for _, g := range parsedGates {
		if problems := ValidateGate(g, env); Disabling(problems) {
			invalid = append(invalid, newInvalidWithOrigin(NatureGate, g.Name, gatePath(originRoot(s.root, g.Origin), g.Name), g.Origin, problems...))
			continue
		}
		soundGates = append(soundGates, g)
	}
	for _, c := range parsedContexts {
		if problems := ValidateContext(c, env); Disabling(problems) {
			invalid = append(invalid, newInvalidWithOrigin(NatureContext, c.Name, contextPath(originRoot(s.root, c.Origin), c.Name), c.Origin, problems...))
			continue
		}
		soundContexts = append(soundContexts, c)
	}
	for _, sg := range parsedStructures {
		if problems := ValidateStructureGate(sg, env); Disabling(problems) {
			invalid = append(invalid, newInvalidWithOrigin(NatureStructure, "", structurePath(originRoot(s.root, sg.Origin)), sg.Origin, problems...))
			continue
		}
		soundStructures = append(soundStructures, sg)
	}

	// -- 3. resolve precedence; a displaced declaration is Shadowed, not loaded --
	var out Loaded
	out.Invalid = invalid
	resolveFileGuards(&out, soundFileGuards)
	resolveGates(&out, soundGates)
	resolveContexts(&out, soundContexts)
	resolveStructure(&out, soundStructures)

	// -- 4. apply the project's disable list to loaded AND invalid --
	applyDisable(&out, cfg)

	sortLoaded(&out)
	return out, nil
}

// rootHandle is one directory to read declarations from, and the Origin to tag
// what is found there.
type rootHandle struct {
	dir    string
	origin Origin
}

// rootsInPrecedenceOrder is the project's own `.sloprail` (empty Origin) ahead of
// each plugin's `.sloprail`, in the order the plugins were given. The order is the
// precedence: the project claims a (nature, name) first, then plugins in turn, so
// the project always wins and between two plugins the earlier-listed does. It is
// why the caller must NOT sort the plugins after resolving which one wins — the
// resolver sorts nothing.
func (s *Store) rootsInPrecedenceOrder() []rootHandle {
	roots := []rootHandle{{dir: s.root, origin: Origin{}}}
	for _, p := range s.plugins {
		roots = append(roots, rootHandle{dir: pluginDotDir(p.Root), origin: p})
	}
	return roots
}

// -- path construction (the store owns every path) --
//
// Package functions taking a root, because a load now reads several roots (the
// project's own and each plugin's) and the root is therefore an argument rather
// than a property of the store — the same shape guardrail's loadOne took when it
// grew a second root.

func natureDir(root, dir string) string { return filepath.Join(root, dir) }
func fileGuardPath(root, name string) string {
	return filepath.Join(root, dirFileGuard, name, fileFileGuard)
}
func gatePath(root, name string) string { return filepath.Join(root, dirGate, name, fileGate) }
func contextPath(root, name string) string {
	return filepath.Join(root, dirContext, name, fileContext)
}
func structurePath(root string) string { return filepath.Join(root, dirFileGuard, fileStructure) }

// pluginDotDir is where a plugin's declarations live inside its installation —
// `<pluginRoot>/.sloprail`, the SAME relative layout a project uses, so a rule can
// be developed in a project and shipped in a plugin without being rewritten. A
// plugin ships declarations the way it already ships hooks/ and skills/: a
// directory at its root named for what is in it.
func pluginDotDir(pluginRoot string) string { return filepath.Join(pluginRoot, dotDirName) }

// dotDirName is the directory a project (and a plugin) keeps its new-format
// declarations in. Named here so the loader and the plugin-root resolution agree
// on the spelling; it matches services/sr-session's DotDirName.
const dotDirName = ".sloprail"

// originRoot returns the `.sloprail` directory a declaration with this origin was
// read from — the project's own projectRoot for a project declaration, or the
// plugin's `.sloprail` for a shipped one. Used to rebuild a declaration's path for
// an Invalid after validation, so a broken declaration names the exact file even
// though validation does not carry the path.
func originRoot(projectRoot string, o Origin) string {
	if !o.FromPlugin() {
		return projectRoot
	}
	return pluginDotDir(o.Root)
}

// -- per-nature parsing --
//
// Each parse* reads one root's nature folders, unmarshals each file, tags it with
// the origin, and returns the parsed declarations alongside any Invalid for a file
// that would not parse. Validation is NOT done here — it needs the environment
// assembled in Load. A folder that does not exist yields nothing, not an error:
// the ordinary state of a project (or plugin) that has not adopted that nature.

func parseFileGuards(root string, origin Origin) ([]FileGuard, []Invalid, error) {
	names, err := natureNames(root, dirFileGuard)
	if err != nil {
		return nil, nil, err
	}
	var (
		decls   []FileGuard
		invalid []Invalid
	)
	for _, name := range names {
		path := fileGuardPath(root, name)
		var g FileGuard
		if problems := parseYAMLFile(path, &g); problems != nil {
			invalid = append(invalid, newInvalidWithOrigin(NatureFileGuard, name, path, origin, problems...))
			continue
		}
		g.Name = name
		g.Dir = filepath.Join(root, dirFileGuard, name)
		g.Origin = origin
		decls = append(decls, g)
	}
	return decls, invalid, nil
}

func parseGates(root string, origin Origin) ([]Gate, []Invalid, error) {
	names, err := natureNames(root, dirGate)
	if err != nil {
		return nil, nil, err
	}
	var (
		decls   []Gate
		invalid []Invalid
	)
	for _, name := range names {
		path := gatePath(root, name)
		var g Gate
		if problems := parseYAMLFile(path, &g); problems != nil {
			invalid = append(invalid, newInvalidWithOrigin(NatureGate, name, path, origin, problems...))
			continue
		}
		g.Name = name
		g.Dir = filepath.Join(root, dirGate, name)
		g.Origin = origin
		decls = append(decls, g)
	}
	return decls, invalid, nil
}

func parseContexts(root string, origin Origin) ([]Context, []Invalid, error) {
	names, err := natureNames(root, dirContext)
	if err != nil {
		return nil, nil, err
	}
	var (
		decls   []Context
		invalid []Invalid
	)
	for _, name := range names {
		path := contextPath(root, name)
		var c Context
		if problems := parseYAMLFile(path, &c); problems != nil {
			invalid = append(invalid, newInvalidWithOrigin(NatureContext, name, path, origin, problems...))
			continue
		}
		c.Name = name
		c.Dir = filepath.Join(root, dirContext, name)
		c.Origin = origin
		decls = append(decls, c)
	}
	return decls, invalid, nil
}

// parseStructure reads one root's structure singleton, if present. Absent is nil,
// not an error — most projects (and plugins) declare no structure gate. Unlike the
// per-name natures it is one fixed file, so there are no names to enumerate.
func parseStructure(root string, origin Origin) (*StructureGate, []Invalid, error) {
	path := structurePath(root)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("declaration: read %s: %w", path, err)
	}
	var sg StructureGate
	if problems := parseYAML(path, data, &sg); problems != nil {
		return nil, []Invalid{newInvalidWithOrigin(NatureStructure, "", path, origin, problems...)}, nil
	}
	sg.Dir = filepath.Join(root, dirFileGuard)
	sg.Origin = origin
	return &sg, nil, nil
}

// natureNames lists the per-name subfolders of one nature's directory under a
// root, in a stable order. A missing directory yields no names, not an error.
//
// Only DIRECTORIES are names — a nature's folder holds one subfolder per
// declaration, each carrying the nature's yaml plus its scripts. A stray FILE in
// the nature directory (structure.yaml is the one legitimate case, and it sits in
// file-guard/) is not a name and is skipped here; structure.yaml is read by its
// own parseStructure, not through this enumeration.
func natureNames(root, dir string) ([]string, error) {
	entries, err := os.ReadDir(natureDir(root, dir))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("declaration: read %s: %w", natureDir(root, dir), err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names, nil
}

// parseYAMLFile reads and unmarshals one declaration file into dst, returning the
// problems that stopped it (unreadable file, unparseable YAML) or nil when it
// parsed. A file that is missing where a folder said it should be is a malformed
// declaration, not an absent one: the folder `gate/foo/` with no `gate.yaml`
// inside is a half-written gate, and refusing it by name is better than silently
// skipping a folder the author clearly meant to hold a rule.
func parseYAMLFile(path string, dst any) []Problem {
	data, err := os.ReadFile(path)
	if err != nil {
		return []Problem{prob(ErrMalformed, "", "read: %v", err)}
	}
	return parseYAML(path, data, dst)
}

// parseYAML unmarshals declaration bytes into dst. Strict decoding (KnownFields)
// is deliberately NOT used: an unknown key is tolerated, matching the old
// loader's tolerance and the spec's storage models, which are open. A future
// field an older engine does not know about should not make a declaration written
// for a newer one fail to load.
func parseYAML(path string, data []byte, dst any) []Problem {
	if err := yaml.Unmarshal(data, dst); err != nil {
		return []Problem{prob(ErrMalformed, "", "parse %s: %v", filepath.Base(path), err)}
	}
	return nil
}

// sortLoaded orders every slice in a Loaded by name, so two loads of the same
// project produce the same order — a diff of two reports is signal, not the noise
// directory iteration order would inject.
//
// Sorting the loaded slices AFTER precedence is safe: precedence is decided by
// claiming during resolution (first writer wins), not by the final order, so a
// name-sort here reorders the winners without changing who won. The Shadowed slice
// is sorted too, so a report of displacements is stable across runs.
func sortLoaded(l *Loaded) {
	sort.Slice(l.FileGuards, func(i, j int) bool { return l.FileGuards[i].Name < l.FileGuards[j].Name })
	sort.Slice(l.Gates, func(i, j int) bool { return l.Gates[i].Name < l.Gates[j].Name })
	sort.Slice(l.Contexts, func(i, j int) bool { return l.Contexts[i].Name < l.Contexts[j].Name })
	sort.Slice(l.Invalid, func(i, j int) bool { return l.Invalid[i].Qualified() < l.Invalid[j].Qualified() })
	sort.Slice(l.Shadowed, func(i, j int) bool { return l.Shadowed[i].Qualified() < l.Shadowed[j].Qualified() })
}

// -- precedence resolution --
//
// Each resolve* walks the SOUND declarations of one nature in the order they were
// parsed (project's own first, then plugins in the given order), and claims each
// (nature, name). The first to claim a name wins and is loaded; a later
// declaration of the same name is recorded as Shadowed and NOT loaded — first
// writer wins, so the project always does and between two plugins the earlier
// does. This is guardrail.Store.Resolve's claim loop, per nature.
//
// Only sound declarations are resolved here. A broken declaration is already an
// Invalid; whether a broken PLUGIN declaration a project has overridden should
// still be reported is a real question the old format answers (it reports the
// shadow), but the new format's dispatch already treats an Invalid as blocking
// nothing and every hook point reports the full Invalid set, so a broken shadowed
// plugin declaration surfaces as an Invalid the consumer can disable by its
// qualified name — the same remedy. Keeping the broken-and-shadowed bookkeeping
// out of here avoids inventing a second reporting path for a case the Invalid set
// already covers.

func resolveFileGuards(out *Loaded, sound []FileGuard) {
	claimed := map[string]FileGuard{}
	for _, g := range sound {
		if prior, taken := claimed[g.Name]; taken {
			out.Shadowed = append(out.Shadowed, shadowOf(NatureFileGuard, g.Name, g.Origin, g.Dir, prior.Origin, prior.Dir))
			continue
		}
		claimed[g.Name] = g
		out.FileGuards = append(out.FileGuards, g)
	}
}

func resolveGates(out *Loaded, sound []Gate) {
	claimed := map[string]Gate{}
	for _, g := range sound {
		if prior, taken := claimed[g.Name]; taken {
			out.Shadowed = append(out.Shadowed, shadowOf(NatureGate, g.Name, g.Origin, g.Dir, prior.Origin, prior.Dir))
			continue
		}
		claimed[g.Name] = g
		out.Gates = append(out.Gates, g)
	}
}

func resolveContexts(out *Loaded, sound []Context) {
	claimed := map[string]Context{}
	for _, c := range sound {
		if prior, taken := claimed[c.Name]; taken {
			out.Shadowed = append(out.Shadowed, shadowOf(NatureContext, c.Name, c.Origin, c.Dir, prior.Origin, prior.Dir))
			continue
		}
		claimed[c.Name] = c
		out.Contexts = append(out.Contexts, c)
	}
}

// resolveStructure claims the single structure gate. The structure gate is a
// singleton per root, so the FIRST root that declares one wins — the project's own
// over any plugin's, and an earlier plugin's over a later one's — and every other
// is Shadowed. This is the same "first writer wins" the per-name natures use,
// applied to the one nature that has no name.
func resolveStructure(out *Loaded, sound []StructureGate) {
	for _, sg := range sound {
		if out.Structure != nil {
			prior := out.Structure
			out.Shadowed = append(out.Shadowed, shadowOf(NatureStructure, "", sg.Origin, sg.Dir, prior.Origin, prior.Dir))
			continue
		}
		winner := sg
		out.Structure = &winner
	}
}

// shadowOf builds the Shadow record for a displaced declaration: what was
// displaced (loser's origin/dir) and what displaced it (winner's origin/dir). The
// winner may be the project (empty WinnerPlugin) or an earlier plugin — the two
// cases a report words differently, because the remedy differs.
func shadowOf(nature Nature, name string, loser Origin, loserDir string, winner Origin, winnerDir string) Shadow {
	return Shadow{
		Nature:       nature,
		Name:         name,
		Plugin:       loser.Plugin,
		PluginDir:    loserDir,
		WinnerDir:    winnerDir,
		WinnerPlugin: winner.Plugin,
	}
}

// applyDisable removes from the loaded AND invalid sets every declaration the
// project's config switches off, keyed on the qualified name.
//
// Filtered out entirely rather than marked, so a disabled declaration is inert in
// the same way across natures: it never dispatches, no check runs for it. The
// INVALID ones are filtered too, and that half is the one that matters most — a
// declaration that cannot load blocks nothing but is reported every dispatch, and
// when it is a plugin's the consumer cannot fix the file (it is in an install
// cache). Without reaching the invalid set, a plugin shipping one broken
// declaration would keep a consuming project's logs noisy with a report they have
// no way to silence. This is guardrail.Store.Resolve's disable step, applied to
// every nature.
func applyDisable(out *Loaded, cfg config) {
	if len(cfg.Disabled) == 0 {
		return
	}

	fgs := out.FileGuards[:0]
	for _, g := range out.FileGuards {
		if cfg.isDisabled(g.Qualified()) {
			continue
		}
		fgs = append(fgs, g)
	}
	out.FileGuards = fgs

	gates := out.Gates[:0]
	for _, g := range out.Gates {
		if cfg.isDisabled(g.Qualified()) {
			continue
		}
		gates = append(gates, g)
	}
	out.Gates = gates

	contexts := out.Contexts[:0]
	for _, c := range out.Contexts {
		if cfg.isDisabled(c.Qualified()) {
			continue
		}
		contexts = append(contexts, c)
	}
	out.Contexts = contexts

	if out.Structure != nil && cfg.isDisabled(out.Structure.Qualified()) {
		out.Structure = nil
	}

	invalid := out.Invalid[:0]
	for _, iv := range out.Invalid {
		if cfg.isDisabled(iv.Qualified()) {
			continue
		}
		invalid = append(invalid, iv)
	}
	out.Invalid = invalid
}
