package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/checkrun"
	"github.com/sloprail/sloprail/internal/checkstore"
	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/module"
	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/sloprail/sloprail/internal/transcript"
)

// The ranges a session answers for.
//
// Tracking lives in the SESSION; the checks stay stateless (`sr-checks run|verify` are given a
// range and know nothing of sessions). A session registers the folders it works in (root,
// sub-agent worktrees, repositories a command ran in) and, per folder, the ranges of commits it
// is answerable for: when a folder is discovered its current branch is tracked from where the
// work started (the merge base with the default branch), and the agent may track another range
// (`sr-session refs track`) or drop one with a reason (`sr-session refs untrack`). A drop is
// free — CI is the backstop — but the Stop lists it.
//
// At Stop each tracked range is VERIFIED, never judged: the same deterministic logic as
// `sr-checks verify`, which calls no model and writes nothing. A range whose judges have not
// been asked is refused with the command that asks them.

// trackedHead is the head a folder's current line of work is tracked under: its branch, or the
// commit for a detached HEAD. ok is false for a repository with no commit.
func trackedHead(folder string) (head, sha string, ok bool) {
	pos, err := gitrepo.Head(folder)
	if err != nil || pos.Commit == "" {
		return "", "", false
	}
	head = pos.Branch
	if head == "" {
		head = pos.Commit
	}
	return head, pos.Commit, true
}

// autoBase is where a folder's current work started: the merge base with the default branch;
// when that is the head itself (the work is on the default branch, so nothing is "ahead"), the
// HEAD the folder was registered at.
func autoBase(folder, sha, startedAt string) string {
	base := gitrepo.DefaultBase(folder, sha)
	if base == sha && startedAt != "" {
		if startedAt == sessionstate.FolderBaseUnborn {
			return gitrepo.EmptyTree
		}
		return startedAt
	}
	return base
}

// ensureTracked tracks a folder's current branch, automatically, unless that range is already
// there (what the agent changed or dropped stays so). startedAt is the folder's registered
// BaseRef.
func ensureTracked(reg sessionstate.Store, sessionID, folder, agent, startedAt string) {
	head, sha, ok := trackedHead(folder)
	if !ok {
		return
	}
	_ = reg.TrackRange(sessionstate.TrackedRange{
		SessionID: sessionID, Folder: filepath.Clean(folder), Head: head, HeadSHA: sha,
		Base: autoBase(folder, sha, startedAt), AddedBy: sessionstate.RangeAuto, AgentID: agent,
	})
}

// trackFolders makes sure the current branch of this agent's folders is tracked: the tree it
// stands in and the folders registered for it.
func trackFolders(reg sessionstate.Store, rs rootSession, p HookPayload) {
	folders, err := reg.Folders(rs.ID)
	if err != nil {
		return
	}
	for _, f := range folders {
		if f.AgentID != p.AgentID {
			continue
		}
		if st, err := os.Stat(f.Path); err != nil || !st.IsDir() {
			continue
		}
		ensureTracked(reg, rs.ID, f.Path, f.AgentID, f.BaseRef)
	}
}

// untrackGone drops, with the reason, the ranges of folders that no longer exist (a worktree
// removed): there is nothing left to verify there.
func untrackGone(reg sessionstate.Store, sessionID string, ranges []sessionstate.TrackedRange) {
	for _, r := range ranges {
		if !r.Tracked() {
			continue
		}
		if st, err := os.Stat(r.Folder); err != nil || !st.IsDir() {
			_ = reg.UntrackRange(sessionID, r.Folder, r.Head, "worktree removed", r.AgentID)
		}
	}
}

// verifyTrackedRanges is the Stop's file-guard work: each tracked range of this agent's folders
// is verified against the stored results (no model is asked, nothing is written), and the
// refusals are returned, with a note on what the agent untracked.
func verifyTrackedRanges(cmd *cobra.Command, p HookPayload, reg *module.Registry, store sessionstate.Store) []string {
	rs, err := resolveRootSession(p)
	if err != nil {
		return nil // no session identity, so no ranges
	}
	if _, err := os.Stat(rs.Path); err != nil {
		return nil
	}
	root, err := sessionstate.Open(rs.Path)
	if err != nil {
		return []string{fmt.Sprintf("the session's tracked ranges could not be read (%v); refusing because a registry that could not be read must not be read as 'nothing to judge'", err)}
	}
	defer root.Close()
	trackFolders(root, rs, p)
	ranges, err := root.Ranges(rs.ID)
	if err != nil {
		return []string{fmt.Sprintf("the session's tracked ranges could not be read (%v); refusing because a registry that could not be read must not be read as 'nothing to judge'", err)}
	}
	untrackGone(root, rs.ID, ranges)
	if ranges, err = root.Ranges(rs.ID); err != nil {
		return nil
	}

	quiet := &cobra.Command{}
	quiet.SetOut(io.Discard)
	quiet.SetErr(io.Discard)
	var out, notes []string
	for _, r := range ranges {
		if r.AgentID != p.AgentID {
			continue
		}
		if !r.Tracked() {
			notes = append(notes, fmt.Sprintf("untracked: %s %s (reason: %s)", r.Folder, r.Head, r.UntrackedReason))
			continue
		}
		if reason := verifyRange(cmd, p, reg, store, quiet, r); reason != "" {
			out = append(out, reason)
		}
	}
	if len(notes) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: "+strings.Join(notes, "; "))
		if len(out) > 0 {
			out = append(out, "Not verified, because it was untracked: "+strings.Join(notes, "; ")+".")
		}
	}
	return out
}

// verifyRange verifies one tracked range, returning the refusal or "".
func verifyRange(cmd *cobra.Command, p HookPayload, reg *module.Registry, store sessionstate.Store, quiet *cobra.Command, r sessionstate.TrackedRange) string {
	where := fmt.Sprintf("In %s (%s, from %s)", r.Folder, r.Head, shortRev(r.Base))
	head := r.Head
	goneNote := ""
	if _, err := gitrepo.ResolveRange(r.Folder, "HEAD", "refs/heads/"+r.Head); err != nil {
		// Not a branch (or not any more): a commit it was tracked at is still verifiable.
		if _, serr := gitrepo.ResolveRange(r.Folder, "HEAD", r.Head); serr == nil {
			head = r.Head // a sha, or another revision
		} else if r.HeadSHA != "" {
			head = r.HeadSHA
			goneNote = fmt.Sprintf(" The branch %q is gone: verified at the commit it last pointed at (%s). Re-track the range under another head (`sr-session refs track`) or untrack it with a reason (`sr-session refs untrack`).", r.Head, shortRev(r.HeadSHA))
		}
	} else {
		head = "refs/heads/" + r.Head
	}
	rng, err := gitrepo.ResolveRange(r.Folder, r.Base, head)
	if err != nil {
		return fmt.Sprintf("%s: the range cannot be read (%v). Re-track it (`sr-session refs track`) or untrack it with a reason (`sr-session refs untrack`).%s", where, err, goneNote)
	}
	loaded := newNatureDeclarations(quiet, r.Folder, reg)
	if len(loaded.FileGuards) == 0 {
		return ""
	}
	cache, err := checkrun.OpenLocalCache(r.Folder)
	if err != nil {
		return fmt.Sprintf("%s: the check results could not be opened (%v); refusing because results that could not be read must not be read as 'nothing was judged'.", where, err)
	}
	results := checkstore.Open(cache, true)
	defer results.Close()
	contextMap := checkrun.LoadContextMap(io.Discard, store, loaded.Contexts)
	refusals, _ := checkrun.Evaluate(checkrun.Params{
		Err: io.Discard, Guards: loaded.FileGuards, Root: r.Folder, Range: rng, Cwd: r.Folder,
		Workspace: r.Folder, AgentID: p.AgentID, Subagent: p.IsSubagent(),
		ContextMap: contextMap, Store: results, Verify: true,
	})
	if len(refusals) == 0 {
		return ""
	}
	var parts []string
	for _, f := range refusals {
		parts = append(parts, f.Reason+" (file-guard "+f.Attribution+")")
	}
	return where + ": " + joinRefusals(parts) + goneNote
}

func shortRev(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// ---- sr-session refs track | untrack | list ----

func newSessionRefsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "refs",
		Short: "The ranges of commits this session answers for: list, track, untrack",
		Long: `The ranges of commits this session answers for, per folder.

When the session discovers a folder (its own repository, a sub-agent's worktree, a repository a
command ran in) it tracks the folder's current branch, from where the work started (the merge
base with the default branch). At Stop each tracked range is verified: every file-guard must
have a stored verdict for the content, or the Stop is refused with the ` + "`sr-checks run`" + ` command that
produces it. Nothing here judges anything.

  sr-session refs list                                   the tracked and untracked ranges
  sr-session refs track   [--folder D] [--base REV] [--head REF]   track a range (replaces its base)
  sr-session refs untrack  --reason TEXT [--folder D] [--head REF]  stop answering for a range

Untracking is allowed freely — CI is the backstop — but the Stop lists it with your reason.`,
	}
	cmd.AddCommand(newRefsListCmd(), newRefsTrackCmd(), newRefsUntrackCmd())
	return cmd
}

// refsSession is what a refs command works in: the root session's store.
type refsSession struct {
	rs    rootSession
	reg   sessionstate.Store
	agent string
}

func openRefsSession(cmd *cobra.Command) (refsSession, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return refsSession{}, err
	}
	p := readPayloadIfWaiting(cmd)
	if p.Cwd == "" {
		p.Cwd = cwd
	}
	if rec, err := p.record(); err == nil && rec != "" {
		p.TranscriptPath = rec
	} else if cur := transcript.CurrentSessionPath(p.Cwd); cur != "" {
		p.TranscriptPath = cur
	}
	rs, err := resolveRootSession(p)
	if err != nil {
		return refsSession{}, fmt.Errorf("sloprail: this is run from inside a session (%w)", err)
	}
	reg, err := sessionstate.Open(rs.Path)
	if err != nil {
		return refsSession{}, err
	}
	return refsSession{rs: rs, reg: reg, agent: p.AgentID}, nil
}

// folderOrCwd is the git root of --folder, or of the working directory.
func folderOrCwd(folder string) (string, error) {
	if folder == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		folder = wd
	}
	root, err := gitrepo.Root(folder)
	if err != nil || root == "" {
		return "", fmt.Errorf("sloprail: %s is not inside a git repository", folder)
	}
	return filepath.Clean(root), nil
}

// ownerOf is the agent that owns a registered folder ("" for the root's or an unknown one).
func (s refsSession) ownerOf(folder string) string {
	if f, found, err := s.reg.Folder(s.rs.ID, folder); err == nil && found {
		return f.AgentID
	}
	return s.agent
}

func newRefsListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "The tracked and untracked ranges of this session",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := openRefsSession(cmd)
			if err != nil {
				return err
			}
			defer s.reg.Close()
			ranges, err := s.reg.Ranges(s.rs.ID)
			if err != nil {
				return err
			}
			if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(ranges)
			}
			for _, r := range ranges {
				state := "tracked"
				if !r.Tracked() {
					state = "untracked (" + r.UntrackedReason + ")"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s  %s  base %s  by %s  %s\n", r.Folder, r.Head, shortRev(r.Base), r.AddedBy, state)
			}
			return nil
		},
	}
	cmd.Flags().Bool("json", false, "Print the ranges as JSON")
	return cmd
}

func newRefsTrackCmd() *cobra.Command {
	var folder, base, head string
	cmd := &cobra.Command{
		Use:   "track [--folder <dir>] [--base <rev>] [--head <ref>]",
		Short: "Track a range of commits in a folder (replacing its base if it is already tracked)",
		Long: `Track a range of commits the session answers for. Without flags: the current branch of the
working directory's repository, from the merge base with the default branch. --head is a branch
(the range follows it) or a commit; --base a revision.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := folderOrCwd(folder)
			if err != nil {
				return err
			}
			s, err := openRefsSession(cmd)
			if err != nil {
				return err
			}
			defer s.reg.Close()
			headName, headSHA, ok := trackedHead(dir)
			if head != "" {
				headName = head
				out, err := gitrepo.ResolveRange(dir, "HEAD", head)
				if err != nil {
					return fmt.Errorf("sloprail: %w", err)
				}
				headSHA, ok = out.Head, true
			}
			if !ok {
				return fmt.Errorf("sloprail: %s has no commit to track", dir)
			}
			if base == "" {
				started := ""
				if f, found, _ := s.reg.Folder(s.rs.ID, dir); found {
					started = f.BaseRef
				}
				base = autoBase(dir, headSHA, started)
			} else if _, err := gitrepo.ResolveRange(dir, base, headSHA); err != nil {
				return fmt.Errorf("sloprail: %w", err)
			}
			if err := s.reg.TrackRange(sessionstate.TrackedRange{
				SessionID: s.rs.ID, Folder: dir, Head: headName, HeadSHA: headSHA, Base: base,
				AddedBy: sessionstate.RangeAgent, AgentID: s.ownerOf(dir),
			}); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "tracking %s in %s from %s\n", headName, dir, shortRev(base))
			return nil
		},
	}
	cmd.Flags().StringVar(&folder, "folder", "", "A directory inside the repository (default: the working directory)")
	cmd.Flags().StringVar(&base, "base", "", "The base revision (default: the merge base with the default branch)")
	cmd.Flags().StringVar(&head, "head", "", "The head: a branch or commit (default: the current branch)")
	return cmd
}

func newRefsUntrackCmd() *cobra.Command {
	var folder, head, reason string
	cmd := &cobra.Command{
		Use:   "untrack --reason <text> [--folder <dir>] [--head <ref>]",
		Short: "Stop answering for a range of commits, saying why",
		Long: `Stop verifying a tracked range at Stop. Allowed freely — CI is the backstop — but the Stop
lists what was untracked with the reason you give, so say it plainly.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(reason) == "" {
				return fmt.Errorf("sloprail: --reason is required: say why the range is not yours to answer for")
			}
			dir, err := folderOrCwd(folder)
			if err != nil {
				return err
			}
			s, err := openRefsSession(cmd)
			if err != nil {
				return err
			}
			defer s.reg.Close()
			if head == "" {
				h, _, ok := trackedHead(dir)
				if !ok {
					return fmt.Errorf("sloprail: %s has no commit; name the range with --head", dir)
				}
				head = h
			}
			if err := s.reg.UntrackRange(s.rs.ID, dir, head, reason, s.ownerOf(dir)); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "untracked %s in %s: %s\n", head, dir, reason)
			return nil
		},
	}
	cmd.Flags().StringVar(&folder, "folder", "", "A directory inside the repository (default: the working directory)")
	cmd.Flags().StringVar(&head, "head", "", "The range's head (default: the current branch)")
	cmd.Flags().StringVar(&reason, "reason", "", "Why the range is dropped (required)")
	return cmd
}
