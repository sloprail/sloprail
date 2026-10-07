package claudecode

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sloprail/sloprail/internal/harness"
)

// Binary is Claude Code's executable.
const Binary = "claude"

// Binary implements harness.Provisioner.
func (Harness) Binary() string { return Binary }

// AuthFiles implements harness.Provisioner: Claude Code's login is in the keychain.
func (Harness) AuthFiles() []string { return nil }

// ConfigDirIn implements harness.Provisioner.
func (Harness) ConfigDirIn(home string) string { return filepath.Join(home, ".claude") }

// InstallPlugins implements harness.Provisioner: `claude plugin marketplace add` and
// `claude plugin install --scope project`, then the project's auto-memory switched off.
//
// Not a hand-written .claude/settings.json: that LOOKS right (`claude plugin list`
// reports the plugin enabled) but does not register the install in
// ~/.claude/plugins/installed_plugins.json, a project-path-keyed registry only the CLI
// install flow populates, and without it a plugin's hooks silently never fire on a fresh
// project path (measured: Stop hookCount 1 instead of 2, no error anywhere).
func (Harness) InstallPlugins(ctx context.Context, in harness.PluginInstall) error {
	run := func(args ...string) error {
		c := exec.CommandContext(ctx, Binary, args...)
		c.Dir = in.Project
		c.Env = in.Env
		if out, err := c.CombinedOutput(); err != nil {
			return fmt.Errorf("claude %s: %w: %s", strings.Join(args[:2], " "), err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	if err := run("plugin", "marketplace", "add", in.Marketplace); err != nil {
		return err
	}
	for _, name := range in.Plugins {
		if err := run("plugin", "install", name+"@"+in.MarketplaceName, "--scope", "project", "-y"); err != nil {
			return err
		}
	}
	return disableAutoMemory(in.Project)
}

// disableAutoMemory sets autoMemoryEnabled: false in the project's
// .claude/settings.json (the CLI install just wrote), merged in rather than
// overwritten. Without it the agent's cross-session auto-memory writes memories about a
// fixture's throwaway content into the operator's memory store, and the agent then
// confidently claims work it never did in the tree (it wrote to its memory tool instead).
func disableAutoMemory(project string) error {
	settingsPath := filepath.Join(project, ".claude", "settings.json")
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		return fmt.Errorf("read settings.json written by claude plugin install: %w", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(raw, &settings); err != nil {
		return fmt.Errorf("parse settings.json written by claude plugin install: %w", err)
	}
	settings["autoMemoryEnabled"] = false
	body, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("encode settings.json with autoMemoryEnabled: %w", err)
	}
	return os.WriteFile(settingsPath, body, 0o644)
}
