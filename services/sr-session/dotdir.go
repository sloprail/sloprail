package main

import (
	"os"
	"path/filepath"

	"github.com/sloprail/sloprail/internal/guardrail"
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

// guardrailStore opens the guardrails in force for a session: the project's
// own, plus those shipped by whatever plugins the harness has told us about.
//
// This is the ONLY place the engine learns that installed plugins exist, and it
// learns it from an environment variable it defined rather than from any
// harness's manifest. What that buys is stated in full at guardrail.PluginDirsEnv;
// the short version is that the alternative — reading
// ~/.claude/plugins/installed_plugins.json — would put the path, the JSON shape
// and the cache layout of one specific harness inside an engine whose entire
// design premise is not knowing which harness it is under.
//
// One constructor for all three hook points, so pre-tool, post and session-start
// cannot come to disagree about which rules are in force. They have to agree:
// a rule that session-start announces and pre-tool does not enforce is worse
// than either behaviour on its own, because the announcement is what convinces
// the author they are covered.
func guardrailStore(cwd string) *guardrail.Store {
	return guardrail.NewWithPlugins(dotDir(cwd), guardrail.PluginDirs(os.Getenv))
}
