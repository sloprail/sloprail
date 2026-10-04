package main

import (
	"path/filepath"
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

// repairInvocation: one invocation that is `chmod <mode> <paths>` or `sr-file edit|write <path>
// ...` with every path inside folder. An unknown cwd (a `cd` it could not resolve) is not a repair.
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
	if len(argv) < 2 {
		return false
	}
	var positional []string
	for _, a := range argv[1:] {
		if !strings.HasPrefix(a, "-") || a == "-" {
			positional = append(positional, a)
		} else if bin == "chmod" && len(a) > 1 && (a[1] == 'x' || a[1] == 'r' || a[1] == 'w') {
			// `chmod -x file`: a mode, not a flag. Not a repair, and not a path either.
			positional = append(positional, a)
		}
	}
	var paths []string
	switch bin {
	case "chmod":
		if len(positional) < 2 {
			return false
		}
		paths = positional[1:] // the first is the mode
	case "sr-file":
		if len(positional) < 2 || (positional[0] != "edit" && positional[0] != "write") {
			return false
		}
		paths = positional[1:2] // the file; the rest is content
	default:
		return false
	}
	for _, p := range paths {
		if !insideFolder(absPath(p, base), folder) {
			return false
		}
	}
	return true
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
