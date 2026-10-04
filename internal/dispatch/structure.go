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
// StructureGateDeclaration): allowlists of paths that may be written, DENY BY
// DEFAULT. It is a primitive rather than a rule nature — one `structure.yaml` per
// root, not a per-name folder — so it lives here as a small path-matcher rather
// than going through the require/checks runner.
//
// Two layers. StructureGate is ONE compiled structure.yaml and its allow/deny
// rule; StructureSet combines every loaded one — the project's (whole tree) and
// each plugin's (its declared `scope` only) — and decides a written path by
// ownership (see StructureSet.Decide).
//
// One structure gate's rule, exactly:
//
//   - a write to a path that matches NO `allow` entry is DENIED (deny by default);
//   - a write to a path that matches an `allow` entry is ALLOWED;
//   - unless it also matches a `deny` entry, which carves an exception BACK OUT of
//     allow — so `deny` only ever subtracts from what `allow` permitted, and a
//     `deny` entry matching nothing already-allowed is a no-op.
//
// `allow`/`deny` entries are `{glob}` XOR `{regex}` (the loader guarantees exactly
// one). A glob is matched with the SAME glob machinery the file-guard scope uses
// (guardrail.CompileFileMatch, so `**`/`*`/`?`/classes and anchoring behave
// identically to a file-guard's bare-glob match); a regex is Go's RE2, matched as
// the author wrote it (the spec's examples anchor with `^…$` themselves).

// StructureGate is a compiled structure gate: the allow and deny entries turned
// into path matchers once, so a malformed pattern is a construction error rather
// than a per-path surprise.
//
// Built from a declaration.StructureGate (already loaded and validated), it is the
// runtime form the dispatch calls Allows on for each written path. The loader
// already refuses a bad glob/regex, so CompileStructureGate should not fail for a
// declaration that loaded — but it returns an error rather than panicking, because
// a compile that disagrees with the load check must surface loudly, not decide
// enforcement silently.
type StructureGate struct {
	allow []pathMatcher
	deny  []pathMatcher
}

// pathMatcher decides whether one path matches one allow/deny entry. A tiny
// interface over the two entry kinds, so Allows walks a uniform list.
type pathMatcher interface {
	matches(path string) (bool, error)
}

// CompileStructureGate turns a loaded structure declaration into a runtime matcher.
//
// Each entry is compiled by its kind: a glob through the file-guard glob machinery
// (so structure globs and file-guard globs are the same grammar and anchoring), a
// regex through Go's regexp. An entry setting neither — impossible for a loaded
// declaration — is a construction error rather than a silently skipped entry.
func CompileStructureGate(sg declaration.StructureGate) (*StructureGate, error) {
	allow, err := compileEntries(sg.Allow, "allow")
	if err != nil {
		return nil, err
	}
	deny, err := compileEntries(sg.Deny, "deny")
	if err != nil {
		return nil, err
	}
	return &StructureGate{allow: allow, deny: deny}, nil
}

// compileEntries compiles one list (allow or deny) of structure entries.
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

// Allows reports whether a write to path is permitted by this structure gate, and
// on a refusal why.
//
// Deny-by-default: a path allowed by no `allow` entry is refused. A path that IS
// allowed is then checked against `deny`, which carves exceptions back out — a
// match there refuses. So the order is: must match some allow, must match no deny.
//
// A matcher that ERRORS (a glob handed a non-string path, say — not reachable
// here since path is a string, but the interface admits it) is treated as a
// refusal for that decision, fail-closed: an allow entry that could not be
// evaluated has not established that the path is allowed, and a deny entry that
// could not be evaluated is treated as matching, because the engine cannot show
// the path is outside the exception. Both directions refuse, which is the safe way
// to be wrong about a write.
func (sg *StructureGate) Allows(path string) (allowed bool, reason string) {
	matchedAllow := false
	for _, m := range sg.allow {
		ok, err := m.matches(path)
		if err != nil {
			// An allow entry that could not be evaluated cannot establish the path
			// is allowed. Skip it — if no other allow entry matches, the path is
			// denied by default, which is the fail-closed direction.
			continue
		}
		if ok {
			matchedAllow = true
			break
		}
	}
	if !matchedAllow {
		return false, structureDenyReason(path)
	}

	// Allowed by an allow entry; now a deny exception can still carve it out.
	for _, m := range sg.deny {
		ok, err := m.matches(path)
		if err != nil {
			// A deny entry that could not be evaluated is treated as matching:
			// the engine cannot show the path is outside the exception, so it
			// refuses rather than admit on an unevaluated deny.
			return false, structureDenyExceptionReason(path)
		}
		if ok {
			return false, structureDenyExceptionReason(path)
		}
	}
	return true, ""
}

// matchesDeny reports whether any `deny` entry matches path. A deny entry that
// errors is treated as matching — fail-closed, as in Allows.
func (sg *StructureGate) matchesDeny(path string) bool {
	for _, m := range sg.deny {
		ok, err := m.matches(path)
		if err != nil || ok {
			return true
		}
	}
	return false
}

// matchesAllow reports whether any `allow` entry matches path. An allow entry
// that errors establishes nothing — fail-closed, as in Allows.
func (sg *StructureGate) matchesAllow(path string) bool {
	for _, m := range sg.allow {
		if ok, err := m.matches(path); err == nil && ok {
			return true
		}
	}
	return false
}

// structureDenyReason is the refusal for a path allowed by nothing.
func structureDenyReason(path string) string {
	return fmt.Sprintf(
		"writing to %q is not allowed by this project's structure gate — the tree is deny-by-default, and this path matches no `allow` entry in .sloprail/file-guard/structure.yaml. "+
			"Write under an allowed path, or add an `allow` entry for this location if it should be permitted.",
		path)
}

// structureDenyExceptionReason is the refusal for a path an `allow` entry matched
// but a `deny` exception carved back out.
func structureDenyExceptionReason(path string) string {
	return fmt.Sprintf(
		"writing to %q is blocked by a `deny` exception in this project's structure gate (.sloprail/file-guard/structure.yaml) — the path is under an allowed area but explicitly excluded from it.",
		path)
}

// -- combining every loaded structure gate --

// StructureSet is every loaded structure gate compiled together: the project's
// own (whole tree), if any, and each plugin's with the folders its `scope` owns.
type StructureSet struct {
	project *StructureGate
	plugins []pluginStructure
}

// pluginStructure is one plugin's compiled structure gate and its scope.
type pluginStructure struct {
	decl   declaration.StructureGate
	gate   *StructureGate
	scopes []scopeMatcher
}

// scopeMatcher is one compiled scope entry: the glob as the plugin wrote it (for
// messages) and a matcher over FOLDER paths (the glob less its trailing `/`).
type scopeMatcher struct {
	glob   string
	folder pathMatcher
}

// CompileStructureSet compiles every loaded structure gate. A project gate is the
// one with no plugin origin; every other one must carry a scope (the loader
// already refused any that do not).
func CompileStructureSet(gates []declaration.StructureGate) (*StructureSet, error) {
	set := &StructureSet{}
	for _, d := range gates {
		g, err := CompileStructureGate(d)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", d.Describe(), err)
		}
		if !d.Origin.FromPlugin() {
			set.project = g
			continue
		}
		ps := pluginStructure{decl: d, gate: g}
		for i, e := range d.Scope {
			m, err := guardrail.CompileFileMatch(declaration.ScopeFolder(e.Glob))
			if err != nil {
				return nil, fmt.Errorf("%s: scope entry %d: %w", d.Describe(), i, err)
			}
			ps.scopes = append(ps.scopes, scopeMatcher{glob: e.Glob, folder: globMatcher{m: m}})
		}
		set.plugins = append(set.plugins, ps)
	}
	return set, nil
}

// Empty reports whether the set holds no structure gate at all.
func (s *StructureSet) Empty() bool { return s.project == nil && len(s.plugins) == 0 }

// owner is a plugin whose scope a path lies in, and the scope entry that matched.
type owner struct {
	p     *pluginStructure
	scope string
}

// ownerOf reports the scope entry of this plugin that owns path, or "" if none:
// path lies under a folder the scope glob denotes when one of its ANCESTOR
// folders matches the folder glob. The path itself is not a folder here — a file
// named `.mdmap` is not inside `.mdmap/`.
//
// A scope matcher that errors counts as owning the path: then the plugin's
// stricter, scoped rule applies rather than the path escaping it.
func (p *pluginStructure) ownerOf(path string) string {
	for i := 1; i < len(path); i++ {
		if path[i] != '/' {
			continue
		}
		folder := path[:i]
		for _, sc := range p.scopes {
			if ok, err := sc.folder.matches(folder); err != nil || ok {
				return sc.glob
			}
		}
	}
	return ""
}

// Decide reports whether a write to path is permitted by the combined structure
// gates, and on a refusal why — naming the source that decided.
//
// The order, exactly:
//
//  1. OWNERS are the plugins whose scope the path lies in. More than one → refuse,
//     naming every owner (an ownership conflict: nobody can say which rule is
//     meant, so neither is guessed).
//  2. Exactly one owner: the project's `deny` still VETOES (the project always
//     keeps the last word on its own tree); otherwise the owner decides — its
//     `allow` must match and its `deny` must not. The project's `allow` does NOT
//     widen an owned scope.
//  3. No owner: the project's structure decides exactly as it always has (allow
//     must match, deny must not); with no project structure the write is
//     permitted — a plugin can lock down only its own scope.
func (s *StructureSet) Decide(path string) (bool, string) {
	var owners []owner
	for i := range s.plugins {
		if sc := s.plugins[i].ownerOf(path); sc != "" {
			owners = append(owners, owner{p: &s.plugins[i], scope: sc})
		}
	}

	switch {
	case len(owners) > 1:
		return false, ownershipConflictReason(path, owners)
	case len(owners) == 1:
		o := owners[0]
		if s.project != nil && s.project.matchesDeny(path) {
			return false, projectVetoReason(path, o)
		}
		if !o.p.gate.matchesAllow(path) {
			return false, pluginDenyReason(path, o)
		}
		if o.p.gate.matchesDeny(path) {
			return false, pluginDenyExceptionReason(path, o)
		}
		return true, ""
	case s.project != nil:
		return s.project.Allows(path)
	default:
		return true, ""
	}
}

// Deciders names the structure gates that decide a write to path, by their disable key
// (`<plugin>/structure`, `structure` for the project's): the owning plugin (every owner, in a
// conflict), else the project's own. Empty when no gate has a say.
func (s *StructureSet) Deciders(path string) []string {
	var out []string
	for i := range s.plugins {
		if s.plugins[i].ownerOf(path) != "" {
			out = append(out, s.plugins[i].decl.Qualified())
		}
	}
	if len(out) == 0 && s.project != nil {
		out = append(out, "structure")
	}
	return out
}

// describeOwner names a plugin structure and the scope that matched, for a
// refusal: `plugin "mdmap"'s structure gate (scope ".mdmap/", <path>)`.
func describeOwner(o owner) string {
	return fmt.Sprintf("%s (scope %q, %s)", o.p.decl.Describe(), o.scope, o.p.decl.Path())
}

// ownershipConflictReason is the refusal for a path two or more plugins' scopes
// claim.
func ownershipConflictReason(path string, owners []owner) string {
	names := make([]string, 0, len(owners))
	keys := make([]string, 0, len(owners))
	for _, o := range owners {
		names = append(names, describeOwner(o))
		keys = append(keys, o.p.decl.Qualified())
	}
	return fmt.Sprintf(
		"writing to %q is refused: ownership conflict — it lies in the scope of more than one plugin's structure gate: %s. "+
			"No single rule can decide it; disable all but one of them in .sloprail/config.yaml (`disabled: [%s]`).",
		path, strings.Join(names, "; "), strings.Join(keys, ", "))
}

// projectVetoReason is the refusal for a path inside a plugin's scope that the
// project's own structure gate denies.
func projectVetoReason(path string, o owner) string {
	return fmt.Sprintf(
		"writing to %q is blocked by a `deny` entry in this project's structure gate (.sloprail/file-guard/structure.yaml) — "+
			"the path is owned by %s, but the project's deny always has the last word.",
		path, describeOwner(o))
}

// pluginDenyReason is the refusal for a path inside a plugin's scope that its
// allowlist does not cover.
func pluginDenyReason(path string, o owner) string {
	return fmt.Sprintf(
		"writing to %q is not allowed by %s, which owns this part of the tree — it is deny-by-default there, and this path matches no `allow` entry. "+
			"Write where that plugin's structure allows%s; the project's own `allow` does not widen a plugin's scope.",
		path, describeOwner(o), allowedShapes(o))
}

// allowedShapes lists the owning plugin's `allow` entries that could lie in the
// scope the path fell under, so the agent sees what a path there must look like
// — the plugin's structure.yaml sits in its install, not in the project.
func allowedShapes(o owner) string {
	folder := strings.TrimSuffix(o.scope, "/")
	var shapes []string
	for _, e := range o.p.decl.Allow {
		switch {
		case e.Glob != "" && strings.HasPrefix(e.Glob, folder):
			shapes = append(shapes, "glob "+e.Glob)
		case e.Regex != "" && strings.Contains(e.Regex, regexp.QuoteMeta(folder)):
			shapes = append(shapes, "regex "+e.Regex)
		}
	}
	if len(shapes) == 0 {
		return ""
	}
	return " — there, a path must match one of: " + strings.Join(shapes, ", ")
}

// pluginDenyExceptionReason is the refusal for a path a plugin's allow covers but
// its deny carves back out.
func pluginDenyExceptionReason(path string, o owner) string {
	return fmt.Sprintf(
		"writing to %q is blocked by a `deny` exception in %s — the path is under an allowed area but explicitly excluded from it.",
		path, describeOwner(o))
}
