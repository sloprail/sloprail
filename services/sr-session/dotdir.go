package main

import (
	"os"
	"path/filepath"

	"github.com/sloprail/sloprail/internal/guardrail"
	"github.com/sloprail/sloprail/internal/harness"
)

// DotDirName is the directory a project keeps its guardrails in.
//
// An alias for guardrail.DotDir rather than a second spelling of the same
// string: a plugin's installation now uses the identical layout, and the loader
// that reads both is where the answer belongs. Two independent literals is how
// the project's path and the plugin's came to differ in the first place.
const DotDirName = guardrail.DotDir

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

// guardrailStore opens the guardrails in force for a session: the project's
// own, plus those shipped by the plugins the PROJECT has enabled.
//
// # Where the plugin set comes from
//
// From the repo's own `.claude/settings.json` and `.claude/settings.local.json`,
// read by internal/harness. The repo is the thing that decided to install a
// plugin, so the repo's settings are the truth about which are installed — not
// which happened to fire a hook, which is a different and smaller set that
// omits every plugin shipping guardrails without hooks.
//
// # The unresolved plugins are returned, not swallowed
//
// Resolve reports enabled plugins it could not locate, and this function passes
// them back so every caller can print them. That is the safety property of
// reading a harness's configuration from in here: if the layout or the manifest
// schema moves, the user is told which plugin went missing rather than quietly
// losing its guardrails. Dropping this return value would restore exactly the
// silent no-op the design is built to prevent — so it is returned, and each
// hook point reports it.
//
// One constructor for all three hook points, so pre-tool, post and session-start
// cannot come to disagree about which rules are in force. They have to agree:
// a rule that session-start announces and pre-tool does not enforce is worse
// than either behaviour on its own, because the announcement is what convinces
// the author they are covered.
func guardrailStore(cwd string) (*guardrail.Store, []harness.Unresolved, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		// Without a home directory the install cache cannot be found. Reported
		// rather than defaulted to the project's own guardrails: a session that
		// silently enforces only half the rules is the failure mode this whole
		// mechanism exists to close.
		return nil, nil, err
	}

	res, err := harness.Resolve(projectDir(cwd), home)
	if err != nil {
		return nil, nil, err
	}

	plugins := make([]guardrail.Origin, 0, len(res.Roots))
	for _, r := range res.Roots {
		plugins = append(plugins, guardrail.Origin{Plugin: r.Plugin.Name, Root: r.Dir})
	}
	return guardrail.NewWithPlugins(dotDir(cwd), plugins), res.Unresolved, nil
}
