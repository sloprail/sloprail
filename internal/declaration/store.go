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
// the FIVE new formats rather than the old one-folder-per-guardrail GUARDRAIL.md.
//
// The YAML library is gopkg.in/yaml.v3, the same the old loader and the rest of
// the repo use (see go.mod and internal/guardrail/store.go). The declarations are
// whole-YAML files, not frontmatter, so there is no fence to split — yaml.Unmarshal
// reads the whole file.

// Store reads the declarations a project keeps under its `.sloprail/` directory.
//
// Rooted at the `.sloprail` dir itself (not the project root), matching how
// internal/guardrail.Store is rooted at the dot-directory: the store owns every
// path beneath it, and a caller passes typed identifiers (a nature, a name), never
// a built path. Plugin-shipped declarations are out of scope for this slice — the
// old loader's plugin resolution stays where it is; this reads one project's own
// `.sloprail`.
type Store struct {
	root string
}

// New returns a store rooted at a project's `.sloprail` directory.
func New(root string) *Store { return &Store{root: root} }

// Directory names beneath `.sloprail`, one per file-backed nature. The store owns
// these — a caller never spells them.
const (
	dirFileGuard = "file-guard"
	dirGate      = "gate"
	dirContext   = "context"
	dirGoal      = "goal"
)

// File names within a per-name folder, and the structure singleton. Named
// constants so the loader and any test agree on the on-disk spelling.
const (
	fileFileGuard = "file-guard.yaml"
	fileGate      = "gate.yaml"
	fileContext   = "context.yaml"
	fileGoal      = "goal.yaml"
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

	// Goals are the loaded goal declarations, sorted by name.
	Goals []Goal

	// Structure is the tree-wide structure gate, or nil when the project declares
	// none. A pointer rather than a value with a "present" flag, so "no structure
	// gate" and "an empty structure gate" are distinct — the first is nil, the
	// second is a non-nil value the validator would already have refused.
	Structure *StructureGate

	// Invalid are the declarations that could not be loaded, across every nature,
	// sorted by their qualified name. Reported rather than fatal: the engine loads
	// every sound declaration and refuses only the ones that are not.
	Invalid []Invalid
}

// Invalid is a declaration that could not be read or could not do what it says,
// and why. Modelled on internal/guardrail's Invalid: a broken rule needs
// attribution (which nature, which name) and every fault found, not the first, so
// an author fixing a declaration sees all of it at once rather than one reload per
// mistake.
type Invalid struct {
	// Nature says which of the five formats this was — so a diagnostic can say
	// "gate X" rather than a bare name that collides across natures (a gate and a
	// context may both be named `people-linked`).
	Nature Nature

	// Name is the declaration's name, from its folder. Empty for the structure
	// singleton, which has no per-name folder.
	Name string

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

// newInvalid builds an Invalid from the problems found, keeping every view of
// them in step. The only place an Invalid is made.
func newInvalid(nature Nature, name, path string, problems ...Problem) Invalid {
	reasons := Messages(problems)
	return Invalid{
		Nature:   nature,
		Name:     name,
		Path:     path,
		Problems: problems,
		Reasons:  reasons,
		Reason:   strings.Join(reasons, "; "),
	}
}

// Qualified names this declaration as "<nature>/<name>", the stable key a
// diagnostic and a sort use — unique across natures where a bare name is not.
func (iv Invalid) Qualified() string {
	if iv.Name == "" {
		return string(iv.Nature)
	}
	return string(iv.Nature) + "/" + iv.Name
}

// Load reads every declaration under `.sloprail`, validating each against the
// event vocabulary the given registry declares.
//
// A project with no `.sloprail` directory, or one with none of a given nature's
// folders, has no declarations of that kind — not an error, the ordinary state of
// a project that has not adopted them.
//
// The load is TWO-PHASE for one reason: a `require: [{context: X}]` on any
// declaration may name a context X declared in the context folder, and resolving
// it needs the full set of context names in hand before ANY declaration is
// validated. So phase one parses every context.yaml and collects the names, and
// phase two validates everything (contexts included) against that set. Parsing a
// context still happens once — its parsed form from phase one is reused in phase
// two, not re-read.
//
// A nil registry parses and validates everything EXCEPT trigger `match`
// expressions, which have no kind declaration to compile against — the same
// position internal/guardrail.Load (versus LoadWith) takes. Every caller about to
// act on what it loaded passes modules.Registry.
func (s *Store) Load(reg *module.Registry) (Loaded, error) {
	// Phase one: parse every nature's files. Parsing is separated from validating
	// so the context names are known before validation runs. A parse failure
	// (unreadable YAML) is recorded as an Invalid immediately — it needs no
	// environment to diagnose.
	fileGuards, fgInvalid, err := s.parseFileGuards()
	if err != nil {
		return Loaded{}, err
	}
	gates, gateInvalid, err := s.parseGates()
	if err != nil {
		return Loaded{}, err
	}
	contexts, ctxInvalid, err := s.parseContexts()
	if err != nil {
		return Loaded{}, err
	}
	goals, goalInvalid, err := s.parseGoals()
	if err != nil {
		return Loaded{}, err
	}
	structure, structInvalid, err := s.parseStructure()
	if err != nil {
		return Loaded{}, err
	}

	// The set of declared context names, from the contexts that PARSED. A context
	// whose YAML did not parse contributes no name — a prerequisite naming it
	// would (correctly) be reported as unknown, because a context the engine could
	// not read is a context it cannot order against.
	contextNames := make(map[string]bool, len(contexts))
	for _, c := range contexts {
		contextNames[c.Name] = true
	}
	env := Env{Registry: reg, Contexts: contextNames}

	var out Loaded
	out.Invalid = append(out.Invalid, fgInvalid...)
	out.Invalid = append(out.Invalid, gateInvalid...)
	out.Invalid = append(out.Invalid, ctxInvalid...)
	out.Invalid = append(out.Invalid, goalInvalid...)
	out.Invalid = append(out.Invalid, structInvalid...)

	// Phase two: validate each parsed declaration against the environment. A
	// declaration with any disabling problem becomes an Invalid; a sound one joins
	// the loaded set.
	for _, g := range fileGuards {
		if problems := ValidateFileGuard(g, env); Disabling(problems) {
			out.Invalid = append(out.Invalid, newInvalid(NatureFileGuard, g.Name, s.fileGuardPath(g.Name), problems...))
			continue
		}
		out.FileGuards = append(out.FileGuards, g)
	}
	for _, g := range gates {
		if problems := ValidateGate(g, env); Disabling(problems) {
			out.Invalid = append(out.Invalid, newInvalid(NatureGate, g.Name, s.gatePath(g.Name), problems...))
			continue
		}
		out.Gates = append(out.Gates, g)
	}
	for _, c := range contexts {
		if problems := ValidateContext(c, env); Disabling(problems) {
			out.Invalid = append(out.Invalid, newInvalid(NatureContext, c.Name, s.contextPath(c.Name), problems...))
			continue
		}
		out.Contexts = append(out.Contexts, c)
	}
	for _, g := range goals {
		if problems := ValidateGoal(g, env); Disabling(problems) {
			out.Invalid = append(out.Invalid, newInvalid(NatureGoal, g.Name, s.goalPath(g.Name), problems...))
			continue
		}
		out.Goals = append(out.Goals, g)
	}
	if structure != nil {
		if problems := ValidateStructureGate(*structure, env); Disabling(problems) {
			out.Invalid = append(out.Invalid, newInvalid(NatureStructure, "", s.structurePath(), problems...))
		} else {
			out.Structure = structure
		}
	}

	sortLoaded(&out)
	return out, nil
}

// -- path construction (the store owns every path) --

func (s *Store) natureDir(dir string) string { return filepath.Join(s.root, dir) }
func (s *Store) fileGuardPath(name string) string {
	return filepath.Join(s.root, dirFileGuard, name, fileFileGuard)
}
func (s *Store) gatePath(name string) string { return filepath.Join(s.root, dirGate, name, fileGate) }
func (s *Store) contextPath(name string) string {
	return filepath.Join(s.root, dirContext, name, fileContext)
}
func (s *Store) goalPath(name string) string { return filepath.Join(s.root, dirGoal, name, fileGoal) }
func (s *Store) structurePath() string       { return filepath.Join(s.root, dirFileGuard, fileStructure) }

// -- per-nature parsing --
//
// Each parse* reads one nature's per-name folders, unmarshals each file, and
// returns the parsed declarations alongside any Invalid for a file that would not
// parse. Validation is NOT done here — it needs the environment assembled in
// Load. A folder that does not exist yields nothing, not an error: the ordinary
// state of a project that has not adopted that nature.

func (s *Store) parseFileGuards() ([]FileGuard, []Invalid, error) {
	names, err := s.natureNames(dirFileGuard)
	if err != nil {
		return nil, nil, err
	}
	var (
		decls   []FileGuard
		invalid []Invalid
	)
	for _, name := range names {
		path := s.fileGuardPath(name)
		var g FileGuard
		if problems := parseYAMLFile(path, &g); problems != nil {
			invalid = append(invalid, newInvalid(NatureFileGuard, name, path, problems...))
			continue
		}
		g.Name = name
		g.Dir = filepath.Join(s.root, dirFileGuard, name)
		decls = append(decls, g)
	}
	return decls, invalid, nil
}

func (s *Store) parseGates() ([]Gate, []Invalid, error) {
	names, err := s.natureNames(dirGate)
	if err != nil {
		return nil, nil, err
	}
	var (
		decls   []Gate
		invalid []Invalid
	)
	for _, name := range names {
		path := s.gatePath(name)
		var g Gate
		if problems := parseYAMLFile(path, &g); problems != nil {
			invalid = append(invalid, newInvalid(NatureGate, name, path, problems...))
			continue
		}
		g.Name = name
		g.Dir = filepath.Join(s.root, dirGate, name)
		decls = append(decls, g)
	}
	return decls, invalid, nil
}

func (s *Store) parseContexts() ([]Context, []Invalid, error) {
	names, err := s.natureNames(dirContext)
	if err != nil {
		return nil, nil, err
	}
	var (
		decls   []Context
		invalid []Invalid
	)
	for _, name := range names {
		path := s.contextPath(name)
		var c Context
		if problems := parseYAMLFile(path, &c); problems != nil {
			invalid = append(invalid, newInvalid(NatureContext, name, path, problems...))
			continue
		}
		c.Name = name
		c.Dir = filepath.Join(s.root, dirContext, name)
		decls = append(decls, c)
	}
	return decls, invalid, nil
}

func (s *Store) parseGoals() ([]Goal, []Invalid, error) {
	names, err := s.natureNames(dirGoal)
	if err != nil {
		return nil, nil, err
	}
	var (
		decls   []Goal
		invalid []Invalid
	)
	for _, name := range names {
		path := s.goalPath(name)
		var g Goal
		if problems := parseYAMLFile(path, &g); problems != nil {
			invalid = append(invalid, newInvalid(NatureGoal, name, path, problems...))
			continue
		}
		g.Name = name
		g.Dir = filepath.Join(s.root, dirGoal, name)
		decls = append(decls, g)
	}
	return decls, invalid, nil
}

// parseStructure reads the structure singleton, if present. Absent is nil, not an
// error — most projects declare no structure gate. Unlike the per-name natures it
// is one fixed file, so there are no names to enumerate.
func (s *Store) parseStructure() (*StructureGate, []Invalid, error) {
	path := s.structurePath()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("declaration: read %s: %w", path, err)
	}
	var sg StructureGate
	if problems := parseYAML(path, data, &sg); problems != nil {
		return nil, []Invalid{newInvalid(NatureStructure, "", path, problems...)}, nil
	}
	sg.Dir = filepath.Join(s.root, dirFileGuard)
	return &sg, nil, nil
}

// natureNames lists the per-name subfolders of one nature's directory, in a
// stable order. A missing directory yields no names, not an error.
//
// Only DIRECTORIES are names — a nature's folder holds one subfolder per
// declaration, each carrying the nature's yaml plus its scripts. A stray FILE in
// the nature directory (structure.yaml is the one legitimate case, and it sits in
// file-guard/) is not a name and is skipped here; structure.yaml is read by its
// own parseStructure, not through this enumeration.
func (s *Store) natureNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(s.natureDir(dir))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("declaration: read %s: %w", s.natureDir(dir), err)
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
func sortLoaded(l *Loaded) {
	sort.Slice(l.FileGuards, func(i, j int) bool { return l.FileGuards[i].Name < l.FileGuards[j].Name })
	sort.Slice(l.Gates, func(i, j int) bool { return l.Gates[i].Name < l.Gates[j].Name })
	sort.Slice(l.Contexts, func(i, j int) bool { return l.Contexts[i].Name < l.Contexts[j].Name })
	sort.Slice(l.Goals, func(i, j int) bool { return l.Goals[i].Name < l.Goals[j].Name })
	sort.Slice(l.Invalid, func(i, j int) bool { return l.Invalid[i].Qualified() < l.Invalid[j].Qualified() })
}
