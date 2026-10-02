package checkrun

import (
	"strings"
)

// LaunchedByEnv names the guardrails whose hooks a session is running underneath.
//
// A guardrail hook may launch an agent — that is what `sr-agent` is for, and a
// judging rule is built on it. But the agent it launches works in the tree the
// rule guards, so the agent's own first Write fires PreToolUse, which runs the
// hook, which launches an agent. Measured end to end, that recursed to depth 8
// and stopped only because the test had a counter in it; nothing in the engine
// ended it.
//
// This variable is what ends it. The engine sets it on every hook it runs,
// appending the guardrail's own name to whatever it already said, and the
// environment is inherited across the exec into the harness and down into that
// agent's own hooks. So a hook firing inside a launched agent can read which
// rules it is running underneath, and the engine declines to enforce THOSE —
// and only those.
//
// A COLON-SEPARATED LIST rather than a single name, because a launched agent
// may itself launch one. Nesting appends, so the third level knows about both
// rules above it and not merely the nearest. A single-valued variable would
// have let the middle rule re-enter at the third level, which is the same bug
// one turn further in.
//
// Colon because a guardrail name is a directory name under
// .sloprail/guardrails/, so it cannot contain a path separator, and ':' is what
// PATH-shaped lists in this environment already use.
const LaunchedByEnv = "SLOPRAIL_LAUNCHED_BY"

// LaunchedBy reads the guardrails the current process is running underneath.
//
// Read through a lookup rather than os.Getenv directly so a test can supply one
// without mutating the process environment — which under -race would race every
// other test reading one.
func LaunchedBy(getenv func(string) string) []string {
	raw := getenv(LaunchedByEnv)
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out []string
	for _, name := range strings.Split(raw, ":") {
		if name = strings.TrimSpace(name); name != "" {
			out = append(out, name)
		}
	}
	return out
}

// IsLaunchedBy reports whether this session is running underneath the named
// guardrail's own hook.
//
// This is the whole enforcement question. It is asked PER GUARDRAIL rather than
// per session, and that is the difference between this and turning the launched
// agent loose:
//
//   - The rule that launched the agent does not run inside it. It already ran —
//     that is why the agent exists — and running it again is the recursion.
//   - EVERY OTHER RULE STILL RUNS. A judging agent that legitimately edits files
//     is still held to the project's other guardrails, because it is still an
//     agent editing the project's files and nothing about having been launched
//     makes its writes trustworthy.
//
// That second half is what rules out declining to enforce at all inside a
// session the engine started. Scope-based silence is simpler and strictly
// worse: it converts a launched agent into an unguarded one, and the launched
// agent is precisely the one nobody is watching.
func IsLaunchedBy(getenv func(string) string, guardrail string) bool {
	for _, name := range LaunchedBy(getenv) {
		if name == guardrail {
			return true
		}
	}
	return false
}

// AppendLaunchedBy returns the value LaunchedByEnv should carry inside one
// guardrail's hook: whatever it already said, plus this guardrail.
//
// Appending rather than replacing is what makes the sub-agent case work. A rule
// that launches an agent, whose hook launches another, must leave BOTH rules
// un-re-enterable at the innermost level; replacing would forget the outer one
// and let it fire again one level down.
//
// A name already present is not appended twice. It cannot be reached twice by
// enforcement — the engine declines it at the second level — but a hook is free
// to run `sloprail` itself, and a value that grew without bound across a long
// chain would eventually be an environment too large to exec.
func AppendLaunchedBy(getenv func(string) string, guardrail string) string {
	existing := LaunchedBy(getenv)
	for _, name := range existing {
		if name == guardrail {
			return strings.Join(existing, ":")
		}
	}
	return strings.Join(append(existing, guardrail), ":")
}
