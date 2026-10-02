package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/checkrun"
	"github.com/sloprail/sloprail/internal/checkstore"
	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/module/modules"
	"github.com/sloprail/sloprail/internal/natures"
	"github.com/sloprail/sloprail/internal/sessionpath"
	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/sloprail/sloprail/internal/transcript"
)

func newRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run --base <rev> --head <rev>",
		Short: "Judge every file-guard over merge-base(base, head)..head and record the verdicts",
		Long: `Judge every file-guard over merge-base(--base, --head)..--head.

Requirements and scripts run every time. A judge is asked only when the cache holds no PASS for
exactly what it is about to be given; its verdict (pass or fail) is then stored, as one segment
of the sloprail/checks branch, and pushed to origin when the repository has one. Prints each
refusal, and exits 1 when any rule refuses.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return execute(cmd, modeRun) },
	}
	addRangeFlags(cmd)
	return cmd
}

func newVerifyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "verify --base <rev> --head <rev>",
		Short: "Check every file-guard over merge-base(base, head)..head against the stored verdicts",
		Long: `Check every file-guard over merge-base(--base, --head)..--head, deterministically.

Never asks a model and never writes: the same repository state and the same stored results
read the same anywhere, with no session. Scripts are re-run. A judge's key is looked up, and a
key with no stored pass is red (missing, or the stored fail's reasons). A requirement that
needs the session that made the change (a skill that must have been loaded, a context that
must have been open) is not re-checked here: it was checked where the session ran. A citation
requirement counts a Sloprail-Cites-* trailer on the commit that last changed the file, which
is checkable from the repository alone.

Prints each subject's latest result, then each refusal. Exits 0 when everything passes, 1 when
anything fails or has no result.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return execute(cmd, modeVerify) },
	}
	addRangeFlags(cmd)
	cmd.Flags().Bool("json", false, "Print the per-subject results as JSON")
	return cmd
}

func newShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show --base <rev> --head <rev>",
		Short: "Each file-guard subject's latest stored result over the range, without a verdict",
		Long: `Print each subject's latest result over merge-base(--base, --head)..--head, as verify reads it
(nothing is asked of a model, nothing is written), and always exit 0: a reader's view, not a gate.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return execute(cmd, modeShow) },
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

type mode int

const (
	modeRun mode = iota
	modeVerify
	modeShow
)

// session is the session this is run from, when there is one: the record a citation resolves
// in and the state the contexts are read from. Everything may be absent (CI, a bare checkout).
type session struct {
	record, id, workspace string
	subagent              bool
	state                 sessionstate.Store
}

func (s session) close() {
	if s.state != nil {
		s.state.Close()
	}
}

func openSession(root string) session {
	s := session{workspace: sessionpath.WorkspaceAnchor(root)}
	s.record = transcript.CurrentSessionPath(root)
	if s.record == "" {
		return s
	}
	s.subagent = transcript.SessionDirOfSubagent(s.record) != ""
	id, err := sessionpath.StableIdentity(s.record, root)
	if err != nil {
		return s
	}
	s.id = id.ID
	if path, err := sessionpath.StateDB(sessionpath.StateCwd(s.record, root), id.ID); err == nil {
		if _, err := os.Stat(path); err == nil {
			if st, err := sessionstate.Open(path); err == nil {
				s.state = st
			}
		}
	}
	return s
}

// target is what a command works over: the repository, the range, the rules and the session.
type target struct {
	root   string
	rng    gitrepo.Range
	loaded declaration.Loaded
	sess   session
}

func (t target) contexts(cmd *cobra.Command) map[string]natures.ContextState {
	return checkrun.LoadContextMap(cmd.ErrOrStderr(), t.sess.state, t.loaded.Contexts)
}

func resolveTarget(cmd *cobra.Command) (target, error) {
	baseRev, _ := cmd.Flags().GetString("base")
	headRev, _ := cmd.Flags().GetString("head")
	cwd, err := os.Getwd()
	if err != nil {
		return target{}, fmt.Errorf("sloprail: working directory: %w", err)
	}
	root, err := gitrepo.Root(cwd)
	if err != nil || root == "" {
		return target{}, fmt.Errorf("sloprail: %s is not inside a git repository", cwd)
	}
	root = filepath.Clean(root)
	r, err := gitrepo.ResolveRange(root, baseRev, headRev)
	if err != nil {
		return target{}, fmt.Errorf("sloprail: %w", err)
	}
	reg, err := modules.Registry()
	if err != nil {
		return target{}, err
	}
	return target{root: root, rng: r, loaded: checkrun.LoadDeclarations(cmd.ErrOrStderr(), root, reg), sess: openSession(root)}, nil
}

func execute(cmd *cobra.Command, m mode) error {
	asJSON, _ := cmd.Flags().GetBool("json")
	t, err := resolveTarget(cmd)
	if err != nil {
		return err
	}
	defer t.sess.close()
	if len(t.loaded.FileGuards) == 0 {
		return nil
	}
	cache, err := checkrun.OpenCache(cmd.ErrOrStderr(), t.root)
	if err != nil {
		return err
	}
	results := checkstore.Open(cache, m != modeRun)
	refusals, outcomes := checkrun.Evaluate(checkrun.Params{
		Err: cmd.ErrOrStderr(), Guards: t.loaded.FileGuards, Root: t.root, Range: t.rng,
		Cwd: t.root, Transcript: t.sess.record, Workspace: t.sess.workspace, SessionID: t.sess.id, Subagent: t.sess.subagent,
		ContextMap: t.contexts(cmd), Store: results, Verify: m != modeRun,
	})
	if err := results.Close(); err != nil {
		return fmt.Errorf("sloprail: the verdicts could not be stored: %w", err)
	}

	w := cmd.OutOrStdout()
	if m != modeRun {
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
	if m == modeShow {
		return nil
	}
	var out []string
	for _, f := range refusals {
		out = append(out, f.Reason+" (file-guard "+f.Attribution+")")
	}
	if len(out) == 0 {
		return nil
	}
	fmt.Fprintln(w, joinRefusals(out))
	os.Exit(1)
	return nil
}

// joinRefusals renders the collected refusals as one block, naming each rule: a refusal an
// agent cannot attribute to a rule is one it cannot act on.
func joinRefusals(refusals []string) string {
	if len(refusals) == 1 {
		return refusals[0]
	}
	return "the following rules refused this work:\n  - " + strings.Join(refusals, "\n  - ")
}
