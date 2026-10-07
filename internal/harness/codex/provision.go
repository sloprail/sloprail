package codex

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sloprail/sloprail/internal/harness"
)

// Binary is the Codex CLI's executable.
const Binary = "codex"

// Binary implements harness.Provisioner.
func (Harness) Binary() string { return Binary }

// AuthFiles implements harness.Provisioner: a Codex login (ChatGPT or API key) is
// auth.json in the config directory, which the launcher links (Codex rotates the
// refresh token and rewrites the file in place); the rest of it (config.toml, sessions, caches) is
// the operator's and stays behind.
func (Harness) AuthFiles() []string { return []string{filepath.Join(".codex", "auth.json")} }

// ConfigDirIn implements harness.Provisioner.
func (Harness) ConfigDirIn(home string) string { return filepath.Join(home, ".codex") }

// SessionID implements harness.Provisioner: a rollout is
// rollout-<timestamp>-<thread id>.jsonl, the thread id being the UUID at the end.
func (Harness) SessionID(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	if !strings.HasPrefix(base, "rollout-") || len(base) < 36 {
		return ""
	}
	id := base[len(base)-36:]
	if strings.Count(id, "-") != 4 {
		return ""
	}
	return id
}

// InstallPlugins implements harness.Provisioner: the marketplace is registered and each
// plugin added with `codex plugin`, as a user does, and then the plugin's hooks are
// trusted with `sr-session codex-trust` (Codex skips an untrusted hook silently, so an
// enabled but untrusted sloprail guards nothing).
func (Harness) InstallPlugins(ctx context.Context, in harness.PluginInstall) error {
	run := func(name string, args ...string) error {
		c := exec.CommandContext(ctx, name, args...)
		c.Dir = in.Project
		c.Env = in.Env
		if out, err := c.CombinedOutput(); err != nil {
			return fmt.Errorf("%s %s: %w: %s", filepath.Base(name), strings.Join(args[:min(len(args), 2)], " "), err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	if err := run(Binary, "plugin", "marketplace", "add", in.Marketplace); err != nil {
		return err
	}
	for _, name := range in.Plugins {
		if err := run(Binary, "plugin", "add", name+"@"+in.MarketplaceName); err != nil {
			return err
		}
	}
	return run(filepath.Join(in.BinDir, "sr-session"), "codex-trust", "--dir", in.Project)
}
