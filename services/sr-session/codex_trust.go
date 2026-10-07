package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/harness/codex/trust"
)

// newCodexTrustCmd records Codex's trust in sloprail's plugin hooks, or says that
// Codex is skipping them.
//
// Codex runs a hook only once it is trusted and skips the rest silently, so a
// sloprail plugin that is enabled but untrusted guards nothing and shows nothing.
// This is the one place that can say so: the hooks that would say it are the ones
// not running. install.sh and `make distribute-local` call it after the binaries are
// in place; run it again after a plugin upgrade that changed a hook's definition
// (internal/harness/codex/trust says what changes a hook's hash).
func newCodexTrustCmd() *cobra.Command {
	var check bool
	var dir, bin string
	cmd := &cobra.Command{
		Use:   "codex-trust",
		Short: "Trust sloprail's plugin hooks in Codex (Codex skips untrusted hooks silently)",
		Long: `Codex runs a plugin's hooks only after they are trusted, and skips the others
without a message: an enabled sloprail plugin whose hooks are untrusted guards nothing.

This asks Codex (codex app-server, the same calls its review dialog makes) which of
sloprail's hooks it would skip, and records trust for them. Enabling or upgrading the
plugin does not do it; changing a hook's definition in hooks.json drops its trust
again, and editing the hook's script does not.

  sr-session codex-trust            trust sloprail's untrusted or modified hooks
  sr-session codex-trust --check    only report; exit 1 when Codex would skip any

Run it where the project is (--dir) so project-layer hooks resolve the same way. With
no sloprail plugin enabled in Codex it finds nothing and changes nothing.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if dir == "" {
				wd, err := os.Getwd()
				if err != nil {
					return err
				}
				dir = wd
			}
			res, err := trust.Run(cmd.Context(), bin, dir, check)
			if errors.Is(err, trust.ErrNoCodex) {
				fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: codex-trust:", err, "(nothing to do)")
				return nil
			}
			if err != nil {
				return fmt.Errorf("codex-trust: %w", err)
			}
			out := cmd.OutOrStdout()
			switch {
			case res.Sloprail == 0:
				fmt.Fprintln(out, "no sloprail plugin hooks are known to Codex (enable the plugin first: codex plugin add sloprail@<marketplace>)")
			case len(res.Pending) == 0:
				fmt.Fprintf(out, "all %d sloprail hooks are trusted\n", res.Sloprail)
			case res.Trusted:
				fmt.Fprintf(out, "trusted %d of %d sloprail hooks\n", len(res.Pending), res.Sloprail)
			default:
				for _, h := range res.Pending {
					fmt.Fprintf(out, "Codex skips %s (%s)\n", h.Key, h.TrustStatus)
				}
				return fmt.Errorf("codex-trust: Codex would skip %d sloprail hooks; run `sr-session codex-trust`", len(res.Pending))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "only report the hooks Codex would skip; change nothing")
	cmd.Flags().StringVar(&dir, "dir", "", "the project directory (default: the current one)")
	cmd.Flags().StringVar(&bin, "codex", "codex", "the codex binary")
	return cmd
}
