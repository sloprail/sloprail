package record

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var nonAlnum = regexp.MustCompile(`[^A-Za-z0-9]`)

// EncodeProjectDir maps a workspace path to the name Cursor gives its project
// directory: the path without its leading "/", every non-alphanumeric as "-"
// (cursor-mock/internal/runner/transcript.go, grounded on real recordings:
// /Users/u/proj becomes Users-u-proj).
func EncodeProjectDir(dir string) string {
	return nonAlnum.ReplaceAllString(strings.TrimPrefix(dir, "/"), "-")
}

// ProjectDir is where Cursor keeps the conversations of a workspace:
// <configDir>/projects/<encoded>; each conversation is
// agent-transcripts/<id>/<id>.jsonl beneath it. Empty when configDir is empty.
func ProjectDir(configDir, dir string) string {
	if configDir == "" {
		return ""
	}
	return filepath.Join(configDir, "projects", EncodeProjectDir(dir))
}

// ConfigDir is Cursor's per-user directory, ~/.cursor. Cursor's own override
// variable, if any, is not documented in the harness-mocks recordings, so none is
// read. Empty when the home directory cannot be resolved.
func ConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".cursor")
}
