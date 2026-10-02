package main

import "github.com/sloprail/sloprail/internal/checkrun"

// DotDirName is the directory a project keeps its guardrails in.
const DotDirName = checkrun.DotDirName

// claudeDirName is the directory the harness records a project's settings in.
const claudeDirName = checkrun.ClaudeDirName

// dotDir resolves the project's dot-directory: the NEAREST `.sloprail` from the working
// directory upward, bounded at the tree's anchor (see checkrun.DotDir).
func dotDir(cwd string) string { return checkrun.DotDir(cwd) }

// projectDir resolves the project root — the directory holding `.claude/`.
func projectDir(cwd string) string { return checkrun.ProjectDir(cwd) }

// nearestHolding is the nearest directory from cwd upward, bounded by cwd's workspace anchor,
// that holds a directory called name.
func nearestHolding(cwd, name string) string { return checkrun.NearestHolding(cwd, name) }
