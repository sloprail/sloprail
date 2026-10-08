//go:build unix

package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// The process table is the one channel an environment scrub cannot close: `ps -ef` shows
// every same-user process's argv, and an agent that runs it sees `sr-eval run --fixture
// <host path>` (a real Cursor run named the fixture's path, then the checkout's, within a few
// records of doing exactly that). So `sr-eval run` re-executes itself once with an argv that
// carries no path: the real arguments go through a private file that the new process reads
// and deletes before doing anything else.

const argsFilePrefix = "@sr-eval-args:"

// hideArgv re-executes the process with a path-free argv. It returns only when it did not
// (or could not) re-execute.
func hideArgv() {
	args := os.Args[1:]
	if len(args) == 0 || args[0] != "run" || len(os.Args) == 3 && strings.HasPrefix(os.Args[2], argsFilePrefix) {
		return
	}
	self, err := os.Executable()
	if err != nil {
		return
	}
	f, err := os.CreateTemp("", "sr-eval-args-*")
	if err != nil {
		return
	}
	// NUL-separated: arguments may hold spaces and newlines.
	_, werr := f.WriteString(strings.Join(args, "\x00"))
	if cerr := f.Close(); werr != nil || cerr != nil {
		os.Remove(f.Name())
		return
	}
	argv := []string{"sr-eval", "run", argsFilePrefix + filepath.Base(f.Name())}
	if err := syscall.Exec(self, argv, os.Environ()); err != nil {
		os.Remove(f.Name())
	}
}

// restoreArgv replaces os.Args with the real arguments hideArgv stashed and deletes the file.
func restoreArgv() {
	if len(os.Args) != 3 || !strings.HasPrefix(os.Args[2], argsFilePrefix) {
		return
	}
	p := filepath.Join(os.TempDir(), strings.TrimPrefix(os.Args[2], argsFilePrefix))
	body, err := os.ReadFile(p)
	os.Remove(p)
	if err != nil {
		return
	}
	os.Args = append([]string{os.Args[0]}, strings.Split(string(body), "\x00")...)
}
