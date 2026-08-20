package dispatch

import (
	"fmt"
	"regexp"

	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/guardrail"
)

// This file is the STRUCTURE-GATE primitive (dot-dir-file-store/main.tsp
// StructureGateDeclaration): one tree-wide allowlist of paths a project may write
// under, DENY BY DEFAULT. It is a primitive rather than a rule nature — a single
// `structure.yaml` for the whole project, not a per-name folder — so it lives here
// as a small path-matcher rather than going through the require/checks runner.
//
// The rule, exactly:
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
