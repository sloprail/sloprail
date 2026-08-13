package guardrail

import (
	"os"
	"path/filepath"
	"strings"
)

// validateHook checks a hook names a mechanism this engine has and a command it
// could actually run.
func validateHook(d Declaration, h Hook, kind string, binding, j int) []Problem {
	var problems []Problem

	switch {
	case h.Type == "":
		problems = append(problems, at(ErrBadHookType, kind, binding, j,
			"no type — the only type is %s", HookCommand))
	case h.Type != HookCommand:
		problems = append(problems, at(ErrBadHookType, kind, binding, j,
			"type %q not understood — the only type is %s", h.Type, HookCommand))
	}

	if strings.TrimSpace(h.Command) == "" {
		return append(problems, at(ErrBadHookCommand, kind, binding, j, "no command"))
	}

	if detail := checkExecutable(d.Dir, h.Command); detail != "" {
		problems = append(problems, at(ErrBadHookCommand, kind, binding, j, "%s", detail))
	}
	return problems
}

// checkExecutable reports why a hook's command could not run, or "" when
// nothing here says it cannot.
//
// The command is a shell command line, not a path — the engine runs it through
// `sh -c`. So this only judges the cases it can judge honestly: a command line
// whose first word is a path into the guardrail's own folder, which is the form
// the spec documents and every worked example uses. Anything else — a bare name
// resolved through PATH, a pipeline, a variable expansion — is left alone
// rather than guessed at, because a checker that rejects a working command line
// it merely failed to parse is worse than one that stays quiet.
func checkExecutable(dir, command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		// The caller has already reported an empty command. Guarded here too so
		// this does not become a panic the day something else calls it.
		return ""
	}
	ref := fields[0]
	if !isPathRef(ref) {
		return ""
	}

	path := ref
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, ref)
	}

	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return "command " + quote(ref) + ": no such file"
	}
	if err != nil {
		return "command " + quote(ref) + ": " + err.Error()
	}
	if info.IsDir() {
		return "command " + quote(ref) + ": is a directory"
	}
	if info.Mode().Perm()&0o111 == 0 {
		return "command " + quote(ref) + ": not executable (mode " + info.Mode().Perm().String() + ")"
	}
	return ""
}

// isPathRef reports whether a command line's first word names a file rather
// than something PATH will resolve. `./refuse.sh` and `hooks/check.py` do; `sh`
// and `python3` do not, and where they live is not ours to have an opinion on.
func isPathRef(ref string) bool {
	if ref == "" || strings.ContainsAny(ref, "$`\"'*?") {
		// A word the shell will rewrite is not a path we can resolve.
		return false
	}
	return strings.ContainsRune(ref, filepath.Separator) || filepath.IsAbs(ref)
}

func quote(s string) string { return `"` + s + `"` }
