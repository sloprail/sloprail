// Package scriptexec is the one place sloprail decides whether a declared script
// may be run: it must be executed DIRECTLY, so it must be executable and carry a
// shebang naming a standard interpreter. There is no `sh <file>` fallback — a
// file without a shebang would otherwise run under whatever shell happens to
// start it, and a bash script run by dash fails in ways that read as a refusal
// of the guarded action.
//
// A declared script that fails Verify is REPORTED (declaration.Loaded.Degraded: `sr-file
// declarations`, the next session hook) and REFUSED at run time, naming the file and the fix; its
// rule stays loaded and enforced. For a context's `enter` that means the event that triggered it is
// refused (a Pre* event denied, a Post* one refused at the Stop), and every Stop is refused while an
// enter or exit cannot run: an enter that cannot run is not a decline, it never reads as approval.
// Dropping the rule instead would disarm it on a `chmod -x`, which
// is not a write and so never reaches a hook.
//
// Only declared SCRIPT paths are affected (a check's `script`/`prepare`, a rule's
// `subjects`, a context's `enter`/`exit`, a judge mock, an sr-test `test.sh`),
// never an inline command. A declared script string is a path plus plain arguments, exec'd
// directly (see Argv), never `sh -c`.
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
	"unicode"
)

// The ways a script can be unrunnable. Compare with errors.Is.
var (
	ErrNotExecutable  = errors.New("script is not executable")
	ErrNoShebang      = errors.New("script has no shebang")
	ErrBadInterpreter = errors.New("script's shebang names a non-standard interpreter")
	ErrShellSyntax    = errors.New("declared script is not a path plus plain arguments")
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

// Argv splits a declared script string into the argv it is exec'd with, or says why it
// cannot be.
//
// A declared script is a PATH plus plain ARGUMENTS, split on blanks and exec'd directly,
// never through a shell: `./staged.sh check` is fine, `./a.sh && ./b.sh`, `./x.sh | y`,
// `"my dir/x.sh"`, `$HOME/x.sh`, `./x.sh >out`, `./x.sh *` are not (a shell would run them,
// but only the first word could be verified, and the rest would run unchecked). Every
// character of a word must be a letter, a digit or one of `_ . / - + = : , @ %`; anything else
// is shell syntax and is refused, naming the character. The first word is resolved against dir
// unless absolute; a bare first word (no `/`) is a program on PATH, left to the exec to find.
// sr:invariant checks/check-that-cannot-answer-refuses
func Argv(dir, script string) ([]string, error) {
	words := strings.Fields(script)
	if len(words) == 0 {
		return nil, fmt.Errorf("%w: it is empty", ErrShellSyntax)
	}
	for _, w := range words {
		for _, r := range w {
			if unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("_./-+=:,@%", r) {
				continue
			}
			return nil, fmt.Errorf("%w: %q has %q, which a shell would interpret. A declared script is a path plus plain arguments, run directly, never through a shell. Fix: put the logic in a script file and declare `./that.sh`", ErrShellSyntax, script, r)
		}
	}
	if strings.ContainsRune(words[0], '=') {
		return nil, fmt.Errorf("%w: %q starts with an assignment (%q), which only a shell reads as an environment variable. Fix: set it inside the script", ErrShellSyntax, script, words[0])
	}
	if strings.ContainsRune(words[0], filepath.Separator) {
		p := words[0]
		if !filepath.IsAbs(p) {
			abs, err := filepath.Abs(dir)
			if err != nil {
				return nil, err
			}
			p = filepath.Join(abs, p)
		}
		words[0] = p
	}
	return words, nil
}

// Path resolves the script file a declared script string names (see Argv): its first word,
// relative to dir unless absolute. "" when the string is empty or names a program on PATH.
func Path(dir, script string) string {
	argv, err := Argv(dir, script)
	if err != nil || !strings.ContainsRune(argv[0], filepath.Separator) {
		return ""
	}
	return argv[0]
}

// VerifyDeclared is Verify for a declared script string resolved against dir: the string must
// be a path plus plain arguments (Argv), and the file it names must pass Verify. A declaration
// naming a file that does not exist is nil here: absence is reported where it is run (a clear
// "not found"), and a bare word is a program on PATH, not a script of ours. Everything that
// EXISTS must pass Verify.
//
// Verify then exec is not atomic, and cannot be: the kernel's own exec is the last word. A file
// swapped in between is still exec'd directly, never through a shell, so one that lost its
// shebang or execute bit fails to start (refused by the caller), never runs as `sh <file>`.
func VerifyDeclared(dir, script string) error {
	if strings.TrimSpace(script) == "" {
		return nil
	}
	if _, err := Argv(dir, script); err != nil {
		return err
	}
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
// no interpreter fallback) with args. A relative path is resolved against the
// process's working directory, once, here: the file verified is the file exec'd,
// whatever Dir the caller then sets on the command (Go would otherwise resolve a
// relative Path against cmd.Dir, a different place).
func Command(ctx context.Context, path string, args ...string) (*exec.Cmd, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err := Verify(abs); err != nil {
		return nil, err
	}
	return exec.CommandContext(ctx, abs, args...), nil
}
