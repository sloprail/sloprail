package transcript

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// A transcript's path arrives on the harness's payload, so nothing here is
// needed to find the file we were told to read. It is needed for the identity
// walk: crossing a restart means looking into the OTHER transcripts of the same
// conversation, and where those sit is not on any payload — it is wherever the
// harness put the project this session runs in.
//
// Deriving that directory from the working directory is what keeps the walk
// from scanning. One real ~/.claude held 1067 project directories; globbing a
// session id across all of them is a directory-listing sweep on every single
// resolution, when the encoding is deterministic and already known.

// nonAlnum matches every character that is not an ASCII letter or digit —
// Claude Code's own working-directory-to-project-directory rule, which is a
// verbatim `replace(/[^a-zA-Z0-9]/g,"-")`.
var nonAlnum = regexp.MustCompile(`[^a-zA-Z0-9]`)

// EncodeProjectDir maps a working directory to the harness's project directory
// name. Ported from a10n, which ported it from the harness's own encoding and
// keeps it in one place deliberately — a second copy of this rule is a second
// thing to get subtly wrong.
func EncodeProjectDir(dir string) string {
	return nonAlnum.ReplaceAllString(dir, "-")
}

// ResolveWorkDir returns dir with its symlinks resolved, so that
// EncodeProjectDir(ResolveWorkDir(dir)) matches what the harness itself
// encoded.
//
// On macOS the temp and data roots are symlinked — /var to /private/var — so a
// logical path and the harness's own resolved working directory differ, and an
// encoding taken from the unresolved one names a directory nothing is in.
//
// For a path whose tail does not exist yet, the longest existing ancestor is
// resolved and the missing tail rejoined. On any error the input is returned
// unchanged, because a path we cannot resolve is still likelier to be right
// than nothing.
func ResolveWorkDir(dir string) string {
	if dir == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		return resolved
	}
	remaining := ""
	cur := dir
	for {
		parent := filepath.Dir(cur)
		if parent == cur {
			return dir
		}
		remaining = filepath.Join(filepath.Base(cur), remaining)
		if resolved, err := filepath.EvalSymlinks(parent); err == nil {
			return filepath.Join(resolved, remaining)
		}
		cur = parent
	}
}

// ProjectDir is where the harness keeps the transcripts of every session run in
// dir.
//
// Empty when the harness's own configuration directory cannot be located, which
// the caller reports rather than papering over: a walk that silently searched
// the wrong directory would find nothing and call the conversation new.
func ProjectDir(configDir, dir string) string {
	if configDir == "" {
		return ""
	}
	return filepath.Join(configDir, "projects", EncodeProjectDir(ResolveWorkDir(dir)))
}

// ConfigDir is the harness's own configuration directory — $CLAUDE_CONFIG_DIR
// when set, else ~/.claude. Empty when neither can be resolved.
func ConfigDir() string {
	if cfg := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); cfg != "" {
		return cfg
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude")
}
