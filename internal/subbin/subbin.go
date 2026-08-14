// Package subbin resolves a co-installed sloprail service binary (sr-session,
// sr-file, sr-mark, ...) by its well-known sibling path.
//
// The root `sr` proxy and any service that spawns a sibling must agree on the
// resolution rules. Otherwise tests pass against the freshly-built binary in a
// temp dir while production reaches a stale system install, and the failure
// names the wrong thing — `unknown command` from a binary nobody meant to run.
//
// Lookup order:
//
//  1. $SLOP_SUBBIN_DIR/<name> when that env var is set (an explicit override).
//  2. The directory of the running binary (os.Executable's sibling).
//  3. $PATH.
//
// UNLIKE a10n's equivalent, $PATH IS searched, and it is searched last. a10n
// excludes it deliberately: its services are installed as a set into one
// directory, so a PATH hit is by definition the wrong copy, and excluding it
// turns a confusing stale-binary failure into a clear not-found. sloprail
// cannot borrow that. Its binaries are what a `go install` puts in GOPATH/bin
// and what a marketplace plugin expects to find by name, so the sibling
// directory is an install layout it has but does not require. Dropping the PATH
// step would break the ordinary `go install ./services/...` arrangement, where
// the binaries ARE siblings in GOPATH/bin — that case is caught by step 2 — but
// also every arrangement where they are not.
//
// The stale-binary risk a10n avoids is handled by ordering instead: an explicit
// override wins over a sibling, and a sibling wins over PATH. A test building
// into a temp dir sets SLOP_SUBBIN_DIR or invokes the binary there, so it can
// never fall through to a machine install by accident.
package subbin

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// EnvDir is the environment variable that overrides where sibling service
// binaries are found. It exists for the case where they are NOT siblings of the
// running binary — the e2e harness builds them into a temp directory and points
// this at it. Empty in a normal install, so it never affects production layout.
const EnvDir = "SLOP_SUBBIN_DIR"

// Find returns the path of a co-installed service binary, or an error
// naming every location tried.
//
// The path is absolute for the two resolution routes that matter — the sibling
// directory of the running binary, and a PATH lookup — because both start from
// one. It is NOT absolute when SLOP_SUBBIN_DIR names a relative directory, in
// which case the join is relative too and resolves against the caller's working
// directory. That variable is test-only and setting it already implies control
// of the environment, so this is a statement about the docstring rather than a
// hole; it said "absolute" unconditionally and one route does not.
func Find(name string) (string, error) {
	var tried []string

	if dir := os.Getenv(EnvDir); dir != "" {
		candidate := filepath.Join(dir, name)
		if isExecutable(candidate) {
			return candidate, nil
		}
		tried = append(tried, candidate)
	}

	if self, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(self), name)
		if isExecutable(candidate) {
			return candidate, nil
		}
		tried = append(tried, candidate)
	}

	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	tried = append(tried, name+" (on $PATH)")

	return "", fmt.Errorf("%s not found — looked in %v; install the sloprail services together, or set %s to the directory holding them", name, tried, EnvDir)
}

// isExecutable reports whether path is a regular file with an execute bit.
func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode()&0o111 != 0
}
