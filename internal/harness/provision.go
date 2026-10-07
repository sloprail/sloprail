package harness

import (
	"context"
	"fmt"
)

// Provisioner is what a launcher needs to run a REAL agent of this harness in a
// sandbox of its own (sr-eval's isolated HOME): which program, how it is logged in,
// where it writes its record, and how a plugin gets installed into it. A Harness MAY
// implement it; the facts live here, beside the rest of what is specific to the
// harness, and the launcher asks rather than switching on a name.
type Provisioner interface {
	// Binary is the harness's executable name (what sr-agent runs, what a sandbox's
	// PATH must still reach).
	Binary() string

	// AuthFiles are the files, relative to HOME, that carry the login when it is a
	// file (Codex's auth.json). A launcher LINKS exactly these and nothing else of the
	// config directory (a rotated refresh token must land in the operator's real file). A login kept in the platform's keychain (Claude Code, Cursor)
	// lists nothing: the launcher links ~/Library for every harness.
	AuthFiles() []string

	// ConfigDirIn is the harness's configuration directory under a given home: where
	// its transcripts land in a sandbox whose HOME is not the process's own.
	ConfigDirIn(home string) string

	// SessionID is the harness's own id of the session whose record is at path
	// (Claude's session id, Codex's thread id, Cursor's chat id), "" when the path
	// names none. A multi-turn launcher resumes by exactly this id, never by recency.
	SessionID(transcriptPath string) string

	// InstallPlugins installs sloprail's plugin (and any further ones from the same
	// marketplace) for a project, the way a user does, into the sandbox.
	InstallPlugins(ctx context.Context, in PluginInstall) error
}

// PluginInstall is one plugin installation into a sandbox.
type PluginInstall struct {
	// Env is the sandbox's environment (HOME and PATH included).
	Env []string
	// Project is the project directory; Home is the sandbox's HOME.
	Project, Home string
	// BinDir holds this checkout's built sloprail binaries.
	BinDir string
	// Marketplace is the root of a snapshot of the repo's marketplace (every harness's
	// manifest directory is in it) and MarketplaceName is the name it is registered under.
	Marketplace, MarketplaceName string
	// Plugins are the plugins to install from it, by name.
	Plugins []string
}

// Lookup is the registered harness of that name.
func Lookup(name string) (Harness, bool) {
	mu.RLock()
	defer mu.RUnlock()
	h, ok := registry[name]
	return h, ok
}

// Names are the registered harnesses' names, sorted.
func Names() []string {
	mu.RLock()
	defer mu.RUnlock()
	return sortedNames()
}

// ProvisionerOf is h's Provisioner, or an error saying it has none.
func ProvisionerOf(h Harness) (Provisioner, error) {
	p, ok := h.(Provisioner)
	if !ok {
		return nil, fmt.Errorf("harness %q cannot be provisioned into a sandbox", h.Name())
	}
	return p, nil
}
