package declaration

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// This file is the new-format counterpart to internal/guardrail's config.go: the
// project's own say over declarations it did not write. It is the SAME file on
// disk — `.sloprail/config.yaml` with a `disabled:` list — because the old and new
// formats run alongside each other in one project and a consumer switching a rule
// off should not have to know which format shipped it. The two loaders read the
// same file; a `disabled:` entry naming an old-format guardrail is a no-op here
// and one naming a new-format declaration is a no-op there, each ignoring keys
// that are not its own.

// configFile is the project's own settings, beside its declarations — the one
// file a project writes to disable a declaration it did not author (a plugin's,
// or its own not-ready one). Named here and quoted by the load-failure remedies
// (Invalid.Remedy, Shadow.Message), single-sourced so those agree.
const configFile = "config.yaml"

// config is what a project says about declarations it did not write.
//
// It exists for one reason, the same one guardrail.Config exists for: a plugin's
// declaration lives in an install cache the next reinstall overwrites, so a
// switch-off made by editing it there is silently undone by an upgrade. A consumer
// needs a place to say "not this one" that survives, and that place has to be on
// the consumer's side.
type config struct {
	// Disabled names the declarations this project switches off, by their qualified
	// name — `<plugin>/<nature>/<name>` for a plugin's, `<nature>/<name>` for the
	// project's own (see Origin.Qualified). A list of names rather than a map of
	// booleans, so there is no way to write the confusing `x: true` that would read
	// as a second, weaker enabling mechanism competing with the declaration itself.
	Disabled []string `yaml:"disabled"`

	// StopHookBlockCap is how many times in a row a Stop may be refused before
	// the engine lets the turn end un-judged. A pointer so "absent" (the default)
	// is told apart from an explicit 0 (no cap). See StopHookBlockCap.
	StopHookBlockCap *int `yaml:"stop_hook_block_cap"`
}

// DefaultStopHookBlockCap mirrors Claude Code's own CLAUDE_CODE_STOP_HOOK_BLOCK_CAP
// default: after 8 consecutive blocks the harness overrides the hook and ends the
// turn anyway, so a cap the engine sets any higher is one the harness never lets
// it reach unless the operator raises that variable too.
const DefaultStopHookBlockCap = 8

// StopHookBlockCap reads the project's `stop_hook_block_cap` from the config in
// root (a `.sloprail` directory): how many consecutive refusals a Stop may take
// before the engine stops judging it and lets the turn end.
//
// A refused Stop is judged again on every retry, so an agent cannot end a turn by
// replying twice — it loops until its reply passes. The cap is the escape valve,
// and it is a PROJECT decision, written where the project's other overrides are
// rather than in an environment variable nobody reviews:
//
//	stop_hook_block_cap: 8   # default — refuse up to 8 times in a row
//	stop_hook_block_cap: 1   # refuse once, then let the retry end un-judged
//	stop_hook_block_cap: 0   # no engine cap (the harness's own cap still applies)
//
// Absent means DefaultStopHookBlockCap. A negative value is an error, and so is a
// config that exists and cannot be read; the caller decides what an error costs.
func StopHookBlockCap(root string) (int, error) {
	c, err := loadConfig(root)
	if err != nil {
		return DefaultStopHookBlockCap, err
	}
	if c.StopHookBlockCap == nil {
		return DefaultStopHookBlockCap, nil
	}
	if *c.StopHookBlockCap < 0 {
		return DefaultStopHookBlockCap, fmt.Errorf("declaration: %s: stop_hook_block_cap must be 0 or more, got %d",
			filepath.Join(root, configFile), *c.StopHookBlockCap)
	}
	return *c.StopHookBlockCap, nil
}

// loadConfig reads a project's config, returning the zero value when there is
// none. A project with no config file has made no overrides, which is the ordinary
// state and not an error.
//
// A config that exists and cannot be READ is an error, and Load refuses on it. The
// two are not the same: an absent file is a project saying nothing, an unreadable
// one is a project saying something nobody can hear. Treating the second as the
// first would let a permissions accident silently re-enable every declaration a
// project had deliberately switched off — the fail-open guardrail.LoadConfig
// closes, closed the same way here.
func loadConfig(root string) (config, error) {
	path := filepath.Join(root, configFile)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return config{}, nil
	}
	if err != nil {
		return config{}, fmt.Errorf("declaration: read %s: %w", path, err)
	}

	var c config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return config{}, fmt.Errorf("declaration: parse %s: %w", path, err)
	}
	return c, nil
}

// isDisabled reports whether this project has switched off the named declaration.
//
// Matched against the QUALIFIED name — `<plugin>/<nature>/<name>` or
// `<nature>/<name>` — so disabling a plugin's `sloprail/file-guard/authoring-slop`
// does not also disable a project's own `file-guard/authoring-slop`. Those are
// different rules with different authors, and conflating them would switch off a
// rule the consumer wrote while trying to switch off one they installed. This is
// guardrail.Config.IsDisabled's exact-match rule, over the new format's key.
func (c config) isDisabled(qualified string) bool {
	for _, name := range c.Disabled {
		if name == qualified {
			return true
		}
	}
	return false
}

// protectedDisable reports whether the qualified name is a rule a project cannot switch
// off by writing its working-tree config. `sloprail/<nature>/grounded-rule-changes` is the
// plugin's rule that refuses a change to the project's own rules, so a `disabled:` entry
// naming it is the cheapest way out of its refusal: written by any writer no gate models
// (a script, `yq -i`, `git apply`), the working-tree config would turn the rule off
// before it could judge the very change that did it.
func protectedDisable(qualified string) bool {
	return strings.HasPrefix(qualified, "sloprail/") && strings.HasSuffix(qualified, "/grounded-rule-changes")
}

// trustProtected drops from cfg every protectedDisable entry the TRUSTED config does not
// also list. The trusted config is the one committed at rev (the commit the session began
// at, so the agent's own commits are not trusted either), or at HEAD when rev is empty. If
// it cannot be read (no repository, no such commit, no such file) nothing protected is
// honoured: a rule that could not be checked stays on.
func trustProtected(cfg config, root, rev string) config {
	has := false
	for _, n := range cfg.Disabled {
		if protectedDisable(n) {
			has = true
			break
		}
	}
	if !has {
		return cfg
	}
	if rev == "" {
		rev = "HEAD"
	}
	var trusted config
	out, err := exec.Command("git", "-C", root, "show", rev+":./"+configFile).Output()
	if err == nil && yaml.Unmarshal(out, &trusted) != nil {
		trusted = config{}
	}
	kept := make([]string, 0, len(cfg.Disabled))
	for _, n := range cfg.Disabled {
		if protectedDisable(n) && !trusted.isDisabled(n) {
			continue
		}
		kept = append(kept, n)
	}
	cfg.Disabled = kept
	return cfg
}
