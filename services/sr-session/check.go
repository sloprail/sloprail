package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/checkcache"
	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/module/modules"
	"github.com/sloprail/sloprail/internal/sessionpath"
)

// newCheckCmd is `sr check`: the file-guards judged over an EXPLICIT range of commits.
//
// A file-guard judges commits, and which commits is stated by the caller: --base and
// --head, both required. The range judged is merge-base(base, head)..head, so a base
// that is behind (a stale branch) only widens it. Nothing here tracks a session, a
// branch or what was judged before: the same content in the range is the same input,
// and a judge's PASS for that input is reused wherever it was made.
func newCheckCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Judge the file-guards over an explicit commit range: run, verify",
		Long: `Judge the file-guards over an explicit range of commits.

  sr check run    --base <rev> --head <rev>   judge, asking a model where there is no stored pass; writes the results
  sr check verify --base <rev> --head <rev>   deterministic: asks no model, writes nothing

The range is merge-base(--base, --head)..--head. --base and --head are required: the
caller states the range. A judge's verdict is keyed by the rule, its definition, the check
and a fingerprint of everything the judge was given — never by a commit, session or
agent — so the same content after a rebase, a squash or a revert is a cache hit.`,
	}
	cmd.AddCommand(newCheckRunCmd(), newCheckVerifyCmd())
	return cmd
}

func newCheckRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run --base <rev> --head <rev>",
		Short: "Judge every file-guard over merge-base(base, head)..head and record the verdicts",
		Long: `Judge every file-guard over merge-base(--base, --head)..--head.

Requirements and scripts run every time. A judge is asked only when the cache holds no
PASS for exactly what it is about to be given; its verdict (pass or fail) is then stored.
Prints each refusal, and exits 1 when any rule refuses.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runCheck(cmd, false) },
	}
	addRangeFlags(cmd)
	return cmd
}

func newCheckVerifyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "verify --base <rev> --head <rev>",
		Short: "Check every file-guard over merge-base(base, head)..head against the stored verdicts",
		Long: `Check every file-guard over merge-base(--base, --head)..--head, deterministically.

Requirements and scripts are re-run; a judge is never asked and nothing is written: its
key is looked up, and a key with no stored pass is red (missing, or the stored fail's
reasons). Prints each subject's latest result, then each refusal. Exits 0 when everything
passes, 1 when anything fails or has no result.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runCheck(cmd, true) },
	}
	addRangeFlags(cmd)
	cmd.Flags().Bool("json", false, "Print the per-subject results as JSON")
	return cmd
}

func addRangeFlags(cmd *cobra.Command) {
	cmd.Flags().String("base", "", "The base revision: a branch, tag or sha (required)")
	cmd.Flags().String("head", "", "The head revision: a branch, tag or sha (required)")
	_ = cmd.MarkFlagRequired("base")
	_ = cmd.MarkFlagRequired("head")
}

// openCheckCache is the repository's check cache.
func openCheckCache(root string) (checkcache.Cache, error) {
	path, err := sessionpath.ChecksDB(root)
	if err != nil {
		return nil, err
	}
	return checkcache.OpenFile(path), nil
}

func runCheck(cmd *cobra.Command, verify bool) error {
	baseRev, _ := cmd.Flags().GetString("base")
	headRev, _ := cmd.Flags().GetString("head")
	asJSON, _ := cmd.Flags().GetBool("json")

	cwd := os.Getenv(WorkspaceEnv)
	if cwd == "" || cwd == unresolvedWorkspace {
		wd, err := os.Getwd()
		if err != nil {
			return err
		}
		cwd = wd
	}
	root, err := gitrepo.Root(cwd)
	if err != nil || root == "" {
		return fmt.Errorf("sloprail: %s is not inside a git repository", cwd)
	}
	root = filepath.Clean(root)
	r, err := gitrepo.ResolveRange(root, baseRev, headRev)
	if err != nil {
		return fmt.Errorf("sloprail: %w", err)
	}

	p := HookPayload{Cwd: root, TranscriptPath: os.Getenv(TranscriptEnv), SessionID: os.Getenv(SessionEnv)}
	reg, err := modules.Registry()
	if err != nil {
		return err
	}
	scope := natureHookScope(cmd, p)
	loaded := newNatureDeclarations(cmd, root, reg)
	if len(loaded.FileGuards) == 0 {
		return nil
	}
	store := natureStore(cmd, p)
	if store != nil {
		defer store.Close()
	}
	cache, err := openCheckCache(root)
	if err != nil {
		return err
	}
	contextMap := contextsOf(cmd, store, loaded.Contexts)
	refusals, outcomes := evaluateChangesets(cmd, loaded.FileGuards, p, scope, root, r, contextMap, store, cache, verify)

	var out []string
	for _, f := range refusals {
		out = append(out, f.Reason+" (file-guard "+f.Attribution+")")
	}
	w := cmd.OutOrStdout()
	if verify {
		if asJSON {
			enc := json.NewEncoder(w)
			enc.SetIndent("", "  ")
			if err := enc.Encode(outcomes); err != nil {
				return err
			}
		} else {
			for _, o := range outcomes {
				fmt.Fprintf(w, "%-7s %s  %s/%s  (%s)\n", o.Status, o.Rule, o.Subject, o.Kind, o.Source)
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	fmt.Fprintln(w, joinRefusals(out))
	os.Exit(1)
	return nil
}
