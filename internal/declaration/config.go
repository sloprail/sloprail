package declaration

import (
	"fmt"
	"os"
	"path/filepath"

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

// configFile is the project's own settings, beside its declarations. The same
// filename guardrail.ConfigFile names, deliberately shared.
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
