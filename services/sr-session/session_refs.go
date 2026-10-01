package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/checkstore"
	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/natures"
	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/sloprail/sloprail/internal/transcript"
)

// The refs a session left commits on: what it touched, as data.
//
// A file-guard used to judge only base..HEAD, so a branch the agent committed on and
// then checked out away from was never judged and its pull request merged unchecked.
// Now every tip of a line of history the session (or a sub-agent) made commits on is a
// row of the root store's session_refs, per folder, and at Stop each one is judged by
// the same rule-range logic as HEAD.
//
// The rows come from two places, and the Stop judges both:
//   - the engine's own observation: at each Stop the HEAD reflog of the folder (a
//     worktree has its own) is read for the commits made since the session began, on any
//     branch or on a detached HEAD, and every line of history that is still reachable
//     (or was left detached) is recorded;
//   - `sr-session refs add`, for a ref the engine has not seen (or to patch a session
//     that began before this existed).

// stopTip is one line of history judged at Stop. The zero value is HEAD.
type stopTip struct {
	Sha string
	Ref string
	// Start is where the ref was created (its oldest reflog entry), or "": a floor for
	// the range so upstream commits merged before the branch was cut are not judged.
	Start string
	// Landed is true when the tip's changes are already upstream (squash-merged) yet it is
	// still owed a judgement: only what still stands upstream is judged (see prepare).
	Landed bool
}

// describe is what a refusal for this tip says first: which branch, in which folder,
// and how to fix it. Empty for HEAD.
func (t stopTip) describe(folder string) string {
	if t.Sha == "" {
		return ""
	}
	name := strings.TrimPrefix(strings.TrimPrefix(t.Ref, "refs/heads/"), "refs/remotes/")
	drop := fmt.Sprintf("If the USER wants this branch dropped, ask them, then run `sr-session refs abandon --ref %s --folder %s "+
		"--cite-user '<their exact words>'` citing what they said. ", shellQuote(name), shellQuote(folder))
	// Never tell the agent to switch this folder's checkout: it is the coordination
	// worktree, and the ref is judged on its own tree wherever it lives.
	if strings.HasPrefix(t.Ref, "detached/") {
		return fmt.Sprintf("On commits made on a detached HEAD and left (%s, in %s), which are not checked out: "+
			"they are judged on their own tree. Give them a branch and a worktree of their own "+
			"(`git -C %s branch <name> %s`, then `git -C %s worktree add <path> <name>`), fix there, commit, and stop again; "+
			"do not switch this folder's checkout. %s",
			short(t.Sha), folder, shellQuote(folder), short(t.Sha), shellQuote(folder), drop)
	}
	if other := gitrepo.CheckedOutAt(folder, t.Ref); other != "" {
		return fmt.Sprintf("On branch %s, which is checked out in another worktree (%s): this session committed on it, "+
			"so its commits are judged too, on that branch's own tree. Fix it in that worktree (`cd %s`), commit, and stop again; "+
			"do not switch this folder (%s) to it. %s",
			name, other, shellQuote(other), folder, drop)
	}
	return fmt.Sprintf("On branch %s (in %s), which is not checked out: this session committed on it, "+
		"so its commits are judged too, on that branch's own tree. Fix it in a new worktree "+
		"(`git -C %s worktree add <path> %s`), commit there, and stop again; do not switch this folder's checkout. %s",
		name, folder, shellQuote(folder), shellQuote(name), drop)
}

// evaluateChangesets judges every file-guard over HEAD and then over every other tip the
// session recorded in this folder. A commit shared between tips is judged once per rule:
// a tip a later tip contains is dropped, and a rule's watermark (a pass reachable from
// the tip) starts each range after what already passed.
func evaluateChangesets(cmd *cobra.Command, guards []declaration.FileGuard, p HookPayload, scope hookScope, root string,
	contextMap map[string]natures.ContextState, state sessionstate.Store, results checkstore.Store) []fileGuardResult {
	if len(guards) == 0 {
		return nil
	}
	out := evaluateChangesetsAt(cmd, guards, p, scope, root, stopTip{}, contextMap, state, results)
	for _, t := range stopTips(cmd, p, root, guards, results) {
		out = append(out, evaluateChangesetsAt(cmd, guards, p, scope, root, t, contextMap, state, results)...)
	}
	return out
}

// stopTips records what the folder's reflog shows and returns the tips to judge besides
// HEAD: every recorded ref of this agent in this folder, refreshed from the ref, minus
// what HEAD contains and what another tip contains.
func stopTips(cmd *cobra.Command, p HookPayload, root string, guards []declaration.FileGuard, results checkstore.Store) []stopTip {
	warn := func(err error) {
		fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: the session's other branches were not read: %v\n", err)
	}
	rs, err := resolveRootSession(p)
	if err != nil {
		return nil // no session to record in (a bare payload)
	}
	if _, err := os.Stat(rs.Path); err != nil {
		return nil
	}
	reg, err := sessionstate.Open(rs.Path)
	if err != nil {
		warn(err)
		return nil
	}
	defer reg.Close()
	folder := filepath.Clean(root)

	if err := observeRefs(reg, rs.ID, folder, root, p.AgentID); err != nil {
		warn(err)
	}
	// Backfill: the commits this folder made since the session began, from the reflog.
	if since, ok := sessionStartTime(p); ok {
		derived, err := gitrepo.ReflogTips(root, since)
		if err != nil {
			warn(err)
		}
		var atStart []string
		if v, had, err := reg.Meta(sessionstate.MetaRefsAtStart); err == nil && had {
			var m map[string]string
			if json.Unmarshal([]byte(v), &m) == nil {
				for _, sha := range m {
					atStart = append(atStart, sha)
				}
			}
		}
		for _, t := range derived {
			if held, err := gitrepo.InHistoryOf(root, t.Sha, atStart); err == nil && held {
				continue // already on a branch when the session began
			}
			tip := t.Sha
			if strings.HasPrefix(t.Ref, "refs/") {
				if cur, err := gitrepo.RefTip(root, t.Ref); err == nil && cur != "" {
					tip = cur
				}
			}
			if err := reg.RecordRef(sessionstate.Ref{SessionID: rs.ID, Folder: folder, Name: t.Ref, Tip: tip, AgentID: p.AgentID}); err != nil {
				warn(err)
			}
		}
	}

	// Judge: every recorded ref of this agent.
	rows, err := reg.Refs(rs.ID, folder)
	if err != nil {
		warn(err)
		return nil
	}
	head, _ := gitrepo.Head(root)
	var cands []stopTip
	for _, r := range rows {
		if r.AgentID != p.AgentID {
			continue
		}
		tip := r.Tip
		if strings.HasPrefix(r.Name, "refs/") {
			cur, err := gitrepo.RefTip(root, r.Name)
			if err != nil {
				warn(err)
				continue
			}
			if cur != "" && cur != r.Tip {
				tip = cur
				_ = reg.RecordRef(sessionstate.Ref{SessionID: rs.ID, Folder: folder, Name: r.Name, Tip: cur, AgentID: r.AgentID})
			} else if cur == "" {
				// The ref is gone: its commits are judged only while something still holds them,
				// or while they are owed a judgement (a branch squash-merged and deleted before
				// any rule passed it is nothing holds, and is still the session's unjudged work).
				if ok, err := gitrepo.Reachable(root, tip); err != nil || !ok {
					if held, err := gitrepo.RefTip(root, tip); err != nil || held == "" || judgedByEvery(root, tip, guards, results) {
						continue
					}
				}
			}
		}
		if r.Abandoned != "" {
			// The user had this ref dropped, at that tip. Still that tip, and not pushed or
			// merged since: not judged. Anything else un-abandons it.
			if tip == r.Abandoned {
				if onRemote, err := gitrepo.OnRemote(root, tip); err == nil && !onRemote {
					continue
				}
			}
			_ = reg.SetRefAbandoned(rs.ID, folder, r.Name, "")
		}
		if ok, err := gitrepo.IsAncestor(root, tip, "HEAD"); err != nil {
			warn(err)
			continue
		} else if ok || tip == head.Commit {
			continue // HEAD's own judgment covers it
		}
		landed := gitrepo.LandedUpstream(root, tip)
		if landed && judgedByEvery(root, tip, guards, results) {
			continue // squash-merged AFTER a rule passed it: everything it changed is upstream, and was judged
		}
		start, _ := gitrepo.RefCreation(root, r.Name)
		cands = append(cands, stopTip{Sha: tip, Ref: r.Name, Start: start, Landed: landed})
	}
	shas := make([]string, len(cands))
	for i, c := range cands {
		shas[i] = c.Sha
	}
	maximal, err := gitrepo.Maximal(root, shas)
	if err != nil {
		warn(err)
		return nil
	}
	var out []stopTip
	for _, sha := range maximal {
		for _, c := range cands {
			if c.Sha == sha {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

// judgedByEvery reports whether every file-guard has a FINISHED PASSING run at tip or at a
// descendant of it: the work up to tip was approved by each rule. Landing upstream
// never substitutes for it: a recorded tip nobody passed stays owed even after its
// branch was squash-merged. Anything not known (no store, a failed read) is false, so
// the tip is judged.
func judgedByEvery(root, tip string, guards []declaration.FileGuard, results checkstore.Store) bool {
	if results == nil {
		return false
	}
	for _, g := range guards {
		heads, err := results.PassedHeads(g.Qualified())
		if err != nil {
			return false
		}
		covered := false
		for _, h := range heads {
			if ok, err := gitrepo.IsAncestor(root, tip, h); err == nil && ok {
				covered = true
				break
			}
		}
		if !covered {
			return false
		}
	}
	return true
}

// sessionStartTime is when the session's record began, which bounds what the reflog is
// read for.
func sessionStartTime(p HookPayload) (t time.Time, ok bool) {
	record, err := p.sessionRecord()
	if err != nil || record == "" {
		return t, false
	}
	t, err = transcript.StartTime(record)
	return t, err == nil && !t.IsZero()
}

var objectSha = regexp.MustCompile(`^[0-9a-f]{40}$`)

func newSessionRefsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "refs",
		Short: "The refs a session committed on, which a file-guard judges at Stop",
		Long: `The refs a session committed on, per folder (the session_refs table of the session's
store). At Stop every file-guard judges each recorded tip, not only HEAD, so a branch the
agent left is still judged and its pull request cannot merge unchecked.

The engine records them itself from the folder's reflog; ` + "`refs add`" + ` writes one by hand,
for a ref the engine has not seen or a session begun before it did.`,
	}
	cmd.AddCommand(newSessionRefsAddCmd(), newSessionRefsListCmd(), newSessionRefsAbandonCmd())
	return cmd
}

type refsTarget struct{ session, workspace string }

func (t *refsTarget) flags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&t.session, "session", "", "the session's stable id (`sr-session id`)")
	cmd.Flags().StringVar(&t.workspace, "workspace", "", "the directory the session began in (default: the current directory)")
	_ = cmd.MarkFlagRequired("session")
}

func (t *refsTarget) open() (sessionstate.Store, error) {
	ws := t.workspace
	if ws == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		ws = wd
	}
	path, err := sessionDBPath(ws, t.session)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("sloprail: no store for session %s under %s (%v); pass --workspace as the directory the session began in", t.session, ws, err)
	}
	return sessionstate.Open(path)
}

func newSessionRefsAddCmd() *cobra.Command {
	var t refsTarget
	var folder, ref, tip, agent string
	cmd := &cobra.Command{
		Use:   "add --session <id> --folder <git root> --ref <name> --tip <sha>",
		Short: "Record a ref the session committed on, so Stop judges its tip",
		Long: `Record a ref the session committed on. At the session's next Stop every file-guard
judges this tip as well as HEAD (the ref's CURRENT tip when it still exists, else --tip).

--ref is a branch name ('feat-a' or 'refs/heads/feat-a'), or 'detached/<sha12>' for commits left
on a detached HEAD. --folder is the repository's git root (default: --workspace). --agent
names the sub-agent that owns the row (default: none, the session itself).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !objectSha.MatchString(tip) {
				return fmt.Errorf("sloprail: --tip must be a full 40-character commit SHA, got %q", tip)
			}
			if ref == "" {
				return fmt.Errorf("sloprail: --ref is required")
			}
			if !strings.HasPrefix(ref, "refs/") && !strings.HasPrefix(ref, "detached/") {
				ref = "refs/heads/" + ref
			}
			if folder == "" {
				folder = t.workspace
			}
			if folder == "" {
				wd, err := os.Getwd()
				if err != nil {
					return err
				}
				folder = wd
			}
			if root, err := gitrepo.Root(folder); err == nil && root != "" {
				folder = root
			}
			store, err := t.open()
			if err != nil {
				return err
			}
			defer store.Close()
			if err := store.RecordRef(sessionstate.Ref{SessionID: t.session, Folder: filepath.Clean(folder), Name: ref, Tip: tip, AgentID: agent}); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "recorded %s at %s in %s\n", ref, tip, filepath.Clean(folder))
			return nil
		},
	}
	t.flags(cmd)
	cmd.Flags().StringVar(&folder, "folder", "", "the repository's git root (default: --workspace)")
	cmd.Flags().StringVar(&ref, "ref", "", "the branch name, or detached/<sha12>")
	cmd.Flags().StringVar(&tip, "tip", "", "the commit the ref points at (40 hex characters)")
	cmd.Flags().StringVar(&agent, "agent", "", "the sub-agent that owns the row (default: the session itself)")
	_ = cmd.MarkFlagRequired("ref")
	_ = cmd.MarkFlagRequired("tip")
	return cmd
}

func newSessionRefsListCmd() *cobra.Command {
	var t refsTarget
	cmd := &cobra.Command{
		Use:   "list --session <id>",
		Short: "The refs recorded for a session, one per line: folder, ref, tip, agent",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := t.open()
			if err != nil {
				return err
			}
			defer store.Close()
			rows, err := store.Refs(t.session, "")
			if err != nil {
				return err
			}
			for _, r := range rows {
				fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\t%s\n", r.Folder, r.Name, r.Tip, r.AgentID)
			}
			return nil
		},
	}
	t.flags(cmd)
	return cmd
}
