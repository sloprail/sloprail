package main

import (
	"os"
	"path/filepath"
	"strings"
)

// The agent under test must see nothing that points at the checkout sr-eval runs from, nor at
// the directory the operator ran it in: a measured run's agent went looking in that checkout
// (its sr-eval source, to understand how its own simulated user worked) and 8 of its tool
// calls touched it. The environment is the quiet channel for that: a shell exports PWD and
// OLDPWD, `_`, and any tool the operator uses may export a path into the checkout.

// hostRoots are the directories the agent must not be pointed at: the checkout and the
// directory sr-eval was started in.
func hostRoots(checkout string) []string {
	roots := []string{}
	for _, d := range []string{checkout, mustGetwd()} {
		if d == "" || d == string(filepath.Separator) {
			continue
		}
		roots = append(roots, filepath.Clean(d))
		if r, err := filepath.EvalSymlinks(d); err == nil {
			roots = append(roots, r)
		}
	}
	return roots
}

func mustGetwd() string {
	wd, _ := os.Getwd()
	return wd
}

func mentionsRoot(s string, roots []string) bool {
	for _, r := range roots {
		if strings.Contains(s, r) {
			return true
		}
	}
	return false
}

// withoutHostPaths drops from env the shell's working-directory variables and every variable
// whose value names one of roots, and the PATH entries that lie under them. PATH is the
// caller's to build, so it is cleaned by cleanPath, not here.
func withoutHostPaths(env, roots []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		key, val, _ := strings.Cut(kv, "=")
		switch {
		case key == "PWD" || key == "OLDPWD" || key == "_":
			continue
		case key == "PATH":
			out = append(out, key+"="+cleanPath(val, roots))
			continue
		case mentionsRoot(val, roots):
			continue
		}
		out = append(out, kv)
	}
	return out
}

// cleanPath is a PATH without the entries under roots.
func cleanPath(path string, roots []string) string {
	var kept []string
	for _, dir := range filepath.SplitList(path) {
		if !mentionsRoot(dir, roots) {
			kept = append(kept, dir)
		}
	}
	return strings.Join(kept, string(os.PathListSeparator))
}
