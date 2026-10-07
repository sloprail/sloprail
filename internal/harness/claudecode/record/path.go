package record

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

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

// ProjectDir is where Claude Code keeps the transcripts of every session run in
// dir (already symlink-resolved); empty when configDir is empty.
func ProjectDir(configDir, dir string) string {
	if configDir == "" {
		return ""
	}
	return filepath.Join(configDir, "projects", EncodeProjectDir(dir))
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
