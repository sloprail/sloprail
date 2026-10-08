package main

import (
	"encoding/json"
	"os"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/checkrun"
	"github.com/sloprail/sloprail/internal/harness"
)

// newSessionPluginsCmd is `sr-session plugins`: the plugins the current harness resolves for the
// project, one JSON line each. It is the one place a script asks "which plugin is installed here,
// at which version", so no script reads a harness's own install records.
func newSessionPluginsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "plugins",
		Short: "The plugins the harness resolves for this project, as JSON lines",
		Long: `The plugins the current harness resolves for this project.

  sr-session plugins    one JSON line per plugin: {"name": ..., "version": ..., "root": ...}

root is the folder holding the plugin's files; version is the one its plugin.json declares ("" when it
declares none). Plugins that could not be found are reported on stderr and not listed.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			cwd, _ := os.Getwd()
			res, err := harness.Current().ResolvePlugins(checkrun.ProjectDir(cwd), home)
			if err != nil {
				return err
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			for _, r := range res.Roots {
				if err := enc.Encode(struct {
					Name    string `json:"name"`
					Version string `json:"version"`
					Root    string `json:"root"`
				}{r.Plugin.Name, pluginVersion(r.Dir), r.Dir}); err != nil {
					return err
				}
			}
			checkrun.ReportUnresolved(cmd.ErrOrStderr(), res.Unresolved)
			return nil
		},
	}
}

// pluginVersion is the version in the plugin.json a plugin folder holds, under whichever harness's
// manifest folder, or "".
func pluginVersion(dir string) string {
	for _, path := range harness.PluginManifestPaths(dir) {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var m struct {
			Version string `json:"version"`
		}
		if json.Unmarshal(raw, &m) == nil && m.Version != "" {
			return m.Version
		}
	}
	return ""
}
