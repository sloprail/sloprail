package main

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/sloprail/sloprail/internal/declaration"
	dispatchcore "github.com/sloprail/sloprail/internal/dispatch"
	"github.com/sloprail/sloprail/internal/event"
)

// A context whose enter cannot run refuses every call it triggers on (runContextEnters). When the
// context triggers on a broad event (every PreToolUse), that would deny the repair itself: the
// agent could not `chmod +x` the script or restore its shebang. So a call that REPAIRS the
// fault is never refused for it, and nothing else is exempt. The exemption is narrow on purpose:
//
//   - a file create or update (a Write, an Edit) whose path is inside the context's own folder;
//   - a command whose EVERY invocation is `chmod`, or `sr-file edit|write`, on paths that are
//     all inside the context's own folder. A chain that does anything else is not a repair.
//
// The Stop sweep (brokenContextScripts) still refuses while the fault stands.

// repairsContext reports whether the call that fired the enter only repairs the context's own
// scripts. events is EVERY event of the call, not just the one the context triggered on: a context
// on PreToolUse is woken by the tool event, which carries no path, while the file or command the
// call does is a sibling event. The call is a repair when it does at least one file write or
// command, and every one it does is a repair. workspace is what a relative path resolves against
// (an invocation's own cwd is joined onto it).
func repairsContext(events []event.Event, c declaration.Context, workspace string) bool {
	folder := absPath(c.Dir, "")
	if folder == "" {
		return false
	}
	acts := 0
	for _, e := range events {
		switch e.Kind {
		case declaration.KindPreFileCreate, declaration.KindPreFileUpdate,
			declaration.KindPostFileCreate, declaration.KindPostFileUpdate:
			acts++
			p, _ := e.Fields["path"].(string)
			if p == "" || !insideFolder(absPath(p, workspace), folder) {
				return false
			}
		case declaration.KindPreFileDelete, declaration.KindPostFileDelete:
			return false // removing is no repair
		case "PreCommandInvoke":
			acts++
			invs, _ := e.Fields["invocations"].([]any)
			if len(invs) == 0 {
				return false
			}
			for _, item := range invs {
				inv, _ := item.(map[string]any)
				if !repairInvocation(inv, folder, workspace) {
					return false
				}
			}
		}
	}
	return acts > 0
}

// repairInvocation: one invocation that is exactly
//
//	chmod [-R] <mode> <path>...        mode adds execute: `+x`, `u+x`, `ug+x`, `+rx`, 755, 0755
//	sr-file edit|write <path> [--flag]...   the path FIRST, every later word a bare `--flag`
//
// with every path inside folder. Anything else is not a repair: a removed bit (`-x`), a
// `--reference`, a flag that takes a value (its value could be taken for the path, or hide
// another file), a path hidden behind a flag. An unknown cwd (a `cd` it could not resolve) is not
// a repair. (An edit that needs flag values is made with the Edit or Write tool, which is exempt.)
func repairInvocation(inv map[string]any, folder, workspace string) bool {
	bin, _ := inv["bin"].(string)
	cwd, ok := inv["cwd"].(string)
	if !ok || cwd == "" {
		return false
	}
	base := absPath(cwd, workspace)
	var argv []string
	rawArgv, _ := inv["argv"].([]any)
	for _, a := range rawArgv {
		s, _ := a.(string)
		argv = append(argv, s)
	}
	var paths []string
	switch bin {
	case "chmod":
		args := argv[min(1, len(argv)):]
		if len(args) > 0 && args[0] == "-R" {
			args = args[1:]
		}
		if len(args) < 2 || !addsExecute(args[0]) {
			return false
		}
		paths = args[1:]
	case "sr-file":
		if len(argv) < 3 || (argv[1] != "edit" && argv[1] != "write") || strings.HasPrefix(argv[2], "-") {
			return false
		}
		for _, w := range argv[3:] {
			if !strings.HasPrefix(w, "--") {
				return false
			}
		}
		paths = argv[2:3]
	default:
		return false
	}
	for _, p := range paths {
		if strings.HasPrefix(p, "-") || !insideFolder(absPath(p, base), folder) {
			return false
		}
	}
	return true
}

var (
	symbolicAddsX = regexp.MustCompile(`^[ugoa]*\+[rwXst]*x[rwXst]*$`)
	octalMode     = regexp.MustCompile(`^[0-7]{3,4}$`)
)

// addsExecute: a chmod mode that adds an execute bit (`+x`, `u+x`, `a+rx`) or an octal mode with
// one. `-x`, `u-x`, `=r` and the rest are not.
func addsExecute(mode string) bool {
	if symbolicAddsX.MatchString(mode) {
		return true
	}
	if octalMode.MatchString(mode) {
		for _, d := range mode {
			if (d-'0')&1 != 0 {
				return true
			}
		}
	}
	return false
}

// absPath makes p absolute against base (the current directory when base is "") and resolves
// symlinks as far as it exists.
func absPath(p, base string) string {
	if p == "" {
		return ""
	}
	if !filepath.IsAbs(p) {
		if base == "" {
			abs, err := filepath.Abs(p)
			if err != nil {
				return ""
			}
			p = abs
		} else {
			p = filepath.Join(base, p)
		}
	}
	return dispatchcore.ResolveExistingPrefix(filepath.Clean(p))
}

// insideFolder: path is folder or below it. A look-alike sibling (`ctx-evil` beside `ctx`) is not.
func insideFolder(path, folder string) bool {
	if path == "" || folder == "" {
		return false
	}
	rel, err := filepath.Rel(folder, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
