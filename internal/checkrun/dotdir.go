package checkrun

import (
	"os"
	"path/filepath"

	"github.com/sloprail/sloprail/internal/sessionpath"
)

// DotDirName is the directory a project keeps its guardrails in.
const DotDirName = ".sloprail"

// ClaudeDirName is the directory the harness records a project's settings in.
const ClaudeDirName = ".claude"

// DotDir resolves the project's dot-directory. The harness reports the working
// directory on its payload; when it does not, the process's own is the same
// answer, since a hook runs where the session runs.
//
// The working directory is where the agent's shell happens to be, not where the
// project is. An agent that ran `cd memories/tasks && …` sends every later hook a
// cwd one or more levels down, and `<cwd>/.sloprail` does not exist there — so
// the load found nothing, every project rule went quiet, and nothing said so.
// Measured on 2026-09-24 in a strategy session: every Stop whose cwd was a
// subdirectory ended un-judged while every one from the root was refused.
//
// So the dot-directory is looked for the way git looks for `.git`: the NEAREST
// `.sloprail` from the working directory upward, stopping at the tree's anchor
// (its git root — the same anchor sessionDBPath keys state on, so the rules and
// the state they read agree about which project this is). Nearest rather than
// the anchor's own, so a project that keeps its rules in a sub-package and runs
// its sessions from there keeps loading them. Where no level holds one, the
// anchor's is the answer — it does not exist either, which loads nothing, the
// same as before.
func DotDir(cwd string) string {
	return filepath.Join(NearestHolding(cwd, DotDirName), DotDirName)
}

// ProjectDir resolves the project root — the directory holding `.claude/`,
// which is where the harness records what this repo installed.
//
// Searched upward the same way as dotDir and for the same reason: from a
// subdirectory, `<cwd>/.claude/settings.json` is absent, the enabled plugins
// resolve to none, and every plugin-shipped rule goes quiet with the project's.
func ProjectDir(cwd string) string {
	return NearestHolding(cwd, ClaudeDirName)
}

// NearestHolding is the nearest directory from cwd upward, bounded by cwd's
// workspace anchor, that holds a directory called name; the anchor when none
// does.
//
// Bounded at the anchor so the walk never leaves the tree: above a repository's
// root the next `.claude` is the user's own ~/.claude, which is not this
// project's settings. A cwd outside any repository is its own anchor, so the
// walk there is the cwd alone — exactly the old answer.
func NearestHolding(cwd, name string) string {
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	anchor := sessionpath.WorkspaceAnchor(cwd)

	// Compared against the anchor, which git reports symlink-resolved; resolve
	// the start the same way or /var and /private/var never meet.
	dir := cwd
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		dir = resolved
	}
	if rel, err := filepath.Rel(anchor, dir); err != nil || rel == ".." || filepath.IsAbs(rel) ||
		(len(rel) >= 3 && rel[:3] == ".."+string(filepath.Separator)) {
		// Not inside its own anchor — nothing to walk. Keep the old answer.
		return cwd
	}

	for {
		if info, err := os.Stat(filepath.Join(dir, name)); err == nil && info.IsDir() {
			return dir
		}
		if dir == anchor {
			return anchor
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return anchor
		}
		dir = parent
	}
}
