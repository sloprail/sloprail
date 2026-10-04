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
	"github.com/sloprail/sloprail/internal/judgelimit"
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

Every check (requirement, script, judge) is cached by content: one verdict per guard and subject, keyed
by the rule hash, the subject, the content of its files and the citation quotes. A stored PASS is a hit and a stored FAIL with the same key is replayed (terminal until the
input changes): nothing is run. A miss runs the steps in order and stores the verdict, as one segment
of the sloprail/checks branch, and pushed to origin when the repository has one. Prints each
refusal, and exits 1 when any rule refuses.

Run it in the foreground and wait: it prints progress to stderr (a line per judge and per rule, and a
heartbeat every 30s), so do not poll the process list. It is safe to run in parallel, in several
worktrees at once: judges share a machine-wide limit (SLOPRAIL_JUDGE_SLOTS, default 8; a process waits for
a free slot), two runs judging the same check judge it once, and a second run of the same worktree and
range waits for the first and reuses its verdicts. The lock files are under the user cache dir
(SLOPRAIL_LOCK_DIR overrides) and a killed run releases them.`,
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

Only reads: it never executes a script, a judge or a requirement and never writes. It computes each
subject's key (running the rule's subjects script, without a session) and reads the stored verdict, so the
same repository state and the same stored results read the same anywhere. A key with no stored verdict is
red ("not judged yet", run sr-checks run); a stored fail shows its reasons. A citation requirement counts a
Sloprail-Cites-* trailer on the commit that last really changed the file, which is checkable from the repository
alone.

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
(nothing is asked of a model, nothing is written), over the whole range asked about (verify
starts from each rule's effective base instead), and always exit 0: a reader's view, not a gate.

--failing keeps only what is not passing: a fail, a result still missing, or an error.
--rule limits the listing to one file-guard, by folder name or qualified name.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return execute(cmd, modeShow) },
	}
	addRangeFlags(cmd)
	cmd.Flags().Bool("json", false, "Print the per-subject results as JSON")
	cmd.Flags().Bool("failing", false, "Only what is not passing")
	cmd.Flags().String("rule", "", "Only this file-guard (folder name or qualified name)")
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
	record, id, agentID, workspace string
	subagent                       bool
	state                          sessionstate.Store
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
				// A sub-agent's Bash shares its parent's session record; what says the folder is
				// a sub-agent's is the session's own row for it.
				if f, ok, err := st.Folder(id.ID, root); err == nil && ok && f.AgentID != "" {
					s.adoptSubagent(f.AgentID)
				}
			}
		}
	}
	return s
}

// adoptSubagent makes this session the sub-agent's: the session id stays the parent's (the state
// is keyed on it), but the record a citation resolves in is the sub-agent's own transcript, and
// what is recorded carries its agent id.
func (s *session) adoptSubagent(agentID string) {
	s.subagent = true
	s.agentID = agentID
	if path, err := transcript.SubagentTranscriptPath(s.record, agentID); err == nil {
		s.record = path
	}
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
	// The config at the range's base is what the user committed before this work began: it alone
	// may switch off a protected rule. A disable the range itself introduces is not honoured.
	reg, err := modules.Registry()
	if err != nil {
		return target{}, err
	}
	// A store that cannot be read is a refusal here, not zero guards: a CI `verify` must not pass
	// over rules it could not see. (The Stop path stays lenient: it must not brick the turn.)
	loaded, err := checkrun.LoadDeclarationsStrict(cmd.ErrOrStderr(), root, reg, r.Base)
	if err != nil {
		return target{}, fmt.Errorf("sloprail: the declarations in this project could not be read: %w", err)
	}
	return target{root: root, rng: r, loaded: loaded, sess: openSession(root)}, nil
}

func execute(cmd *cobra.Command, m mode) error {
	out, err := evaluate(cmd, m)
	if err != nil {
		return err
	}
	if len(out) == 0 {
		return nil
	}
	fmt.Fprintln(cmd.OutOrStdout(), joinRefusals(out))
	os.Exit(1)
	return nil
}

// evaluate does what run, verify and show do over the range the command's flags name, and
// returns the refusals instead of exiting: `sr-checks test` runs it in-process.
func evaluate(cmd *cobra.Command, m mode) ([]string, error) {
	asJSON, _ := cmd.Flags().GetBool("json")
	t, err := resolveTarget(cmd)
	if err != nil {
		return nil, err
	}
	defer t.sess.close()
	defer gitrepo.CleanupOnSignal()() // a killed run must not leak its read-only snapshots
	broken := brokenFileGuards(t.loaded)
	if len(t.loaded.FileGuards) == 0 {
		if m != modeShow && len(broken) > 0 {
			return broken, nil
		}
		return nil, nil
	}
	if m == modeRun {
		// A second run of this worktree over the same range waits for the first and then finds
		// its verdicts stored, instead of judging them again.
		l := judgelimit.New(cmd.ErrOrStderr())
		release, err := l.AcquireRun(judgelimit.Name(t.root, t.rng.Base, t.rng.Head),
			"another sr-checks run of this worktree over the same range to finish")
		if err != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: run lock unavailable, running anyway:", err)
		} else {
			defer release()
		}
	}
	cache, err := checkrun.OpenCache(cmd.ErrOrStderr(), t.root, m == modeRun)
	if err != nil {
		return nil, err
	}
	results := checkstore.Open(cache, m != modeRun)
	refusals, outcomes := checkrun.Evaluate(checkrun.Params{
		Err: cmd.ErrOrStderr(), Guards: t.loaded.FileGuards, Root: t.root, Range: t.rng,
		Cwd: t.root, Transcript: t.sess.record, Workspace: t.sess.workspace, SessionID: t.sess.id, AgentID: t.sess.agentID, Subagent: t.sess.subagent,
		Store: results, Verify: m != modeRun, WholeRange: m == modeShow, Recorded: recordedCitations(t.sess, t.root),
	})
	if err := results.Close(); err != nil {
		return nil, fmt.Errorf("sloprail: the verdicts could not be stored: %w", err)
	}
	// Local-first: the verdicts are safe locally, but a push that failed must not be silent.
	// A `run` already pushed what an earlier run left pending; verify and show only read, so
	// they say when verdicts are still waiting for a run to push them.
	checkrun.WarnPending(cmd.ErrOrStderr(), cache)

	w := cmd.OutOrStdout()
	if m == modeShow {
		outcomes = filterShown(cmd, outcomes)
	}
	if m != modeRun {
		if asJSON {
			enc := json.NewEncoder(w)
			enc.SetIndent("", "  ")
			if err := enc.Encode(outcomes); err != nil {
				return nil, err
			}
		} else {
			for _, o := range outcomes {
				line := fmt.Sprintf("%-7s %s  %s/%s  (%s)", o.Status, o.Rule, o.Subject, o.Kind, o.Source)
				if o.Status == "skipped" && o.Reason != "" {
					line += "  " + o.Reason
				}
				fmt.Fprintln(w, line)
			}
		}
	}
	if m == modeShow {
		return nil, nil
	}
	out := broken
	for _, f := range refusals {
		out = append(out, f.Reason+" (file-guard "+f.Attribution+")")
	}
	return out, nil
}

// brokenFileGuards names every file-guard that failed to load, with why: a rule that cannot be
// read judges nothing, so a run that passes over it must fail instead of reading as clean.
func brokenFileGuards(l declaration.Loaded) []string { return checkrun.BrokenFileGuards(l) }

// joinRefusals renders the collected refusals as one block, naming each rule: a refusal an
// agent cannot attribute to a rule is one it cannot act on.
func joinRefusals(refusals []string) string {
	if len(refusals) == 1 {
		return refusals[0]
	}
	return "the following rules refused this work:\n  - " + strings.Join(refusals, "\n  - ")
}

// filterShown applies show's --failing and --rule to the listing. The refusals are not a part
// of show, so a filter only ever narrows what is printed.
func filterShown(cmd *cobra.Command, in []checkrun.CheckOutcome) []checkrun.CheckOutcome {
	failing, _ := cmd.Flags().GetBool("failing")
	rule, _ := cmd.Flags().GetString("rule")
	if !failing && rule == "" {
		return in
	}
	out := []checkrun.CheckOutcome{}
	for _, o := range in {
		if rule != "" && o.Rule != rule && !strings.HasSuffix(o.Rule, "/"+rule) {
			continue
		}
		if failing && (o.Status == "pass" || o.Status == "skipped") {
			continue
		}
		out = append(out, o)
	}
	return out
}
