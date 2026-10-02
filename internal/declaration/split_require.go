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
