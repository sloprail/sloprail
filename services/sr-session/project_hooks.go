package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/harness"
)

// newProjectHooksCmd registers, or takes back out, the hooks a harness's plugin cannot
// carry, in the project's own hooks file. install.sh and `make distribute-local` call it
// after the plugin is in place. A harness whose plugin carries every hook has nothing to do.
func newProjectHooksCmd() *cobra.Command {
	var dir, pluginDir string
	run := func(install bool) func(*cobra.Command, []string) error {
		return func(cmd *cobra.Command, _ []string) error {
			h := harness.Current()
			ph, ok := h.(harness.ProjectHooks)
			if !ok {
				fmt.Fprintf(cmd.OutOrStdout(), "%s: the plugin carries every hook; nothing to do\n", h.Name())
				return nil
			}
			if dir == "" {
				wd, err := os.Getwd()
				if err != nil {
					return err
				}
				dir = wd
			}
			if !install {
				return ph.RemoveProjectHooks(dir)
			}
			if pluginDir == "" {
				return fmt.Errorf("--plugin-dir is required: the installed plugin whose hooks the project runs")
			}
			abs, err := filepath.Abs(pluginDir)
			if err != nil {
				return err
			}
			return ph.InstallProjectHooks(dir, abs)
		}
	}
	cmd := &cobra.Command{
		Use:   "project-hooks",
		Short: "Register, or remove, the hooks a harness's plugin cannot carry, in the project's hooks file",
		Long: `Cursor's plugins never fire a stop hook and load too late for sessionStart, but
the project's .cursor/hooks.json fires both. This merges sloprail's entries for those
events into that file (keeping every other entry, idempotent) or removes only them.
The harness is the running one (SLOPRAIL_HARNESS); one whose plugin carries every
hook does nothing.

  sr-session project-hooks install --plugin-dir <installed plugin>
  sr-session project-hooks remove`,
	}
	install := &cobra.Command{Use: "install", Short: "Merge sloprail's entries into the project's hooks file", Args: cobra.NoArgs, RunE: run(true)}
	remove := &cobra.Command{Use: "remove", Short: "Remove sloprail's entries from the project's hooks file", Args: cobra.NoArgs, RunE: run(false)}
	cmd.PersistentFlags().StringVar(&dir, "dir", "", "the project directory (default: the current one)")
	install.Flags().StringVar(&pluginDir, "plugin-dir", "", "the installed sloprail plugin directory")
	cmd.AddCommand(install, remove)
	return cmd
}
