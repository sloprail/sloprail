package dispatch

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/guardrail"
)

// This file is the STRUCTURE-GATE primitive (dot-dir-file-store/main.tsp
// StructureGateDeclaration): a per-file allowlist of paths a project (or a
// plugin it installed) may write under, DENY BY DEFAULT over the paths that file
// COVERS. It is a primitive rather than a rule nature — one `structure.yaml` per
// `.sloprail` root, not a per-name folder — so it lives here as a small
// path-matcher rather than going through the require/checks runner.
//
// # Composition across covering files
//
// A load can produce several declaration.StructureGate values at once: the
// project's own (if it ships one) and each enabled plugin's. Each one COVERS a
// slice of the tree named by its `scope` (empty scope = the whole tree, valid
// only for a project's own — see declaration.ValidateStructureGate). For a
// write to path P:
//
//   - the COVERING files are every structure gate whose scope matches P (or
//     which is unscoped);
//   - if NO file covers P, no structure gate has an opinion and the write is
//     permitted — composition never makes a path MORE restricted than the set
//     of files that actually speak about it;
//   - otherwise P is allowed iff AT LEAST ONE covering file's `allow` matches P
//     AND NO covering file's `deny` matches P — the union of every covering
//     file's allow, minus the union of every covering file's deny.
//
// So a project's own allowlist does not need to list a plugin-owned shape: the
// plugin's own `allow` covers it, and the two are combined rather than one
// displacing the other. This is the change from the old singleton structure
// gate, which picked ONE winning file per project and Shadowed the rest — see
// declaration.Loaded.Structures and resolveStructures for why that regressed a
// plugin's own allowlist the moment a project declared one of its own.
//
// `allow`/`deny`/`scope` entries are `{glob}` XOR `{regex}` (the loader
// guarantees exactly one). A glob is matched with the SAME glob machinery the
// file-guard scope uses (guardrail.CompileFileMatch, so `**`/`*`/`?`/classes and
// anchoring behave identically to a file-guard's bare-glob match); a regex is
// Go's RE2, matched as the author wrote it (the spec's examples anchor with
// `^…$` themselves).

// ComposedStructureGate is every structure gate in force, compiled once, ready
// to decide a write across all of them together.
type ComposedStructureGate struct {
	gates []compiledStructureGate
}

// compiledStructureGate is one declaration.StructureGate compiled to runtime
// matchers, plus the SOURCE a refusal names — this gate's Qualified() and where
// an author would add an `allow` entry (its Dir, joined with the structure.yaml
// filename).
type compiledStructureGate struct {
	scope []pathMatcher // nil/empty means "covers the whole tree"
	allow []pathMatcher
	deny  []pathMatcher

	// qualified is this gate's disable key (declaration.StructureGate.Qualified),
	// named in a refusal so a reader knows which file covered the path.
	qualified string
	// path is where this structure.yaml sits, for "add an allow entry here".
	path string
}

// pathMatcher decides whether one path matches one scope/allow/deny entry. A
// tiny interface over the two entry kinds, so a lookup walks a uniform list.
type pathMatcher interface {
	matches(path string) (bool, error)
}

// CompileStructureGates turns every loaded structure declaration into a single
// composed runtime matcher.
//
// Each entry is compiled by its kind: a glob through the file-guard glob
// machinery (so structure globs and file-guard globs are the same grammar and
// anchoring), a regex through Go's regexp. An entry setting neither —
// impossible for a loaded declaration — is a construction error rather than a
// silently skipped entry.
func CompileStructureGates(sgs []declaration.StructureGate) (*ComposedStructureGate, error) {
	gates := make([]compiledStructureGate, 0, len(sgs))
	for _, sg := range sgs {
		scope, err := compileEntries(sg.Scope, "scope")
		if err != nil {
			return nil, err
		}
		allow, err := compileEntries(sg.Allow, "allow")
		if err != nil {
			return nil, err
		}
		deny, err := compileEntries(sg.Deny, "deny")
		if err != nil {
			return nil, err
		}
		gates = append(gates, compiledStructureGate{
			scope:     scope,
			allow:     allow,
			deny:      deny,
			qualified: sg.Qualified(),
			path:      structureFilePath(sg),
		})
	}
	return &ComposedStructureGate{gates: gates}, nil
}

// structureFilePath renders where a structure gate's file sits, for a refusal
// to point an author at. sg.Dir is `<root>/file-guard`; the file itself is the
// `structure.yaml` sibling of the per-guard folders.
func structureFilePath(sg declaration.StructureGate) string {
	if sg.Dir == "" {
		return "file-guard/structure.yaml"
	}
	return sg.Dir + "/structure.yaml"
}

// compileEntries compiles one list (scope, allow, or deny) of structure entries.
func compileEntries(entries []declaration.StructureEntry, which string) ([]pathMatcher, error) {
	out := make([]pathMatcher, 0, len(entries))
	for i, e := range entries {
		m, err := compileEntry(e)
		if err != nil {
			return nil, fmt.Errorf("structure gate: %s entry %d: %w", which, i, err)
		}
		out = append(out, m)
	}
	return out, nil
}

// compileEntry compiles one entry by its set field.
func compileEntry(e declaration.StructureEntry) (pathMatcher, error) {
	switch {
	case e.Glob != "":
		// The file-guard glob machinery: a bare glob compiles to a `glob(path)`
		// matcher reading `path` off a flat event. Reused so a structure glob and a
		// file-guard glob mean the same thing.
		m, err := guardrail.CompileFileMatch(e.Glob)
		if err != nil {
			return nil, err
		}
		return globMatcher{m: m}, nil
	case e.Regex != "":
		re, err := regexp.Compile(e.Regex)
		if err != nil {
			return nil, err
		}
		return regexMatcher{re: re}, nil
	default:
		return nil, fmt.Errorf("entry sets neither glob nor regex")
	}
}

// globMatcher matches a path with a compiled file-guard glob.
type globMatcher struct{ m *guardrail.Matcher }

func (g globMatcher) matches(path string) (bool, error) {
	// The file-guard glob reads `path` off a flat event's fields — the same shape
	// CompileFileMatch's own tests drive it with.
	return g.m.Match(event.Event{Kind: "PreFileCreate", Fields: map[string]any{"path": path}})
}

// regexMatcher matches a path with a compiled Go regexp.
type regexMatcher struct{ re *regexp.Regexp }

func (r regexMatcher) matches(path string) (bool, error) {
	return r.re.MatchString(path), nil
}

// covers reports whether this gate has an opinion about path — its scope is
// empty (the whole tree) or one of its scope entries matches. A matcher that
// ERRORS is treated as NOT covering: a scope entry that could not be evaluated
// has not established that this gate owns the path, and the safer direction for
// "does this gate get a say" is to leave it out rather than pull an unrelated
// path under a gate that could not confirm it owns it.
func (g compiledStructureGate) covers(path string) bool {
	if len(g.scope) == 0 {
		return true
	}
	for _, m := range g.scope {
		if ok, err := m.matches(path); err == nil && ok {
			return true
		}
	}
	return false
}

// allows reports whether this gate's OWN allow/deny say a path (already known to
// be within its scope) may be written. Mirrors the single-gate Allows semantics
// this composed type replaces: must match some allow, must match no deny, both
// directions fail closed on a matcher error.
func (g compiledStructureGate) allows(path string) (matchedAllow, matchedDeny bool) {
	for _, m := range g.allow {
		if ok, err := m.matches(path); err == nil && ok {
			matchedAllow = true
			break
		}
	}
	for _, m := range g.deny {
		ok, err := m.matches(path)
		if err != nil {
			// A deny entry that could not be evaluated is treated as matching —
			// fail closed, the same reasoning the single-gate version used.
			matchedDeny = true
			break
		}
		if ok {
			matchedDeny = true
			break
		}
	}
	return matchedAllow, matchedDeny
}

// Allows reports whether a write to path is permitted across every structure
// gate in force, and on a refusal why.
//
// The rule (see the package doc): find every COVERING gate (scope matches, or
// unscoped). No covering gate means no opinion — permitted. Otherwise the path
// is allowed iff some covering gate's allow matches AND no covering gate's deny
// does; the union of allows minus the union of denies among the covering gates.
func (c *ComposedStructureGate) Allows(path string) (allowed bool, reason string) {
	var covering []compiledStructureGate
	for _, g := range c.gates {
		if g.covers(path) {
			covering = append(covering, g)
		}
	}
	if len(covering) == 0 {
		// No structure gate has an opinion about this path — permitted.
		return true, ""
	}

	var (
		matchedAllow bool
		deniedBy     []compiledStructureGate
	)
	for _, g := range covering {
		allow, deny := g.allows(path)
		if allow {
			matchedAllow = true
		}
		if deny {
			deniedBy = append(deniedBy, g)
		}
	}

	if len(deniedBy) > 0 {
		return false, structureDenyExceptionReason(path, deniedBy)
	}
	if !matchedAllow {
		return false, structureDenyReason(path, covering)
	}
	return true, ""
}

// coveringNames renders the covering gates' qualified names and paths for a
// refusal, so a reader knows which file(s) to look at.
func coveringNames(gates []compiledStructureGate) string {
	parts := make([]string, 0, len(gates))
	for _, g := range gates {
		parts = append(parts, fmt.Sprintf("%s (%s)", g.qualified, g.path))
	}
	return strings.Join(parts, ", ")
}

// structureDenyReason is the refusal for a path covered by at least one
// structure gate but allowed by none of them.
func structureDenyReason(path string, covering []compiledStructureGate) string {
	return fmt.Sprintf(
		"writing to %q is not allowed by this project's structure gate — the covering file(s) are deny-by-default "+
			"over the paths they own, and this path matches no `allow` entry in any of them: %s. "+
			"Write under an allowed path, or add an `allow` entry for this location to one of the covering files.",
		path, coveringNames(covering))
}

// structureDenyExceptionReason is the refusal for a path an `allow` entry
// matched (in some covering file) but a `deny` exception in a covering file
// carved back out.
func structureDenyExceptionReason(path string, deniedBy []compiledStructureGate) string {
	return fmt.Sprintf(
		"writing to %q is blocked by a `deny` exception in this project's structure gate — the path is under an "+
			"allowed area but explicitly excluded by: %s.",
		path, coveringNames(deniedBy))
}
