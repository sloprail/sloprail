package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sloprail/sloprail/internal/harness"
)

// tuiUserCommand is the one command the simulated user runs: a wrapper this run writes into a
// private directory, that runs THIS run's own sr-eval (`sr-eval tui "$@"`) by absolute path.
//
// It has a name of its own, not `sr-eval`, because a PATH lookup of `sr-eval` found an older
// release installed on the host (~/.local/bin) in a measured run, and "unknown command tui"
// followed. Whatever the harness does to PATH (a login shell re-prepending ~/.local/bin), no
// installed release has a command called sr-eval-tui, so the user gets this build's or nothing.
const tuiUserCommand = "sr-eval-tui"

// tuiUserGrant is the one tool the simulated user is granted: that command, named by its
// base, which is all a harness like Cursor can scope a shell command by (it refuses a
// path-scoped `Bash(<path> tui:*)`). Unlike a grant on `sr-eval`, it covers nothing but the
// terminal tools. The user runs with no hooks, no plugins and an empty directory.
const tuiUserGrant = "Bash(" + tuiUserCommand + ":*)"

// writeUserBin writes the wrapper into dir (created) and returns it, for the front of the
// user's PATH. srEval is this run's sr-eval, an absolute path.
func writeUserBin(dir, srEval string) error {
	if !filepath.IsAbs(srEval) {
		return fmt.Errorf("this run's sr-eval is %q, not an absolute path", srEval)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	quoted := "'" + strings.ReplaceAll(srEval, "'", `'\''`) + "'"
	body := "#!/bin/sh\nexec " + quoted + " tui \"$@\"\n"
	return os.WriteFile(filepath.Join(dir, tuiUserCommand), []byte(body), 0o755)
}

// tuiUserEnv is the environment the simulated user runs in: the operator's, minus the
// enclosing harness session, with the private bin dir first on PATH, then this run's binaries.
func tuiUserEnv(binDir, userBin, sock string) []string {
	path := userBin + string(os.PathListSeparator) + binDir + string(os.PathListSeparator) + os.Getenv("PATH")
	return append(harness.Current().SessionEnv(os.Environ()), "PATH="+path, tuiSocketEnv+"="+sock)
}

// preflightUserBin proves, in exactly the environment env the user will run in, that the
// command it will be told to run resolves to the wrapper written for this run and that the
// build behind it has the terminal tools. It fails loudly otherwise: a user that can't operate
// the terminal wastes minutes wandering the host looking for how.
func preflightUserBin(userBin string, env []string) error {
	want := filepath.Join(userBin, tuiUserCommand)
	var path string
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			path = v // the last one wins, as in an exec
		}
	}
	for _, dir := range filepath.SplitList(path) {
		cand := filepath.Join(dir, tuiUserCommand)
		if info, err := os.Stat(cand); err == nil && !info.IsDir() {
			if cand != want {
				return fmt.Errorf("the simulated user's %s resolves to %s, not this run's %s", tuiUserCommand, cand, want)
			}
			break
		}
	}
	c := exec.Command(want, "--help")
	c.Env = env
	out, err := c.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "type") || !strings.Contains(string(out), "wait") {
		return fmt.Errorf("the simulated user's %s --help did not show the terminal tools (this run's sr-eval is not the build it should be): %v\n%s", tuiUserCommand, err, strings.TrimSpace(string(out)))
	}
	return nil
}
