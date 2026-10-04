// Package scriptexec is the one place sloprail decides whether a declared script
// may be run: it must be executed DIRECTLY, so it must be executable and carry a
// shebang naming a standard interpreter. There is no `sh <file>` fallback — a
// file without a shebang would otherwise run under whatever shell happens to
// start it, and a bash script run by dash fails in ways that read as a refusal
// of the guarded action.
//
// A declared script that fails Verify is REPORTED (declaration.Loaded.Degraded: `sr-file
// declarations`, the next session hook) and REFUSED at run time, naming the file and the fix; its
// rule stays loaded and enforced. Dropping the rule instead would disarm it on a `chmod -x`, which
// is not a write and so never reaches a hook.
//
// Only declared SCRIPT paths are affected (a check's `script`/`prepare`, a rule's
// `subjects`, a context's `enter`/`exit`, a judge mock, an sr-test `test.sh`),
// never an inline command.
package scriptexec

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// The ways a script can be unrunnable. Compare with errors.Is.
var (
	ErrNotExecutable  = errors.New("script is not executable")
	ErrNoShebang      = errors.New("script has no shebang")
	ErrBadInterpreter = errors.New("script's shebang names a non-standard interpreter")
)

// standardInterpreterDirs are the only directories an absolute interpreter path
// may live in. /usr/local/bin/bash or /opt/homebrew/bin/bash exist on one machine
// and not the next; `#!/usr/bin/env bash` finds whichever bash is on PATH.
var standardInterpreterDirs = map[string]bool{"/bin": true, "/usr/bin": true}

// Verify reports why the file at path cannot be exec'd directly, or nil. A path
// that does not exist is an error from os.Stat, not one of the sentinels.
func Verify(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory, not a script", path)
	}
	if info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("%w: %s. Fix: chmod +x %s (and `git update-index --chmod=+x` it so the mode is committed)", ErrNotExecutable, path, path)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	line, _ := bufio.NewReader(f).ReadString('\n')
	if !strings.HasPrefix(line, "#!") {
		return fmt.Errorf("%w: %s. Fix: add `#!/usr/bin/env bash` (or `#!/bin/sh`) as its first line — sloprail runs scripts directly, never as `sh <file>`", ErrNoShebang, path)
	}
	fields := strings.Fields(strings.TrimPrefix(line, "#!"))
	if len(fields) == 0 {
		return fmt.Errorf("%w: %s has an empty `#!` line. Fix: use `#!/usr/bin/env bash` or `#!/bin/sh`", ErrNoShebang, path)
	}
	interp := fields[0]
	if !filepath.IsAbs(interp) || !standardInterpreterDirs[filepath.Dir(interp)] {
		return fmt.Errorf("%w: %s uses %q. Fix: use `#!/usr/bin/env bash` or `#!/bin/sh` (an interpreter outside /bin and /usr/bin is not on every machine)", ErrBadInterpreter, path, interp)
	}
	if filepath.Base(interp) == "env" && len(fields) < 2 {
		return fmt.Errorf("%w: %s has `#!%s` with no interpreter. Fix: `#!/usr/bin/env bash`", ErrBadInterpreter, path, interp)
	}
	return nil
}

// Path resolves the script file a declared script string names: its first
// whitespace-separated word (the rest are arguments), relative to dir unless
// absolute. "" when the string is empty.
func Path(dir, script string) string {
	fields := strings.Fields(script)
	if len(fields) == 0 {
		return ""
	}
	p := fields[0]
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	return p
}

// VerifyDeclared is Verify for a declared script string resolved against dir. A
// declaration naming a file that does not exist is nil here: absence is reported
// where it is run (a clear "not found"), and a bare word with no such file may be
// a program on PATH rather than a script. Everything that EXISTS must pass Verify.
func VerifyDeclared(dir, script string) error {
	p := Path(dir, script)
	if p == "" {
		return nil
	}
	info, err := os.Stat(p)
	if err != nil || info.IsDir() {
		return nil
	}
	return Verify(p)
}

// Command verifies path and returns a command that execs it directly (no shell,
// no interpreter fallback) with args.
func Command(ctx context.Context, path string, args ...string) (*exec.Cmd, error) {
	if err := Verify(path); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(path) && !strings.ContainsRune(path, filepath.Separator) {
		path = "." + string(filepath.Separator) + path
	}
	return exec.CommandContext(ctx, path, args...), nil
}
