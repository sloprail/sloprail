package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/checkrun"
	"github.com/sloprail/sloprail/internal/checkstore"
	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/module"
	"github.com/sloprail/sloprail/internal/module/modules"
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
// free — CI is the backstop — but the Stop lists it, and it holds only while the branch's tip
// stays where it was dropped: new commits track the range again.
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
	base, ok := gitrepo.DefaultBase(folder, sha)
	if (!ok || base == sha) && startedAt != "" {
		if startedAt == sessionstate.FolderBaseUnborn {
			return gitrepo.EmptyTree
		}
		return startedAt
	}
	if !ok {
		return gitrepo.EmptyTree // no remote default branch and nothing recorded: the widest range, never an empty one
	}
	return base
}

// folderHasFileGuards reports whether at least one file-guard loads for the folder: its own
// .sloprail, or a plugin's shipped one that applies there. A folder with none is still a session
// folder (its gates apply), but there is no range to track in it.
func folderHasFileGuards(folder, trustedRev string) bool {
	reg, err := modules.Registry()
	if err != nil {
		return false
	}
	quiet := &cobra.Command{}
	quiet.SetOut(io.Discard)
	quiet.SetErr(io.Discard)
	if trustedRev == sessionstate.FolderBaseUnborn {
		trustedRev = gitrepo.EmptyTree
	}
	// The commit the folder was registered at vouches for the project's own switch-offs of
	// protected rules, as the session start does at a hook.
	var loaded declaration.Loaded
	if trustedRev != "" {
		loaded = newNatureDeclarations(quiet, folder, reg, trustedRev)
	} else {
		loaded = newNatureDeclarations(quiet, folder, reg)
	}
	return len(loaded.FileGuards) > 0
}

// trackMissing tracks, at a hook, the branches of this agent's folders the SESSION committed
// on: a branch whose tip is a commit this clone made since the folder was registered (its
// reflog says so) or a commit the session recorded as a tip earlier, whether the agent still
// stands on it or left it within the same call. A branch it merely checked out (a colleague's,
// a fetched one) carries no such commit and is not the session's to answer for. Tracking needs a
// file-guard to load in the folder, and a .sloprail that appears mid-session starts being
// tracked at the next hook. A branch already tracked just has its tip refreshed (a branch
// deleted later is verified at the commit it last pointed at).
func trackMissing(reg sessionstate.Store, rs rootSession, p HookPayload) {
	folders, err := reg.Folders(rs.ID)
	if err != nil {
		return
	}
	ranges, err := reg.Ranges(rs.ID)
	if err != nil {
		return
	}
	hasFolder := map[string]bool{}
	hasHead := map[string]bool{}
	lastTip := map[string]string{}
	tipsIn := map[string]map[string]bool{} // folder -> the tips the session recorded there
	for _, r := range ranges {
		if r.HeadSHA != "" {
			if tipsIn[r.Folder] == nil {
				tipsIn[r.Folder] = map[string]bool{}
			}
			tipsIn[r.Folder][r.HeadSHA] = true
		}
		hasFolder[r.Folder] = true
		hasHead[r.Folder+"\x00"+r.Head] = true
		lastTip[r.Folder+"\x00"+r.Head] = r.HeadSHA
	}
	for _, f := range folders {
		if f.AgentID != p.AgentID {
			continue
		}
		if st, err := os.Stat(f.Path); err != nil || !st.IsDir() {
			continue
		}
		folder := filepath.Clean(f.Path)
		head, sha, ok := trackedHead(folder)
		if !ok {
			continue
		}
		since := int64(0)
		if f.BaseRef != "" && f.BaseRef != sessionstate.FolderBaseUnborn {
			since = gitrepo.CommitTime(folder, f.BaseRef)
		}
		// Before this hook refreshes a tip: every local branch standing at a commit the session made.
		trackSessionBranches(reg, rs.ID, folder, f, hasHead, tipsIn[folder], since)
		switch {
		case hasHead[folder+"\x00"+head]:
			if broughtIn(folder, lastTip[folder+"\x00"+head], sha) {
				continue // a fast-forward onto the remote's work: upstream's commits are not the tip the session made
			}
			_ = reg.TrackRange(sessionstate.TrackedRange{
				SessionID: rs.ID, Folder: folder, Head: head, HeadSHA: sha, AddedBy: sessionstate.RangeAuto, AgentID: f.AgentID,
			})
		case !hasFolder[folder]:
			ensureTracked(reg, rs.ID, f.Path, f.AgentID, f.BaseRef)
		case head == sha && sha != f.BaseRef && (tipsIn[folder][sha] || gitrepo.CommittedHere(folder, "HEAD", sha, since)):
			// Commits left on a detached HEAD.
			trackCurrent(reg, rs.ID, f.Path, f.AgentID, f.BaseRef, true)
		}
	}
}

// trackSessionBranches tracks every local branch of folder that stands at a tip the session
// made: a commit this clone's reflog shows being made there since the folder was registered
// (since), or one the session recorded as a tip earlier (a branch it made, reset away, and
// recreated at the old SHA: nothing but the session remembers the commit). Its commits are the
// session's and stay owed until verified. Nothing is tracked where no file-guard loads.
func trackSessionBranches(reg sessionstate.Store, sessionID, folder string, f sessionstate.Folder, hasHead map[string]bool, tips map[string]bool, since int64) {
	out, err := exec.Command("git", "-C", folder, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads").Output()
	if err != nil {
		return
	}
	guards := 0 // 0 unknown, 1 loads, -1 none
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		ref, sha, found := strings.Cut(line, " ")
		name := strings.TrimPrefix(ref, "refs/heads/")
		if !found || hasHead[folder+"\x00"+name] || sha == f.BaseRef {
			continue
		}
		if !tips[sha] && !gitrepo.CommittedHere(folder, ref, sha, since) {
			continue
		}
		if guards == 0 {
			guards = -1
			if folderHasFileGuards(folder, f.BaseRef) {
				guards = 1
			}
		}
		if guards < 0 {
			return
		}
		hasHead[folder+"\x00"+name] = true
		_ = reg.TrackRange(sessionstate.TrackedRange{
			SessionID: sessionID, Folder: folder, Head: name, HeadSHA: sha,
			Base: autoBase(folder, sha, f.BaseRef), AddedBy: sessionstate.RangeAuto, AgentID: f.AgentID,
		})
	}
}

// trackAtHook is the tracking every hook runs for its agent's folders (the root's store holds
// the registry): the current branch, and the branches the session committed on.
func trackAtHook(p HookPayload) {
	rs, err := resolveRootSession(p)
	if err != nil {
		return
	}
	if _, err := os.Stat(rs.Path); err != nil {
		return
	}
	root, err := sessionstate.Open(rs.Path)
	if err != nil {
		return
	}
	defer root.Close()
	trackFolders(root, rs, p)
	trackMissing(root, rs, p)
}

// ensureTracked tracks a folder's current branch, automatically, unless that range is already
// there (what the agent changed or dropped stays so). startedAt is the folder's registered
// BaseRef.
func ensureTracked(reg sessionstate.Store, sessionID, folder, agent, startedAt string) {
	trackCurrent(reg, sessionID, folder, agent, startedAt, true)
}

// trackCurrent is ensureTracked; needGuards: only when the folder's own checkout loads a file-guard.
func trackCurrent(reg sessionstate.Store, sessionID, folder, agent, startedAt string, needGuards bool) {
	if needGuards && !folderHasFileGuards(folder, startedAt) {
		return // nothing to answer for here: no range is tracked, and Stop says nothing of it
	}
	head, sha, ok := trackedHead(folder)
	if !ok {
		return
	}
	if rows, err := reg.Ranges(sessionID); err == nil {
		for _, row := range rows {
			if row.Folder == filepath.Clean(folder) && row.Head == head && broughtIn(folder, row.HeadSHA, sha) {
				return // fast-forwarded onto the remote's work: the recorded tip stays the session's own
			}
		}
	}
	// Falling back to where the folder was registered is for work on the default branch itself. A
	// branch cut at the default branch's tip starts there: what the default branch gained since
	// the session began is upstream's, not the session's. A branch the session committed on is
	// not "cut at the tip": its commits landed there (a fast-forward push), and the base never
	// moves past them.
	if db, ok := gitrepo.DefaultBase(folder, sha); ok && db == sha && !gitrepo.IsDefaultBranch(folder, head) && len(head) < 40 &&
		!sessionCommittedOn(folder, head, sha, startedAt) {
		startedAt = ""
	}
	_ = reg.TrackRange(sessionstate.TrackedRange{
		SessionID: sessionID, Folder: filepath.Clean(folder), Head: head, HeadSHA: sha,
		Base: autoBase(folder, sha, startedAt), AddedBy: sessionstate.RangeAuto, AgentID: agent,
	})
}

// sessionCommittedOn reports whether this clone made sha on branch since the folder was registered.
func sessionCommittedOn(folder, branch, sha, startedAt string) bool {
	since := int64(0)
	if startedAt != "" && startedAt != sessionstate.FolderBaseUnborn {
		since = gitrepo.CommitTime(folder, startedAt)
	}
	return gitrepo.CommittedHere(folder, "refs/heads/"+branch, sha, since)
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

// untrackGone handles the ranges of folders that no longer exist (a worktree removed).
func untrackGone(reg sessionstate.Store, sessionID string, ranges []sessionstate.TrackedRange) {
	for _, r := range ranges {
		if !r.Tracked() {
			continue
		}
		if st, err := os.Stat(r.Folder); err != nil || !st.IsDir() {
			dropRemoved(reg, sessionID, r)
		}
	}
}

// dropRemoved settles a tracked range whose folder is gone. A branch that still exists in the
// session's own repository keeps being answered for: the range moves to the root's folder (its
// commits are the session's, and the Stop verifies them there). A branch that is gone is NOT
// dropped: its commits are unverified, so the range moves to the root pinned at the last tip
// (a ref under refs/sloprail/pins keeps the commits from garbage collection) and the Stop
// verifies it there; only verification passing or `sr-session refs untrack` with a reason
// releases it. With no root folder to move to, the range stays as it is and the Stop refuses it.
func dropRemoved(reg sessionstate.Store, sessionID string, r sessionstate.TrackedRange) {
	home, ok := homeFolder(reg, sessionID, r.Folder)
	if !ok {
		return
	}
	moved := r
	moved.Folder = home.Path
	if rev, note := headRevision(moved); note == "" && rev != "" {
		if err := reg.TrackRange(sessionstate.TrackedRange{
			SessionID: sessionID, Folder: filepath.Clean(home.Path), Head: r.Head, HeadSHA: r.HeadSHA,
			Base: r.Base, AddedBy: sessionstate.RangeAuto, AgentID: home.AgentID,
		}); err == nil {
			_ = reg.UntrackRange(sessionID, r.Folder, r.Head, "worktree removed; the range moved to "+home.Path, r.AgentID, "")
		}
		return
	}
	if r.HeadSHA == "" {
		return
	}
	_ = gitrepo.PinRef(home.Path, "refs/sloprail/pins/"+r.HeadSHA, r.HeadSHA)
	if err := reg.TrackRange(sessionstate.TrackedRange{
		SessionID: sessionID, Folder: filepath.Clean(home.Path), Head: r.HeadSHA, HeadSHA: r.HeadSHA,
		Base: r.Base, AddedBy: sessionstate.RangeAuto, AgentID: home.AgentID,
	}); err == nil {
		_ = reg.UntrackRange(sessionID, r.Folder, r.Head, fmt.Sprintf("worktree removed and branch %s is gone; the range moved to %s, pinned at %s", r.Head, home.Path, shortRev(r.HeadSHA)), r.AgentID, "")
	}
}

// homeFolder is the session's root folder, when it is the same repository as folder.
func homeFolder(reg sessionstate.Store, sessionID, folder string) (sessionstate.Folder, bool) {
	folders, err := reg.Folders(sessionID)
	if err != nil {
		return sessionstate.Folder{}, false
	}
	var repo string
	for _, f := range folders {
		if sameDir(f.Path, folder) {
			repo = f.RepoID
		}
	}
	if repo == "" {
		return sessionstate.Folder{}, false
	}
	for _, f := range folders {
		if f.Role == sessionstate.FolderRoot && f.RepoID == repo && !sameDir(f.Path, folder) {
			if st, err := os.Stat(f.Path); err == nil && st.IsDir() {
				return f, true
			}
		}
	}
	return sessionstate.Folder{}, false
}

// verifyTrackedRanges is the Stop's file-guard work: each tracked range of this agent's folders
// is verified against the stored results (no model is asked, nothing is written), and the
// refusals are returned, with a note on what the agent untracked.
func verifyTrackedRanges(cmd *cobra.Command, p HookPayload, reg *module.Registry, store sessionstate.Store) []string {
	if p.IsSubagent() && !declaration.EnableSubagentStopCheck(dotDir(p.Cwd)) {
		// A sub-agent does not see the whole picture: its folders' ranges are tracked (here too,
		// at its own Stop), and the ROOT's Stop verifies them (`enable_subagent_stop_check: true`
		// makes its own Stop do so too).
		trackAtHook(p)
		return nil
	}
	rs, err := resolveRootSession(p)
	if err != nil {
		return identityRefusal(cmd, p, reg, store, err)
	}
	if _, err := os.Stat(rs.Path); err != nil {
		if os.IsNotExist(err) {
			return nil // a session that never recorded anything has no ranges
		}
		return []string{fmt.Sprintf("the session's tracked ranges could not be read (%v); refusing because a registry that could not be read must not be read as 'nothing to judge'", err)}
	}
	root, err := sessionstate.Open(rs.Path)
	if err != nil {
		return []string{fmt.Sprintf("the session's tracked ranges could not be read (%v); refusing because a registry that could not be read must not be read as 'nothing to judge'", err)}
	}
	defer root.Close()
	trackFolders(root, rs, p)
	trackMissing(root, rs, p)
	ranges, err := root.Ranges(rs.ID)
	if err != nil {
		return []string{fmt.Sprintf("the session's tracked ranges could not be read (%v); refusing because a registry that could not be read must not be read as 'nothing to judge'", err)}
	}
	untrackGone(root, rs.ID, ranges)
	if ranges, err = root.Ranges(rs.ID); err != nil {
		return []string{fmt.Sprintf("the session's tracked ranges could not be read (%v); refusing because a registry that could not be read must not be read as 'nothing to judge'", err)}
	}

	quiet := &cobra.Command{}
	quiet.SetOut(io.Discard)
	quiet.SetErr(io.Discard)
	var out, notes []string
	for _, r := range ranges {
		if p.AgentID != "" && r.AgentID != p.AgentID {
			continue // a sub-agent verifies its own ranges; the root's Stop covers all of them
		}
		if !r.Tracked() {
			notes = append(notes, fmt.Sprintf("untracked: %s %s (reason: %s)", r.Folder, r.Head, r.UntrackedReason))
			continue
		}
		if coveredByBranch(r, ranges) {
			continue // commits left on a detached HEAD, since given a branch: that branch's range holds them
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

// identityRefusal is the Stop's answer when the session it belongs to cannot be named: the
// registry of tracked ranges cannot be found, so what the agent committed cannot be known to
// be judged. That is a refusal, never a pass. The folder this hook runs in is still verified,
// from the start of its history (the start is unknown), so what can be found is not hidden
// behind the missing identity either.
func identityRefusal(cmd *cobra.Command, p HookPayload, reg *module.Registry, store sessionstate.Store, cause error) []string {
	out := []string{fmt.Sprintf("the session this Stop belongs to cannot be identified (%v), so its tracked ranges are unknown and cannot be verified; refusing because a session that cannot be named must not be read as 'nothing to judge'. To recover: make sure the hook payload carries the session's transcript_path (for a sub-agent, agent_transcript_path and the parent's record) and that the record exists, then stop again.", cause)}
	if folder, err := gitrepo.Root(p.Cwd); err == nil && folder != "" {
		if head, sha, ok := trackedHead(folder); ok {
			base, ok := gitrepo.DefaultBase(folder, sha)
			if !ok || base == sha {
				base = gitrepo.EmptyTree
			}
			quiet := &cobra.Command{}
			quiet.SetOut(io.Discard)
			quiet.SetErr(io.Discard)
			if reason := verifyRange(cmd, p, reg, store, quiet, sessionstate.TrackedRange{Folder: folder, Head: head, HeadSHA: sha, Base: base}); reason != "" {
				out = append(out, reason)
			}
		}
	}
	return out
}

// verifyRange verifies one tracked range, returning the refusal or "". It only reads what `sr-checks run`
// stored: no check is executed, so it needs no record or session id.
func verifyRange(cmd *cobra.Command, p HookPayload, reg *module.Registry, store sessionstate.Store, quiet *cobra.Command, r sessionstate.TrackedRange) string {
	head, goneNote := headRevision(r)
	if r.Base == "" {
		// A row an older engine recorded: start from where the work on it began.
		sha := r.HeadSHA
		if h, err := gitrepo.ResolveRange(r.Folder, "HEAD", head); err == nil {
			sha = h.Head
		}
		r.Base = autoBase(r.Folder, sha, "")
	}
	headName := r.Head
	if len(headName) >= 40 && !strings.HasPrefix(headName, "refs/") {
		headName = "detached at " + shortRev(headName) // commits on no branch: say so
	}
	where := fmt.Sprintf("In %s (%s, from %s)", r.Folder, headName, shortRev(r.Base))
	if other := gitrepo.BranchWorktree(r.Folder, r.Head); other != "" {
		where = fmt.Sprintf("In %s (%s, checked out in another worktree, %s; from %s)", r.Folder, headName, other, shortRev(r.Base))
	}
	rng, err := gitrepo.ResolveRange(r.Folder, r.Base, head)
	if err != nil {
		return fmt.Sprintf("%s: the range cannot be read (%v). Re-track it (`sr-session refs track`) or untrack it with a reason (`sr-session refs untrack`).%s", where, err, goneNote)
	}
	// What the default branch gained since the range was tracked (a pull, a rebase onto a newer
	// origin/main) is upstream's, not the session's: the range starts at the head's merge base
	// with the default branch now, when that is later than the stored base.
	rng = advanceBase(r.Folder, rng, r.HeadSHA)
	// The range's base vouches for the project's own switch-offs of protected rules, as it does
	// under `sr-checks run`: verify must load the same rules the run judged.
	loaded := newNatureDeclarations(quiet, r.Folder, reg, rng.Base)
	if len(loaded.FileGuards) == 0 {
		return ""
	}
	cache, err := checkrun.OpenLocalCache(r.Folder)
	if err != nil {
		return fmt.Sprintf("%s: the check results could not be opened (%v); refusing because results that could not be read must not be read as 'nothing was judged'.", where, err)
	}
	results := checkstore.Open(cache, true)
	defer results.Close()
	refusals, _ := checkrun.Evaluate(checkrun.Params{
		Err: io.Discard, Guards: loaded.FileGuards, Root: r.Folder, Range: rng, Cwd: r.Folder,
		Workspace: r.Folder, AgentID: p.AgentID, Subagent: p.IsSubagent(),
		Store: results, Verify: true, Recorded: recordedCitations(p, store),
	})
	if len(refusals) == 0 {
		return ""
	}
	var parts []string
	for _, f := range refusals {
		parts = append(parts, f.Reason+" (file-guard "+f.Attribution+")")
	}
	out := where + ": " + joinRefusals(parts) + goneNote
	if len(r.Head) < 40 && !strings.HasPrefix(r.Head, "refs/") {
		out += fmt.Sprintf("\nIf %s is not yours to answer for (the user said to drop it), stop answering for it: `sr-session refs untrack --head %s --reason '<why>'`, and `sr-session refs track --head %s` takes it back.", r.Head, r.Head, r.Head)
	}
	return out
}

// broughtIn reports whether moving a branch from the tip the session last saw to sha only
// fast-forwarded it onto commits the remote default branch already holds (a pull).
func broughtIn(folder, old, sha string) bool {
	if old == "" || old == sha {
		return false
	}
	if mb, ok := gitrepo.RemoteDefaultBase(folder, sha); !ok || mb != sha {
		return false
	}
	ff, err := gitrepo.IsAncestor(folder, old, sha)
	return err == nil && ff
}

// advanceBase moves a range's base up to the head's merge base with the remote default branch,
// when that is a descendant of the base it has (never earlier). Work already on that branch
// is upstream's: a pull, a fast-forward or a rebase onto a newer origin/main leaves only the
// commits ahead of it in the range. A repository with no remote default branch keeps its base.
func advanceBase(folder string, rng gitrepo.Range, ownTip string) gitrepo.Range {
	db, ok := gitrepo.RemoteDefaultBase(folder, rng.Head)
	if !ok || db == rng.Base || db == ownTip {
		return rng // the tip the session made is itself what landed: judged until it passed
	}
	if rng.Base != gitrepo.EmptyTree {
		if isAnc, err := gitrepo.IsAncestor(folder, rng.Base, db); err != nil || !isAnc {
			return rng
		}
	}
	rng.Base = db
	return rng
}

// headRevision is the revision a tracked range's head names now: its branch, else the commit it
// was tracked at (with a note that the branch is gone).
func headRevision(r sessionstate.TrackedRange) (rev, note string) {
	head := r.Head
	switch {
	case strings.HasPrefix(head, "refs/"), len(head) >= 40:
		// An older engine's full ref name, or a commit sha.
	case strings.HasPrefix(head, "detached/"):
		head = r.HeadSHA
	default:
		head = "refs/heads/" + head
	}
	if head == "" {
		head = r.HeadSHA
	}
	if _, err := gitrepo.ResolveRange(r.Folder, "HEAD", head); err == nil {
		return head, ""
	}
	if r.HeadSHA != "" {
		return r.HeadSHA, fmt.Sprintf(" The branch %q is gone: verified at the commit it last pointed at (%s). Re-track the range under another head (`sr-session refs track`) or untrack it with a reason (`sr-session refs untrack`).", r.Head, shortRev(r.HeadSHA))
	}
	return head, ""
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

Untracking is allowed freely — CI is the backstop — but the Stop lists it with your reason, and
the range is tracked again by itself when the branch tip moves. A removed worktree's range moves
to the root folder (pinned at its last tip if the branch is gone too). A sub-agent's ranges are
verified at the root's Stop unless enable_subagent_stop_check is set.`,
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
			tip := ""
			if out, err := gitrepo.ResolveRange(dir, "HEAD", headRef(head)); err == nil {
				tip = out.Head
			}
			if err := s.reg.UntrackRange(s.rs.ID, dir, head, reason, s.ownerOf(dir), tip); err != nil {
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

// headRef is the revision a range's head names: a branch by its name, else as given.
func headRef(head string) string {
	if strings.HasPrefix(head, "refs/") || len(head) >= 40 {
		return head
	}
	return "refs/heads/" + head
}

// recordedCitations is the citations this session (and the sessions sharing its tree) recorded
// per file with `sr-file --cite`, oldest first: what a refusal hands back as the trailer to paste.
func recordedCitations(p HookPayload, store sessionstate.Store) map[string][]transcript.Citation {
	out := map[string][]transcript.Citation{}
	record, _ := p.record()
	others, _ := otherHistories(p, record)
	for _, hist := range []map[string][]historyPoint{historyIn(store, true), others} {
		for path, pts := range hist {
			for _, pt := range pts {
				out[path] = append(out[path], pt.Cites...)
			}
		}
	}
	return out
}

// coveredByBranch reports whether a range tracked at a bare commit (a detached HEAD) is held by
// another tracked range of the same folder that is on a branch: the commit is in that range
// (reachable from the branch's live tip, not from its base), so verifying the branch judges it
// as well. A commit older than the branch's base, or one the branch cannot be shown to hold, is
// not covered: it is verified on its own.
func coveredByBranch(r sessionstate.TrackedRange, all []sessionstate.TrackedRange) bool {
	commit := r.Head
	switch {
	case len(r.Head) >= 40 && !strings.HasPrefix(r.Head, "refs/"):
	case r.HeadSHA != "":
		// A branch that is gone (renamed, deleted): the commit it last pointed at.
		if _, note := headRevision(r); note == "" {
			return false
		}
		commit = r.HeadSHA
	default:
		return false
	}
	for _, o := range all {
		if !o.Tracked() || o.Folder != r.Folder || o.AgentID != r.AgentID || len(o.Head) >= 40 || o.HeadSHA == "" || o.Base == "" || o.Head == r.Head {
			continue
		}
		tip, note := headRevision(o) // the live tip
		if note != "" {
			continue // a branch that is gone holds nothing now
		}
		if in, err := gitrepo.IsAncestor(r.Folder, commit, tip); err != nil || !in {
			continue
		}
		if o.Base != gitrepo.EmptyTree {
			if before, err := gitrepo.IsAncestor(r.Folder, commit, o.Base); err != nil || before {
				continue
			}
		}
		return true
	}
	return false
}

// hasTrackedRanges reports whether the session has a tracked range of any agent: what the root's
// Stop must verify even when its own tree declares no rule (a sub-agent's worktree or another
// repository may). A registry that cannot be read is not "no ranges": verifyTrackedRanges
// refuses on that, so the caller must not exit early.
func hasTrackedRanges(p HookPayload) bool {
	rs, err := resolveRootSession(p)
	if err != nil {
		return false // nothing names a registry; identityRefusal speaks for the rules that load
	}
	if _, err := os.Stat(rs.Path); err != nil {
		return !os.IsNotExist(err)
	}
	root, err := sessionstate.Open(rs.Path)
	if err != nil {
		return true
	}
	defer root.Close()
	ranges, err := root.Ranges(rs.ID)
	if err != nil {
		return true
	}
	for _, r := range ranges {
		if r.Tracked() {
			return true
		}
	}
	return false
}
