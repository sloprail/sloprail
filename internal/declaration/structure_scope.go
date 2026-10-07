package declaration

import (
	"fmt"
	"regexp/syntax"
	"strings"

	"github.com/sloprail/sloprail/internal/guardrail"
)

// This file is the load-time half of structure-gate OWNERSHIP: what a `scope` is,
// who may declare one, and the checks that can be made before any path is
// written. The write-time half — which structure decides a given path — lives in
// internal/dispatch (StructureSet.Decide).
//
// # Why scopes exist
//
// The structure gate used to be a singleton: the first root declaring one won and
// the rest were shadowed, so a plugin that wanted to keep its own folder tidy
// either replaced the project's whole-tree allowlist or silently did nothing. A
// scope lets every structure load at once without fighting: the project's covers
// the whole tree, and each plugin's covers only the folders it declares it owns.

// ScopeFolder is the folder glob a scope entry denotes — the glob as written, less
// its trailing `/`. `.mdmap/` denotes the folder `.mdmap`; `**/.adr/` denotes every
// folder named `.adr` at any depth. A written path lies in the scope when one of
// its ANCESTOR folders matches this glob.
func ScopeFolder(glob string) string { return strings.TrimSuffix(glob, "/") }

// IsLiteralScope reports whether a scope glob names one fixed folder — no glob
// metacharacters — so it can be compared with a plain string prefix. Only literal
// scopes take part in the load-time checks (entries-inside-scope, overlap between
// plugins); a wildcard scope's extent is not knowable before a path arrives.
func IsLiteralScope(glob string) bool { return !strings.ContainsAny(glob, "*?[") }

// validateStructureScope checks the `scope` rules, which differ by origin:
//
//   - a PROJECT's structure must not declare a scope — it covers the whole tree;
//   - a PLUGIN's structure must declare a non-empty one, every entry a glob that
//     names a folder (trailing `/`), is relative, and does not cover the whole
//     tree;
//   - when every scope entry is a LITERAL folder, every allow/deny entry must lie
//     inside one of them. With a wildcard scope this cannot be decided at load, so
//     it is skipped — an entry outside the scope is inert at write time anyway.
//
// sr:invariant loading/structure-scope-by-owner
func validateStructureScope(s StructureGate) []Problem {
	if !s.Origin.FromPlugin() {
		if len(s.Scope) > 0 {
			return []Problem{prob(ErrBadScope, "scope",
				"a project's own structure gate covers the whole tree and must not declare a `scope` — "+
					"`scope` is for a plugin's structure.yaml, naming the folders that plugin owns; remove it")}
		}
		return nil
	}

	if len(s.Scope) == 0 {
		return []Problem{prob(ErrMissingField, "scope",
			"a plugin's structure gate must declare a `scope` — the folders it owns, e.g. `scope: [{glob: \".mdmap/\"}]`. "+
				"Only the project's own structure gate may govern the whole tree")}
	}

	var problems []Problem
	for i, e := range s.Scope {
		problems = append(problems, validateScopeEntry(e, fmt.Sprintf("scope %d", i))...)
	}
	if len(problems) > 0 {
		// A scope that is itself wrong cannot be checked against; report it alone
		// rather than a cascade of "outside the scope" faults that vanish once it
		// is fixed.
		return problems
	}

	for _, e := range s.Scope {
		if !IsLiteralScope(e.Glob) {
			return nil
		}
	}
	scopes := s.ScopeGlobs()
	for i, e := range s.Allow {
		problems = append(problems, entryInsideScope(e, scopes, fmt.Sprintf("allow %d", i))...)
	}
	for i, e := range s.Deny {
		problems = append(problems, entryInsideScope(e, scopes, fmt.Sprintf("deny %d", i))...)
	}
	return problems
}

// validateScopeEntry checks one scope entry: a glob (not a regex), naming a
// folder, relative to the project root, not covering the whole tree, and
// compiling as a path glob.
func validateScopeEntry(e StructureEntry, where string) []Problem {
	if e.isRegex() {
		return []Problem{prob(ErrBadScope, where,
			"a scope entry must be a `glob` naming a folder — `regex` scopes are not supported")}
	}
	if !e.isGlob() {
		return []Problem{prob(ErrBadScope, where,
			"a scope entry must set `glob` to the folder the plugin owns, e.g. \".mdmap/\"")}
	}
	g := e.Glob
	if coversWholeTree(g) {
		return []Problem{prob(ErrBadScope, where,
			"scope %q covers the whole tree — a plugin may own only part of it; name the folder(s) it owns, e.g. \".mdmap/\"", g)}
	}
	if !strings.HasSuffix(g, "/") {
		return []Problem{prob(ErrBadScope, where,
			"scope %q must name a folder: end it with `/` (%q)", g, g+"/")}
	}
	if strings.HasPrefix(g, "/") || strings.HasPrefix(g, "./") {
		return []Problem{prob(ErrBadScope, where,
			"scope %q must be relative to the project root, with no leading `/` or `./` — written paths never carry one, so it would never match", g)}
	}
	if strings.ContainsAny(g, " \t\n\"'") {
		// CompileFileMatch would read such a string as an expression, not a glob.
		return []Problem{prob(ErrBadScope, where,
			"scope %q must be a plain path glob, with no whitespace or quotes", g)}
	}
	if _, err := guardrail.CompileFileMatch(ScopeFolder(g)); err != nil {
		return []Problem{prob(ErrBadMatch, where, "glob does not compile: %s", oneLine(err.Error()))}
	}
	return nil
}

// coversWholeTree reports whether a scope glob names no particular folder: once
// the trailing `/` is dropped, nothing but wildcards and separators remain (the
// empty glob, `/`, `*/`, `**/`, `**`, `**/*/`, `?*/` …). Such a scope matches every
// top-level folder, which is the project's structure gate's job, not a plugin's.
func coversWholeTree(glob string) bool {
	return strings.Trim(ScopeFolder(glob), "*?/") == ""
}

// entryInsideScope checks that one allow/deny entry can only match paths inside
// one of the (literal) scope folders.
//
// A glob is inside when it starts with a scope folder (`.mdmap/…`). A regex is
// inside when it is anchored at the start (`^`) and its leading literal text
// starts with a scope folder (`^\.mdmap/…`); an unanchored regex can match
// anywhere in the tree and so is not inside.
func entryInsideScope(e StructureEntry, scopes []string, where string) []Problem {
	var lead string
	switch {
	case e.isGlob():
		lead = e.Glob
	case e.isRegex():
		prefix, anchored := regexLiteralPrefix(e.Regex)
		if !anchored {
			return []Problem{prob(ErrOutsideScope, where,
				"regex %q is not anchored with `^`, so it can match outside this plugin's scope (%s); anchor it and start it with a scope folder",
				e.Regex, strings.Join(scopes, ", "))}
		}
		lead = prefix
	default:
		return nil // exactly-one-of is reported by validateStructureEntry
	}
	for _, sc := range scopes {
		if strings.HasPrefix(lead, sc) {
			return nil
		}
	}
	pattern := e.Glob
	if e.isRegex() {
		pattern = e.Regex
	}
	return []Problem{prob(ErrOutsideScope, where,
		"%q lies outside this plugin's scope (%s) — a plugin's structure may only allow or deny paths inside the folders it owns; start the pattern with one of them",
		pattern, strings.Join(scopes, ", "))}
}

// regexLiteralPrefix returns the literal text a regex must begin with, and
// whether it is anchored to the start of the path at all. `^\.mdmap/x/.*` →
// (".mdmap/x/", true); `\.mdmap/.*` → ("", false). A regex that does not parse
// is reported elsewhere (validateStructureEntry); here it is simply not anchored.
func regexLiteralPrefix(re string) (string, bool) {
	r, err := syntax.Parse(re, syntax.Perl)
	if err != nil {
		return "", false
	}
	r = r.Simplify()
	subs := []*syntax.Regexp{r}
	if r.Op == syntax.OpConcat {
		subs = r.Sub
	}
	if len(subs) == 0 || subs[0].Op != syntax.OpBeginText {
		return "", false
	}
	var b strings.Builder
	for _, s := range subs[1:] {
		if s.Op != syntax.OpLiteral || s.Flags&syntax.FoldCase != 0 {
			break
		}
		b.WriteString(string(s.Rune))
	}
	return b.String(), true
}

// ScopeOverlap is two plugins whose LITERAL scopes overlap — the same folder, or
// one inside the other. Both structures stay loaded; a write inside the overlap
// is refused at write time as an ownership conflict. Reported at load so the
// conflict is visible before an agent meets it.
type ScopeOverlap struct {
	// A and B are the two plugins, in load (precedence) order.
	A, B Origin

	// ScopeA and ScopeB are the overlapping scope globs as each plugin wrote them.
	ScopeA, ScopeB string
}

// Message renders an overlap for a person, naming both plugins, both scopes, and
// how to resolve it.
func (o ScopeOverlap) Message() string {
	return fmt.Sprintf(
		"the structure gates of plugin %q (scope %q) and plugin %q (scope %q) both claim the same part of the tree, "+
			"so every write there is refused as an ownership conflict. "+
			"Disable one of them with `disabled: [%s]` (or `%s`) in %s.",
		o.A.Plugin, o.ScopeA, o.B.Plugin, o.ScopeB,
		o.A.Qualified(NatureStructure, ""), o.B.Qualified(NatureStructure, ""), dotDirName+"/"+configFile)
}

// literalScopeOverlaps finds every pair of plugin structures whose literal scopes
// overlap (equal, or one a folder-prefix of the other). Wildcard scopes are not
// compared — their overlap is not decidable here, and the write-time rule refuses
// in any overlap regardless.
func literalScopeOverlaps(structures []StructureGate) []ScopeOverlap {
	var out []ScopeOverlap
	for i := 0; i < len(structures); i++ {
		a := structures[i]
		if !a.Origin.FromPlugin() {
			continue
		}
		for j := i + 1; j < len(structures); j++ {
			b := structures[j]
			if !b.Origin.FromPlugin() {
				continue
			}
			for _, sa := range a.Scope {
				if !IsLiteralScope(sa.Glob) {
					continue
				}
				for _, sb := range b.Scope {
					if !IsLiteralScope(sb.Glob) {
						continue
					}
					if strings.HasPrefix(sa.Glob, sb.Glob) || strings.HasPrefix(sb.Glob, sa.Glob) {
						out = append(out, ScopeOverlap{A: a.Origin, B: b.Origin, ScopeA: sa.Glob, ScopeB: sb.Glob})
					}
				}
			}
		}
	}
	return out
}
