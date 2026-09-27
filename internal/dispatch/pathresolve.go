package dispatch

import "path/filepath"

// ResolveExistingPrefix resolves the deepest existing ancestor of path through
// its symlinks and re-attaches the rest, so two spellings of the same
// directory (macOS's /tmp → /private/tmp, /var → /private/var, or a
// project reached through a symlinked path) compare equal after resolution
// even when path itself does not exist yet.
//
// A path being CREATED does not exist yet, so filepath.EvalSymlinks on the
// whole of it fails outright — this walks up to the first ancestor that does
// exist, resolves THAT, and rejoins the part that was not there.
//
// Shared by two callers that both compare a session-reported path against a
// resolved root and must not be fooled by symlink spelling:
//   - services/sr-session's structure gate (a write's path against the
//     project root gitrepo.Root resolves, and against another project's root
//     when the write lands outside this one);
//   - this package's `{skill}`/`{skill, files}` prerequisite (a Read tool_use
//     or `cat`'d Bash command's path against the candidate skill/subpage
//     paths SkillFilePaths/SkillSubpagePaths compute from Request.Workspace,
//     which is itself a git root — same symlink exposure, same fix).
//
// Measured on macOS specifically: os.MkdirTemp's own result is spelled under
// /var, while `git rev-parse --show-toplevel` (gitrepo.Root, which
// Request.Workspace is built from) resolves the /var → /private/var symlink
// and answers under /private/var — so a session-reported path built from the
// unresolved spelling never string-equals a candidate built from the resolved
// one, and a bare `{skill, files}` prerequisite refused a subpage that had
// genuinely just been read, in exactly the shape it was told to read it.
func ResolveExistingPrefix(path string) string {
	rest := ""
	dir := path
	for {
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(real, rest)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return path
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = parent
	}
}
