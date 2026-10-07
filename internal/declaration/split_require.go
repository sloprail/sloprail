package declaration

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// A file-guard judges committed bytes at Stop, so it can see nothing of the
// session: a `require: skill` or `require: context` is a fact about the
// transcript / session state, which only a gate (live, at the event) can read.
// `require: citation` stays legal on a file-guard: a citation rides on a commit
// trailer, which the committed range carries.

// contextInFileGuardMatch is the refusal for a file-guard `match` that reads `context`.
// The match scope has no `context` variable, so the expression fails to compile on
// that name and ValidateFileGuard swaps the compile error for this advice.
const contextInFileGuardMatch = "a file-guard's match cannot read `context`: a file-guard judges committed bytes (in CI, with no session) " +
	"and a context is session state — move the condition on the context to a gate " +
	"(.sloprail/gate/<name>/gate.yaml, whose match reads `context`), and leave the file-guard to select files by what the commits hold"

// transcriptDependent reports whether a prerequisite reads the session (a skill
// loaded, a context active) and so may live only on a gate.
func (p Prerequisite) transcriptDependent() bool { return p.Skill != "" || p.Context != "" }

func yamlScalar(s string) string {
	b, _ := yaml.Marshal(s)
	return strings.TrimSpace(string(b))
}

// validateNoTranscriptRequire refuses a file-guard `require:` entry that reads
// the session, and shows the gate to write instead. The match is not restated:
// a gate reads the event under `event`, so the author writes the file-guard's
// match with `path` as `event.path` (a glob as `event.path matches "<regex>"`).
// sr:invariant loading/retired-file-guard-keys-are-refused
func validateNoTranscriptRequire(g FileGuard) []Problem {
	var moved []string
	for _, r := range g.Require {
		if !r.transcriptDependent() {
			continue
		}
		var b strings.Builder
		if r.Skill != "" {
			fmt.Fprintf(&b, "  - skill: %s\n", yamlScalar(r.Skill))
			if len(r.Files) > 0 {
				qs := make([]string, len(r.Files))
				for i, f := range r.Files {
					qs[i] = yamlScalar(f)
				}
				fmt.Fprintf(&b, "    files: [%s]\n", strings.Join(qs, ", "))
			}
		} else {
			fmt.Fprintf(&b, "  - context: %s\n", yamlScalar(r.Context))
		}
		moved = append(moved, b.String())
	}
	if len(moved) == 0 {
		return nil
	}
	return []Problem{prob(ErrRetiredKey, "require",
		"a file-guard judges committed bytes at Stop and cannot see the session, so `require: skill` / `require: context` is not allowed here — "+
			"move it to a gate. Write .sloprail/gate/<name>/gate.yaml containing:\n"+
			"on:\n  - event: PreFileWrite\n    match: <this file-guard's match, with `path` written `event.path`>\nrequire:\n%s"+
			"and delete those entries from this file-guard (`require: citation` may stay: it reads the commit's trailers)",
		strings.Join(moved, ""))}
}
