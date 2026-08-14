package guardrail

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ConfigFile is the project's own settings, beside its guardrails.
const ConfigFile = "config.yaml"

// Config is what a project says about guardrails it did not write.
//
// It exists for one reason: `enabled: false` lives in the DECLARATION, and a
// consumer does not own a plugin's declaration. Editing it is not merely rude —
// the file sits in an install cache that the next reinstall overwrites, so a
// switch-off made there is silently undone by an upgrade. A consumer needs a
// place to say "not this one" that survives, and that place has to be on the
// consumer's side.
type Config struct {
	// Disabled names the guardrails this project switches off, as
	// `<plugin>/<guardrail>` for a plugin's rule or a bare name for its own.
	//
	// A list of names rather than a map of booleans. `disabled: [a, b]` says the
	// same thing as `a: false, b: false` with no way to write the confusing
	// half — a map invites `a: true`, which reads as "force this rule on" and
	// would be a second, weaker enabling mechanism competing with the
	// declaration's own.
	Disabled []string `yaml:"disabled"`
}

// LoadConfig reads a project's config, returning the zero value when there is
// none. A project with no config file has made no overrides, which is the
// ordinary state and not an error.
//
// A config that exists and cannot be READ is an error, and the caller refuses
// on it. The two are not the same: an absent file is a project saying nothing,
// and an unreadable one is a project saying something nobody can hear. Treating
// the second as the first would let a permissions accident silently re-enable
// every rule a project had deliberately switched off — the fail-open this
// codebase corrects for everywhere else.
func LoadConfig(root string) (Config, error) {
	path := filepath.Join(root, ConfigFile)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("guardrail: read %s: %w", path, err)
	}

	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return Config{}, fmt.Errorf("guardrail: parse %s: %w", path, err)
	}
	return c, nil
}

// IsDisabled reports whether this project has switched off the named guardrail.
//
// Matched against the QUALIFIED name — `<plugin>/<guardrail>` — so a project
// disabling `sloprail/authoring-slop` does not also disable a rule of its own
// that happens to be called `authoring-slop`. Those are different rules with
// different authors, and a mechanism that conflated them would switch off a rule
// the consumer wrote while they were trying to switch off one they installed.
func (c Config) IsDisabled(qualified string) bool {
	for _, name := range c.Disabled {
		if name == qualified {
			return true
		}
	}
	return false
}
