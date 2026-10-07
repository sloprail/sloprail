package cursor

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/sloprail/sloprail/internal/harness"
	"github.com/sloprail/sloprail/internal/harness/cursor/record"
)

// Binary is Cursor's CLI agent.
const Binary = "cursor-agent"

// Binary implements harness.Provisioner.
func (Harness) Binary() string { return Binary }

// AuthFiles implements harness.Provisioner: cursor-agent's login is in the platform's
// keychain (measured: the token is a keychain item, not a file under ~/.cursor), which
// the launcher's ~/Library link carries.
func (Harness) AuthFiles() []string { return nil }

// ConfigDirIn implements harness.Provisioner.
func (Harness) ConfigDirIn(home string) string { return filepath.Join(home, ".cursor") }

// SessionID implements harness.Provisioner: the chat id is the conversation id
// agent-transcripts/<id>/<id>.jsonl names.
func (Harness) SessionID(path string) string { return record.Transcripts{}.ConversationID(path) }

// InstallPlugins implements harness.Provisioner: each plugin is copied from the
// marketplace snapshot into the user's local plugins, <home>/.cursor/plugins/local/<name>,
// which Cursor loads at start (see Resolve). Copied, not linked: Cursor rejects a link
// that points outside that directory.
func (Harness) InstallPlugins(_ context.Context, in harness.PluginInstall) error {
	for _, name := range in.Plugins {
		src := filepath.Join(in.Marketplace, "marketplace", "plugins", name)
		dst := filepath.Join(in.Home, ".cursor", "plugins", "local", name)
		if err := copyDir(src, dst); err != nil {
			return fmt.Errorf("install cursor plugin %s: %w", name, err)
		}
	}
	return nil
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case info.Mode()&fs.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		default:
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return os.WriteFile(target, body, info.Mode().Perm())
		}
	})
}
