package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/module/modules"
)

// The agent's shell is not where the project is. These pin that the project's
// rules are found from wherever the shell has wandered inside the tree — the
// hole measured on 2026-09-24, where every Stop from a subdirectory loaded no
// declarations and ended un-judged.

func mkdirs(t *testing.T, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		require.NoError(t, os.MkdirAll(d, 0o755))
	}
}

// realpath resolves symlinks so a temp dir under macOS /var compares equal to
// what git reports under /private/var.
func realpath(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	require.NoError(t, err)
	return r
}

// From a subdirectory, the project's own `.sloprail` and `.claude` are found at
// the repository root.
func TestDotDir_FromSubdirectoryFindsTheProjectRoot(t *testing.T) {
	repo := initRepo(t)
	sub := filepath.Join(repo, "memories", "tasks", "distribution")
	mkdirs(t, filepath.Join(repo, DotDirName), filepath.Join(repo, claudeDirName), sub)

	root := realpath(t, repo)
	assert.Equal(t, filepath.Join(root, DotDirName), dotDir(sub),
		"from a subdirectory the load looked for <subdir>/.sloprail, found none, and enforced nothing")
	assert.Equal(t, root, projectDir(sub),
		"from a subdirectory the enabled plugins were read from <subdir>/.claude and resolved to none")
	assert.Equal(t, filepath.Join(root, DotDirName), dotDir(repo), "the root itself still answers itself")
}

// The nearest `.sloprail` wins, so a project keeping its rules in a sub-package
// and running sessions from there keeps loading them.
func TestDotDir_NearestWins(t *testing.T) {
	repo := initRepo(t)
	pkg := filepath.Join(repo, "packages", "app")
	deep := filepath.Join(pkg, "src", "lib")
	mkdirs(t, filepath.Join(repo, DotDirName), filepath.Join(pkg, DotDirName), deep)

	assert.Equal(t, filepath.Join(realpath(t, pkg), DotDirName), dotDir(deep))
}

// The walk never leaves the tree: a `.claude` above the repository's root (the
// user's own ~/.claude) is not this project's settings.
func TestProjectDir_StopsAtTheRepositoryRoot(t *testing.T) {
	outer := t.TempDir()
	mkdirs(t, filepath.Join(outer, claudeDirName))
	repo := filepath.Join(outer, "repo")
	mkdirs(t, repo)
	runGit(t, repo, "init", "--initial-branch=main")
	sub := filepath.Join(repo, "a")
	mkdirs(t, sub)

	assert.Equal(t, realpath(t, repo), projectDir(sub))
}

// Outside any repository the working directory is its own anchor — the same
// directory as the old answer, now symlink-resolved like the anchor.
func TestDotDir_OutsideARepositoryIsTheWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	assert.Equal(t, filepath.Join(realpath(t, dir), DotDirName), dotDir(dir))
}

// SR_WORKSPACE is documented as the repository root, and event paths are
// repository-relative — so it must not follow the agent's shell into a
// subdirectory, or `$SR_WORKSPACE/$path` names a file that does not exist.
func TestNatureHookScope_WorkspaceIsTheRepositoryRootFromASubdirectory(t *testing.T) {
	repo := initRepo(t)
	sub := filepath.Join(repo, "memories", "tasks")
	mkdirs(t, sub)

	scope := natureHookScope(discard(), HookPayload{Cwd: sub})
	assert.Equal(t, realpath(t, repo), scope.Workspace)

	assert.Empty(t, natureHookScope(discard(), HookPayload{}).Workspace,
		"an absent cwd must stay absent so the unresolved sentinel is what a hook sees")
}

// End to end through the dispatch's own loader: a gate declared at the root is
// in force for a hook whose cwd is a subdirectory.
func TestNewNatureDeclarations_LoadsTheProjectGateFromASubdirectory(t *testing.T) {
	reg, err := modules.Registry()
	require.NoError(t, err)

	repo := initRepo(t)
	gate := filepath.Join(repo, DotDirName, "gate", "g")
	sub := filepath.Join(repo, "memories")
	mkdirs(t, gate, sub)
	require.NoError(t, os.WriteFile(filepath.Join(gate, "gate.yaml"), []byte("on:\n  - event: Stop\nchecks:\n  - script: ./check.sh\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(gate, "check.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755))

	t.Setenv("HOME", t.TempDir()) // no plugin cache: the project's own rules only
	loaded := newNatureDeclarations(discard(), sub, reg)
	require.Len(t, loaded.Gates, 1, "a Stop gate declared at the root did not load for a hook run from memories/")
	assert.Equal(t, "g", loaded.Gates[0].Name)
}
