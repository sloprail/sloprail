package main

import (
	"os"
	"path/filepath"
)

// DotDirName is the directory a project keeps its guardrails in.
const DotDirName = ".sloprail"

// dotDir resolves the project's dot-directory. The harness reports the working
// directory on its payload; when it does not, the process's own is the same
// answer, since a hook runs where the session runs.
func dotDir(cwd string) string {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	return filepath.Join(cwd, DotDirName)
}

// projectDir resolves the project root — the directory holding `.claude/`,
// which is where the harness records what this repo installed.
func projectDir(cwd string) string {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	return cwd
}
