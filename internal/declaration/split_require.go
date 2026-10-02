package declaration

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"

	"github.com/sloprail/sloprail/internal/guardrail"
)

// A file-guard judges committed bytes at Stop, so it can see nothing of the
// session: a `require: skill` or `require: context` is a fact about the
// transcript / session state, which only a gate (live, at the event) can read.
// `require: citation` stays legal on a file-guard: a citation rides on a commit
// trailer, which the committed range carries.

// TranscriptDependent reports whether a prerequisite reads the session (a skill
// loaded, a context active) and so may live only on a gate.
func (p Prerequisite) TranscriptDependent() bool { return p.Skill != "" || p.Context != "" }

// ReplacementGateName is the folder a converted file-guard's gate is written to.
func ReplacementGateName(guardName string) string { return guardName + "-requires" }

// GateMatchFromFileMatch restates a file-guard `match` (a glob or a bare-name
// expression) in the gate scope: `path` becomes `event.path`. Facts a PreFileWrite
// event does not carry the same way (status, trailers, markers) are an error, so
// the caller falls back to writing the gate by hand rather than guessing.
func GateMatchFromFileMatch(src string) (string, error) {
	src = strings.TrimSpace(src)
	if src == "" {
		return "", nil
	}
	if looksLikeGlobSrc(src) {
		re, err := guardrail.GlobRegexSource(src)
		if err != nil {
			return "", err
		}
		lit, _ := json.Marshal(re)
		return "event.path matches " + string(lit), nil
	}
	var out strings.Builder
	rs := []rune(src)
	for i := 0; i < len(rs); {
		c := rs[i]
		switch {
		case c == '"' || c == '\'':
			j := i + 1
			for j < len(rs) && rs[j] != c {
				if rs[j] == '\\' {
					j++
				}
				j++
			}
			if j >= len(rs) {
				return "", fmt.Errorf("unterminated string in match %q", src)
			}
			out.WriteString(string(rs[i : j+1]))
			i = j + 1
		case unicode.IsLetter(c) || c == '_':
			j := i
			for j < len(rs) && (unicode.IsLetter(rs[j]) || unicode.IsDigit(rs[j]) || rs[j] == '_') {
				j++
			}
			word := string(rs[i:j])
			prevDot := i > 0 && rs[i-1] == '.'
			switch {
			case prevDot:
				out.WriteString(word)
			case word == "path":
				out.WriteString("event.path")
			case word == "status" || word == "trailers" || word == "markers" || word == "oldMarkers":
				return "", fmt.Errorf("match reads %q, which a PreFileWrite event does not carry the same way; write the gate's trigger by hand", word)
			default:
				out.WriteString(word)
			}
			i = j
		default:
			out.WriteRune(c)
			i++
		}
	}
	return out.String(), nil
}

func looksLikeGlobSrc(src string) bool { return !strings.ContainsAny(src, " \t\n\"'") }

// TranscriptRequires splits a file-guard's require list into the entries that
// must move to a gate and the ones that stay.
func TranscriptRequires(reqs []Prerequisite) (move, keep []Prerequisite) {
	for _, r := range reqs {
		if r.TranscriptDependent() {
			move = append(move, r)
		} else {
			keep = append(keep, r)
		}
	}
	return move, keep
}

func yamlScalar(s string) string {
	b, _ := yaml.Marshal(s)
	return strings.TrimSpace(string(b))
}

// ReplacementGateYAML renders the gate that holds a file-guard's transcript
// requirements: it wakes on PreFileWrite (create/edit) of the guard's match.
// `whenPrefix` is what a `when:` script's leading `./` becomes, so it still
// resolves from the gate's folder.
func ReplacementGateYAML(guardName, match string, moved []Prerequisite, whenPrefix string) (string, error) {
	gm, err := GateMatchFromFileMatch(match)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Holds the skill/context requirement moved off the file-guard of the same\n")
	fmt.Fprintf(&b, "# name (../../file-guard/%s/): a file-guard judges committed bytes and sees\n", guardName)
	fmt.Fprintf(&b, "# nothing of the session; a gate reads it live, before the write lands.\n")
	b.WriteString("on:\n  - event: PreFileWrite\n")
	if gm != "" {
		b.WriteString("    match: " + yamlScalar(gm) + "\n")
	}
	b.WriteString("require:\n")
	for _, r := range moved {
		first := true
		line := func(k, v string) {
			if first {
				b.WriteString("  - " + k + ": " + v + "\n")
				first = false
			} else {
				b.WriteString("    " + k + ": " + v + "\n")
			}
		}
		if r.Skill != "" {
			line("skill", yamlScalar(r.Skill))
			if len(r.Files) > 0 {
				qs := make([]string, len(r.Files))
				for i, f := range r.Files {
					qs[i] = yamlScalar(f)
				}
				line("files", "["+strings.Join(qs, ", ")+"]")
			}
		} else {
			line("context", yamlScalar(r.Context))
		}
		if r.When != "" {
			w := r.When
			if strings.HasPrefix(w, "./") {
				w = whenPrefix + strings.TrimPrefix(w, "./")
			}
			line("when", yamlScalar(w))
		}
	}
	return b.String(), nil
}

// validateNoTranscriptRequire refuses a file-guard `require:` entry that reads
// the session, naming the gate to write instead.
func validateNoTranscriptRequire(g FileGuard) []Problem {
	move, _ := TranscriptRequires(g.Require)
	if len(move) == 0 {
		return nil
	}
	name := "<name>"
	if g.Dir != "" {
		name = baseName(g.Dir)
	}
	gate, err := ReplacementGateYAML(name, g.Match, move, "../../file-guard/"+name+"/")
	if err != nil {
		gate = "(the match could not be restated automatically: " + oneLine(err.Error()) + ")\n"
	}
	return []Problem{prob(ErrRetiredKey, "require",
		"a file-guard judges committed bytes at Stop and cannot see the session, so `require: skill` / `require: context` is not allowed here — "+
			"move it to a gate. Create .sloprail/gate/%s/gate.yaml containing:\n%s"+
			"then delete those entries from this file-guard (`require: citation` may stay: it reads the commit's trailers). "+
			"`go run ./internal/declaration/cmd/split-require` does this for a whole tree",
		ReplacementGateName(name), gate)}
}

func baseName(dir string) string {
	dir = strings.TrimRight(dir, "/")
	if i := strings.LastIndex(dir, "/"); i >= 0 {
		return dir[i+1:]
	}
	return dir
}
