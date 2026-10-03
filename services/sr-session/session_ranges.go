package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/changeset"
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

// autoBase is where a folder's current work starts: ALWAYS the merge base with the remote
// default branch, whatever the session made, pulled or pushed (a head the default branch already
// holds is an empty range). Only a repository with no remote default branch falls back to the HEAD
// the folder was registered at, and without one to the empty tree: the widest range, never an
// empty one.
//
// Why so plain: CI is the hermetic guarantee. It verifies the pull request's range
// merge-base(target, head)..head and a push event's before..after, so a session that pushes
// straight to the default branch is caught by CI on that push. The local Stop is early feedback
// only, and it never has to tell the session's commits from upstream's to do that.
func autoBase(folder, sha, startedAt string) string {
	if base, ok := gitrepo.DefaultBase(folder, sha); ok {
		return base
	}
	if startedAt == sessionstate.FolderBaseUnborn || startedAt == "" {
		return gitrepo.EmptyTree
	}
	return startedAt
}

// loadRegistry builds the module registry; a variable so a test can make it fail.
var loadRegistry = modules.Registry

// folderHasFileGuards reports whether at least one file-guard loads for the folder: its own
// .sloprail, or a plugin's shipped one that applies there. A folder with none is still a session
// folder (its gates apply), but there is no range to track in it.
//
// A registry that cannot be built is an error, never "no file-guards": tracking that silently
// skips would let the session's commits escape the Stop.
func folderHasFileGuards(folder, trustedRev string) (bool, error) {
	reg, err := loadRegistry()
	if err != nil {
		return false, fmt.Errorf("load the guardrail modules to track %s: %w", folder, err)
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
	if len(loaded.FileGuards) > 0 {
		return true, nil
	}
	return fileGuardsOnDefaultBranch(folder), nil
}

// fileGuardsOnDefaultBranch reports whether the project's default branch (the remote's, else a
// local main or master) carries file-guards the checked-out branch does not: a rule added on
// main mid-session reaches an older branch's range from its add commit, so the session's work
// on that branch is tracked although the rule is absent from its checkout (over-tracking: the
// Stop verifies where the rules load).
func fileGuardsOnDefaultBranch(folder string) bool {
	var tips []string
	if tip, ok := gitrepo.RemoteDefaultTip(folder); ok {
		tips = append(tips, tip)
	}
	tips = append(tips, "refs/heads/main", "refs/heads/master")
	for _, tip := range tips {
		out, err := exec.Command("git", "-C", folder, "ls-tree", "--name-only", tip, ".sloprail/file-guard").Output()
		if err == nil && strings.TrimSpace(string(out)) != "" {
			return true
		}
	}
	return false
}

// Observation: what the session SAW, never what git remembers. At every hook each session
// folder's local branches (and a detached HEAD) are recorded with their tips in the session
// store; a branch whose tip differs from the one recorded before has MOVED, however it moved
// (commit, merge, rebase, am, cherry-pick, reset to new commits). No reflog is read anywhere.
const (
	observedTipPrefix    = "observed-tip:"    // observed-tip:<branch>:<folder> -> tip
	observedFolderPrefix = "observed-folder:" // observed-folder:<folder> -> "1" once its baseline is taken
	detachedObserved     = "(detached)"
)

// observedBranch is one local branch (or the detached HEAD) as this hook saw it.
type observedBranch struct {
	name, sha string
	moved     bool // its tip is not the one an earlier hook recorded (or the branch is new)
	foreign   bool // checked out in ANOTHER worktree of the repository: that worktree's line of work, never this folder's
}

// realPath is a path with symlinks resolved, so two spellings of one folder compare equal.
func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return filepath.Clean(r)
	}
	return filepath.Clean(p)
}

// checkedOutElsewhere maps each local branch checked out in a worktree of folder's
// repository other than folder itself to that worktree's path. A branch visible in the shared ref namespace is not the
// work of every folder that can see it: only the folder standing on it (or moving its tip) answers
// for it. An unreadable listing is empty: over-track, never under-track.
func checkedOutElsewhere(folder string) map[string]string {
	out, err := exec.Command("git", "-C", folder, "worktree", "list", "--porcelain").Output()
	if err != nil {
		return nil
	}
	self := realPath(folder)
	elsewhere := map[string]string{}
	var path string
	for _, ln := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(ln, "worktree "):
			path = realPath(strings.TrimPrefix(ln, "worktree "))
		case strings.HasPrefix(ln, "branch refs/heads/") && path != self:
			elsewhere[strings.TrimPrefix(ln, "branch refs/heads/")] = path
		}
	}
	return elsewhere
}

// observeFolder records the folder's branch tips and reports which moved since the last hook.
// The folder's first observation takes the baseline: every branch stands where it stands, except
// the registered branch, which started at the folder's BaseRef. Any git or store error is
// returned: a tip that could not be observed must not be read as "did not move".
func observeFolder(reg sessionstate.Store, folder string, f sessionstate.Folder, head, headSHA string, siblings ...string) ([]observedBranch, error) {
	out, err := exec.Command("git", "-C", folder, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads").Output()
	if err != nil {
		return nil, fmt.Errorf("list branches of %s: %w", folder, err)
	}
	var branches []observedBranch
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if ref, sha, found := strings.Cut(line, " "); found {
			branches = append(branches, observedBranch{name: strings.TrimPrefix(ref, "refs/heads/"), sha: sha})
		}
	}
	if head == headSHA { // detached
		branches = append(branches, observedBranch{name: detachedObserved, sha: headSHA})
	}
	_, baselined, err := reg.Meta(observedFolderPrefix + folder)
	if err != nil {
		return nil, fmt.Errorf("read observations of %s: %w", folder, err)
	}
	elsewhere := checkedOutElsewhere(folder)
	ownRows, err := reg.Ranges(f.SessionID)
	if err != nil {
		return nil, fmt.Errorf("read the session's ranges: %w", err)
	}
	ownTracked := map[string]bool{} // branches this folder already answers for: its session moved them, whoever stands on them now
	for _, r := range ownRows {
		if r.Tracked() && filepath.Clean(r.Folder) == filepath.Clean(folder) {
			ownTracked[r.Head] = true
		}
	}
	for i, b := range branches {
		if b.name != detachedObserved && b.name != head && elsewhere[b.name] != "" && !ownTracked[b.name] {
			branches[i].foreign = true
		}
		key := observedTipPrefix + b.name + ":" + folder
		prev, seen, err := reg.Meta(key)
		if err != nil {
			return nil, fmt.Errorf("read observations of %s: %w", folder, err)
		}
		switch {
		case branches[i].foreign:
			// recorded below, never moved by this folder
		case seen:
			branches[i].moved = prev != b.sha
		case baselined:
			branches[i].moved = true // a branch the session has not seen before
		case b.name == head && f.BaseRef != "" && f.BaseRef != sessionstate.FolderBaseUnborn:
			branches[i].moved = f.BaseRef != b.sha // the registered branch started at BaseRef
		case f.Role == sessionstate.FolderSubagentWorktree && b.name != head && b.name != detachedObserved:
			// A sub-agent's worktree met late shares the repository's branches. One that no other
			// folder of the session ever recorded, and that has commits the default lacks, is the
			// sub-agent's own (it made it after the session began).
			branches[i].moved = !recordedElsewhere(reg, b.name, siblings) && ownCommits(folder, b.sha, f.BaseRef)
		}
		if !seen || prev != b.sha {
			if err := reg.SetMeta(key, b.sha); err != nil {
				return nil, fmt.Errorf("record observations of %s: %w", folder, err)
			}
		}
	}
	if !baselined {
		if err := reg.SetMeta(observedFolderPrefix+folder, "1"); err != nil {
			return nil, fmt.Errorf("record observations of %s: %w", folder, err)
		}
	}
	return branches, nil
}

// recordedElsewhere reports whether another folder of the session already recorded the branch.
func recordedElsewhere(reg sessionstate.Store, branch string, folders []string) bool {
	for _, o := range folders {
		if _, ok, err := reg.Meta(observedTipPrefix + branch + ":" + o); err != nil || ok {
			return true // unreadable: do not over-track
		}
	}
	return false
}

// ownCommits reports whether sha has commits that neither the remote default branch nor the
// folder's start hold. Commit dates are never consulted: the agent controls them.
func ownCommits(folder, sha, startedAt string) bool {
	args := []string{"-C", folder, "rev-list", "-n", "1", sha}
	if tip, ok := gitrepo.RemoteDefaultTip(folder); ok {
		args = append(args, "^"+tip)
	}
	if startedAt != "" && startedAt != sessionstate.FolderBaseUnborn {
		args = append(args, "^"+startedAt)
	}
	out, err := exec.Command("git", args...).Output()
	return err != nil || strings.TrimSpace(string(out)) != "" // unreadable: over-track
}

// ahead reports whether sha carries commits the default branch does not (the folder's
// registered HEAD stands in for the default branch when work is on it).
func ahead(folder, sha, startedAt string) bool {
	return autoBase(folder, sha, startedAt) != sha
}

// trackMissing tracks, at a hook, the branches of this agent's folders the SESSION committed on,
// by observation: a branch whose observed tip moved during the session and has commits beyond
// the default branch, a branch standing at a commit the session recorded as a tip earlier, and
// the checked-out branch when it has commits the default branch does not. It errs toward
// over-tracking (the agent can untrack with a reason) and never toward under-tracking. Tracking
// needs a file-guard to load in the folder; a .sloprail that appears mid-session starts being
// tracked at the next hook. A branch already tracked has its tip refreshed (a branch deleted
// later is verified at the commit it last pointed at).
//
// An error (git or the store) is returned, and the Stop refuses on it:
// a branch that could not be observed is never "not tracked".
func trackMissing(reg sessionstate.Store, rs rootSession, p HookPayload) error {
	if err := pruneUnmovedAuto(reg, rs.ID); err != nil {
		return err
	}
	if err := pruneForeignAuto(reg, rs.ID); err != nil {
		return err
	}
	folders, err := reg.Folders(rs.ID)
	if err != nil {
		return err
	}
	ranges, err := reg.Ranges(rs.ID)
	if err != nil {
		return err
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
	var errs []error
	for _, f := range folders {
		if p.AgentID != "" && f.AgentID != p.AgentID { // the root observes every folder of the session; a sub-agent its own
			continue
		}
		if st, err := os.Stat(f.Path); err != nil || !st.IsDir() {
			continue
		}
		folder := filepath.Clean(f.Path)
		if _, err := gitrepo.Head(folder); err != nil {
			errs = append(errs, fmt.Errorf("read HEAD of %s: %w", folder, err))
			continue
		}
		head, sha, ok := trackedHead(folder)
		if !ok {
			continue // no commit yet: nothing to track
		}
		var siblings []string
		for _, o := range folders {
			if c := filepath.Clean(o.Path); c != folder {
				siblings = append(siblings, c)
			}
		}
		observed, err := observeFolder(reg, folder, f, head, sha, siblings...)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		// Before this hook refreshes a tip: every branch that moved or stands at a commit the session made.
		if err := trackSessionBranches(reg, rs.ID, folder, f, hasHead, lastTip, tipsIn[folder], observed); err != nil {
			errs = append(errs, err)
		}
		switch {
		case hasHead[folder+"\x00"+head]:
			if err := reg.TrackRange(sessionstate.TrackedRange{
				SessionID: rs.ID, Folder: folder, Head: head, HeadSHA: sha, AddedBy: sessionstate.RangeAuto, AgentID: f.AgentID,
			}); err != nil {
				errs = append(errs, fmt.Errorf("track %s in %s: %w", head, folder, err))
			}
		case !hasFolder[folder]:
			if err := ensureTracked(reg, rs.ID, f.Path, f.AgentID, f.BaseRef); err != nil {
				errs = append(errs, err)
			}
		case ahead(folder, sha, f.BaseRef):
			// The checked-out line of work carries commits the default branch does not, or commits
			// were left on a detached HEAD: the session stood on it, so it answers for it.
			if err := trackCurrent(reg, rs.ID, f.Path, f.AgentID, f.BaseRef, true); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// trackSessionBranches tracks every local branch of folder whose observed tip moved during the
// session and has commits beyond the default branch, or stands at a tip the session recorded
// earlier (a branch it made, reset away, and recreated at the old SHA: nothing but the session
// remembers the commit). Its commits stay owed until verified. Nothing is tracked where no
// file-guard loads. A tracked branch that moved has its tip refreshed.
func trackSessionBranches(reg sessionstate.Store, sessionID, folder string, f sessionstate.Folder, hasHead map[string]bool, lastTip map[string]string, tips map[string]bool, observed []observedBranch) error {
	guards := 0 // 0 unknown, 1 loads, -1 none
	for _, b := range observed {
		if b.name == detachedObserved || b.sha == f.BaseRef || b.foreign {
			continue
		}
		key := folder + "\x00" + b.name
		if hasHead[key] && !b.moved {
			continue
		}
		if !(b.moved && ahead(folder, b.sha, f.BaseRef)) && !tips[b.sha] {
			continue
		}
		if guards == 0 {
			guards = -1
			has, err := folderHasFileGuards(folder, f.BaseRef)
			if err != nil {
				return err
			}
			if has {
				guards = 1
			}
		}
		if guards < 0 {
			return nil
		}
		hasHead[key] = true
		if err := reg.TrackRange(sessionstate.TrackedRange{
			SessionID: sessionID, Folder: folder, Head: b.name, HeadSHA: b.sha,
			Base: autoBase(folder, b.sha, f.BaseRef), AddedBy: sessionstate.RangeAuto, AgentID: f.AgentID,
		}); err != nil {
			return fmt.Errorf("track %s in %s: %w", b.name, folder, err)
		}
	}
	return nil
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
	_ = trackMissing(root, rs, p) // observe first: what tracking the current branch needs to know it sees
	_ = trackFolders(root, rs, p) // hooks are best-effort; the Stop refuses on the errors
}

// ensureTracked tracks a folder's current branch, automatically, unless that range is already
// there (what the agent changed or dropped stays so). startedAt is the folder's registered
// BaseRef.
func ensureTracked(reg sessionstate.Store, sessionID, folder, agent, startedAt string) error {
	return trackCurrent(reg, sessionID, folder, agent, startedAt, true)
}

// trackCurrent is ensureTracked; needGuards: only when the folder's own checkout loads a file-guard.
func trackCurrent(reg sessionstate.Store, sessionID, folder, agent, startedAt string, needGuards bool) error {
	if needGuards {
		has, err := folderHasFileGuards(folder, startedAt)
		if err != nil {
			return err
		}
		if !has {
			return nil // nothing to answer for here: no range is tracked, and Stop says nothing of it
		}
	}
	head, sha, ok := trackedHead(folder)
	if !ok {
		return nil
	}
	rows, err := reg.Ranges(sessionID)
	if err != nil {
		return fmt.Errorf("read the session's ranges: %w", err)
	}
	if head == sha && checkedOutOnly(folder, sha, rows) {
		return nil // a commit only checked out (another pull request's) is never the session's work
	}
	return reg.TrackRange(sessionstate.TrackedRange{
		SessionID: sessionID, Folder: filepath.Clean(folder), Head: head, HeadSHA: sha,
		Base: autoBase(folder, sha, startedAt), AddedBy: sessionstate.RangeAuto, AgentID: agent,
	})
}

// checkedOutOnly reports whether a detached HEAD at sha stands on commits a local branch, a tag
// or a remote-tracking ref holds NOW (another pull request's, or work already pushed, which CI
// verifies), and not a tip the session recorded: only checked out, never the session's. A commit
// on no such ref is the session's. An unreadable answer is read as "made": over-track.
func checkedOutOnly(folder, sha string, rows []sessionstate.TrackedRange) bool {
	for _, r := range rows {
		if r.HeadSHA == sha {
			return false
		}
	}
	out, err := exec.Command("git", "-C", folder, "for-each-ref", "--count=1", "--contains", sha, "--format=%(refname)", "refs/heads", "refs/tags", "refs/remotes").Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}

// trackFolders makes sure the current branch of this agent's folders is tracked: the tree it
// stands in and the folders registered for it.
func trackFolders(reg sessionstate.Store, rs rootSession, p HookPayload) error {
	folders, err := reg.Folders(rs.ID)
	if err != nil {
		return err
	}
	var errs []error
	for _, f := range folders {
		if p.AgentID != "" && f.AgentID != p.AgentID { // the root observes every folder of the session; a sub-agent its own
			continue
		}
		if st, err := os.Stat(f.Path); err != nil || !st.IsDir() {
			continue
		}
		if err := ensureTracked(reg, rs.ID, f.Path, f.AgentID, f.BaseRef); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
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
			Base: r.Base, AddedBy: r.AddedBy, AgentID: home.AgentID,
		}); err == nil {
			_ = reg.UntrackRange(sessionID, r.Folder, r.Head, "worktree removed; the range moved to "+home.Path, r.AgentID, removedTip(r))
		}
		return
	}
	if r.HeadSHA == "" {
		return
	}
	_ = gitrepo.PinRef(home.Path, "refs/sloprail/pins/"+r.HeadSHA, r.HeadSHA)
	if err := reg.TrackRange(sessionstate.TrackedRange{
		SessionID: sessionID, Folder: filepath.Clean(home.Path), Head: r.HeadSHA, HeadSHA: r.HeadSHA,
		Base: r.Base, AddedBy: r.AddedBy, AgentID: home.AgentID,
	}); err == nil {
		_ = reg.UntrackRange(sessionID, r.Folder, r.Head, fmt.Sprintf("worktree removed and branch %s is gone; the range moved to %s, pinned at %s", r.Head, home.Path, shortRev(r.HeadSHA)), r.AgentID, removedTip(r))
	}
}

// removedTip is the tip an untrack of a range in a removed folder is recorded against: that
// folder never comes back, so the range's last tip (or a marker no tip equals) does.
func removedTip(r sessionstate.TrackedRange) string {
	if r.HeadSHA != "" {
		return r.HeadSHA
	}
	return "removed"
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
		// Identity is asked for only where there is something to verify: a file-guard loads here.
		// A project with no rules is never blocked for a session it cannot name.
		quiet := &cobra.Command{}
		quiet.SetOut(io.Discard)
		quiet.SetErr(io.Discard)
		// With no session there is no recorded start to vouch for a project's switch-offs of
		// protected rules, so the commit the folder stands at does.
		rev := "HEAD"
		if _, sha, ok := trackedHead(p.Cwd); ok {
			rev = sha
		}
		if len(newNatureDeclarations(quiet, p.Cwd, reg, rev).FileGuards) == 0 {
			return nil
		}
		return identityRefusal(cmd, p, reg, store, err)
	}
	if _, err := os.Stat(rs.Path); err != nil {
		if os.IsNotExist(err) {
			return nil // a session that never recorded anything has no ranges
		}
		return []string{unreadableRegistry(rs.Path, err).Error() + "; refusing because a registry that could not be read must not be read as 'nothing to judge'"}
	}
	root, err := sessionstate.Open(rs.Path)
	if err != nil {
		return []string{unreadableRegistry(rs.Path, err).Error() + "; refusing because a registry that could not be read must not be read as 'nothing to judge'"}
	}
	defer root.Close()
	var trackRefusal []string
	tTrack := time.Now()
	err = errors.Join(trackMissing(root, rs, p), trackFolders(root, rs, p))
	debugTiming(cmd, "track", tTrack)
	if err != nil {
		trackRefusal = []string{fmt.Sprintf("the session's branches could not be observed (%v); refusing because what could not be observed must not be read as 'not tracked'. Fix the repository error and stop again.", err)}
	}
	ranges, err := root.Ranges(rs.ID)
	if err != nil {
		return []string{unreadableRegistry(rs.Path, err).Error() + "; refusing because a registry that could not be read must not be read as 'nothing to judge'"}
	}
	untrackGone(root, rs.ID, ranges)
	if ranges, err = root.Ranges(rs.ID); err != nil {
		return []string{unreadableRegistry(rs.Path, err).Error() + "; refusing because a registry that could not be read must not be read as 'nothing to judge'"}
	}

	quiet := &cobra.Command{}
	quiet.SetOut(io.Discard)
	quiet.SetErr(io.Discard)
	out, notes := trackRefusal, []string(nil)
	tSerial := time.Now()
	var due []sessionstate.TrackedRange
	memo := newCoverMemo()
	vmemo := &verifyMemo{store: store, plugins: func(folder string) []string { return pluginRuleHashes(quiet, folder, reg) }}
	recorded := lazyRecordedCitations(p, store)
	seen := &seenRanges{m: map[string]bool{}} // (repo, head, base) already verified this Stop
	oneEach := collapseByRepo(ranges, p.AgentID, memo)
	plan := settleRootAgents(cmd, root, rs.ID, p, ranges)
	running := plan.Waiting
	var waiting []string
	silentSaid := map[string]bool{}
	// The root's own folder tracks the branch a sub-agent's worktree has checked out too, as a row
	// with no agent_id: the SAME range (one repository, one branch) seen from the other folder.
	// It belongs to the running agent as much as the agent's own row does.
	runningBranch := map[string]string{} // repository + branch -> the running agent holding it
	for _, r := range ranges {
		if r.Tracked() && r.AgentID != "" && running[r.AgentID] {
			runningBranch[repoOf(r.Folder)+"\x00"+r.Head] = r.AgentID
		}
	}
	for i, r := range ranges {
		if p.AgentID != "" && r.AgentID != p.AgentID {
			continue // a sub-agent verifies its own ranges; the root's Stop covers all of them
		}
		if agent := runningAgentOf(r, running, runningBranch); agent != "" {
			if msg, silent := plan.Silent[agent]; silent {
				if !silentSaid[agent] {
					silentSaid[agent] = true
					waiting = append(waiting, msg)
				}
			} else {
				waiting = append(waiting, fmt.Sprintf("not judged yet: sub-agent %s still running (%s %s)", agent, r.Folder, r.Head))
			}
			continue // half-finished work of a background agent that has not reported back: judged at the first Stop after its terminal notification
		}
		if r.Tracked() && !oneEach[i] {
			continue // the same branch of the same repository, tracked from another worktree: one range
		}
		if !r.Tracked() {
			if r.UntrackedReason == prunedReason {
				continue // housekeeping, not a decision anyone should be told about
			}
			notes = append(notes, fmt.Sprintf("untracked: %s %s (reason: %s)", r.Folder, r.Head, r.UntrackedReason))
			continue
		}
		if coveredByBranch(r, ranges, memo) {
			continue // commits left on a detached HEAD, since given a branch: that branch's range holds them
		}
		if coveredWhenGone(r, ranges, memo) {
			continue // a folder that no longer exists, whose commits another tracked range holds
		}
		due = append(due, r)
	}
	debugTiming(cmd, fmt.Sprintf("select-ranges (%d of %d)", len(due), len(ranges)), tSerial)
	// The distinct ranges are verified in parallel, bounded; refusals keep the registry's order.
	// A range whose commits, rules and stored verdicts are as when it was last verified keeps
	// its answer, found from facts read once per repository and folder: no git per range.
	qkeys := make([]string, len(due))
	answers := make([]*string, len(due))
	taken := map[string]bool{}
	for i, r := range due {
		qkeys[i] = vmemo.quickKey(r, p, memo)
		if qkeys[i] == "" {
			continue
		}
		if taken[qkeys[i]] {
			answers[i] = new(string) // the same commits of the same folder: the first row answers
			continue
		}
		taken[qkeys[i]] = true
		if got, ok := vmemo.get(qkeys[i]); ok {
			answers[i] = &got
		}
	}
	if os.Getenv("SLOPRAIL_DEBUG_TIMING") == "1" {
		hit, none := 0, 0
		for i := range due {
			if answers[i] != nil {
				hit++
			} else if qkeys[i] == "" {
				none++
			}
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: timing memo hits %d, no quick key %d, of %d\n", hit, none, len(due))
	}
	reasons := make([]string, len(due))
	sem := make(chan struct{}, rangeVerifyConcurrency)
	var wg sync.WaitGroup
	for i, r := range due {
		if answers[i] != nil {
			reasons[i] = *answers[i]
			continue
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(i int, r sessionstate.TrackedRange, qkey string) {
			defer wg.Done()
			defer func() { <-sem }()
			t0 := time.Now()
			reasons[i] = verifyRangeWith(cmd, p, reg, quiet, r, recorded, seen, vmemo, qkey)
			debugTiming(cmd, "range "+r.Folder+" "+r.Head, t0)
		}(i, r, qkeys[i])
	}
	wg.Wait()
	for _, reason := range reasons {
		if reason != "" {
			out = append(out, reason)
		}
	}
	if len(waiting) > 0 {
		waiting = uniqueLines(waiting)
		// A skip never refuses the Stop: the Stop's caller shows the note (to the user when the
		// Stop passes, in the refusal when it does not). Without a collector it goes to stderr,
		// and into a refusal that exists anyway.
		if !addStopNotice(cmd, waiting...) {
			fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: "+strings.Join(waiting, "; "))
			if len(out) > 0 {
				out = append(out, strings.Join(waiting, "; ")+".")
			}
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

// effectiveBase is the base a tracked range is judged from: an explicit one (an agent's
// `refs track --base`) as given; otherwise (an automatic row, or `refs track` without --base)
// the default, the head's merge base with the remote default branch NOW, whatever was pulled or
// pushed since the row was made (see autoBase). head is the revision the row's head names.
func effectiveBase(r sessionstate.TrackedRange, head string) string {
	if r.Base != "" && r.AddedBy != sessionstate.RangeAuto {
		return r.Base
	}
	sha := r.HeadSHA
	if h, err := gitrepo.ResolveRange(r.Folder, "HEAD", head); err == nil {
		sha = h.Head
	}
	if db, ok := gitrepo.DefaultBase(r.Folder, sha); ok {
		return db
	}
	if r.Base == "" {
		return gitrepo.EmptyTree
	}
	return r.Base
}

// verifyRange verifies one tracked range, returning the refusal or "". It only reads what `sr-checks run`
// stored: no check is executed, so it needs no record or session id.
func verifyRange(cmd *cobra.Command, p HookPayload, reg *module.Registry, store sessionstate.Store, quiet *cobra.Command, r sessionstate.TrackedRange) string {
	return verifyRangeWith(cmd, p, reg, quiet, r, lazyRecordedCitations(p, store), nil, nil, "")
}

var repoOfMemo, repoIDMemo, commonOfMemo sync.Map

// commonOf is the git directory (the object store and refs) a folder's repository keeps: what
// a branch tip is read from. Worktrees share it; clones of one remote (one RepoID) do not, and
// their refs may stand at different commits.
func commonOf(folder string) string {
	if v, ok := commonOfMemo.Load(folder); ok {
		return v.(string)
	}
	c, err := gitrepo.CommonDir(folder)
	if err != nil {
		c = folder
	}
	commonOfMemo.Store(folder, c)
	return c
}

// repoOf names the repository a folder belongs to: its git common dir, shared by every worktree
// of it (the folder itself when git cannot say). Worktrees of one repository track the same
// branches, and the same commits are one range however many folders name them.
func repoOf(folder string) string {
	if v, ok := repoOfMemo.Load(folder); ok {
		return v.(string)
	}
	// The repository's stable identity: worktrees and clones of one repository share it.
	// Worktrees share a git directory, so the identity (which walks history for the initial
	// commit) is read once per git directory, not once per worktree.
	repo := folder
	if common, err := gitrepo.CommonDir(folder); err == nil {
		if v, ok := repoIDMemo.Load(common); ok {
			repo = v.(string)
		} else if id, err := gitrepo.RepoID(folder); err == nil {
			repo = id
			repoIDMemo.Store(common, id)
		}
	} else if id, err := gitrepo.RepoID(folder); err == nil {
		repo = id
	}
	repoOfMemo.Store(folder, repo)
	return repo
}

// seenRanges remembers which (repo, head, base) a Stop has taken up, safe for the parallel verify.
type seenRanges struct {
	mu sync.Mutex
	m  map[string]bool
}

// first reports whether key has not been taken up before, and takes it up.
func (s *seenRanges) first(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m[key] {
		return false
	}
	s.m[key] = true
	return true
}

// rangeVerifyConcurrency bounds how many ranges one Stop verifies at once.
const rangeVerifyConcurrency = 8

// verifyRangeWith is verifyRange with the recorded-quotes hint supplied lazily, so a Stop that
// verifies many ranges builds it at most once, and only if a citation refusal needs it.
func verifyRangeWith(cmd *cobra.Command, p HookPayload, reg *module.Registry, quiet *cobra.Command, r sessionstate.TrackedRange, recorded func() map[string][]transcript.Citation, seen *seenRanges, vm *verifyMemo, qkey string) string {
	head, goneNote := headRevision(r)
	r.Base = effectiveBase(r, head)
	if seen != nil {
		// Rows that name the same commits of the same folder are one range: verified once.
		key := commonOf(r.Folder) + "\x00" + head + "\x00" + r.Base
		if !seen.first(key) {
			return ""
		}
	}
	headName := r.Head
	if isCommitHead(r.Folder, headName) {
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
	if rng.Base == rng.Head {
		vm.put(qkey, "")
		return "" // its tip is reachable from the remote default (landed): nothing of it is owed
	}
	// The range's base vouches for the project's own switch-offs of protected rules, as it does
	// under `sr-checks run`: verify must load the same rules the run judged.
	loaded := newNatureDeclarations(quiet, r.Folder, reg, rng.Base)
	// A rule that failed to load judged nothing: refuse, as `sr-checks verify` does.
	broken := checkrun.BrokenFileGuards(loaded)
	if len(loaded.FileGuards) == 0 && len(broken) == 0 {
		vm.put(qkey, "")
		return ""
	}
	if len(loaded.FileGuards) == 0 {
		vm.put(qkey, where+": "+joinRefusals(broken)+goneNote)
		return where + ": " + joinRefusals(broken) + goneNote
	}
	cache, err := checkrun.OpenLocalCache(r.Folder)
	if err != nil {
		return fmt.Sprintf("%s: the check results could not be opened (%v); refusing because results that could not be read must not be read as 'nothing was judged'.", where, err)
	}
	cache.FreezeTip() // verify only reads: the ref is read once, not once per guard
	// Verify is a pure function of the commits, the rules and the stored verdicts: a range
	// already verified under all three keeps its answer.
	memoKey := vm.key(r, p, where, rng, loaded.FileGuards, broken, cache.Tip())
	if got, ok := vm.get(memoKey); ok {
		vm.put(qkey, got)
		return got
	}
	results := checkstore.Open(cache, true)
	defer results.Close()
	refusals, outcomes := checkrun.Evaluate(checkrun.Params{
		Err: io.Discard, Guards: loaded.FileGuards, Root: r.Folder, Range: rng, Cwd: r.Folder,
		Workspace: r.Folder, AgentID: p.AgentID, Subagent: p.IsSubagent(),
		Store: results, Verify: true, RecordedFn: recorded,
	})
	if len(refusals) == 0 && len(broken) == 0 {
		vm.put(memoKey, "")
		vm.put(qkey, "")
		return ""
	}
	parts := append([]string(nil), broken...)
	parts = append(parts, groupRefusals(refusals, outcomes)...)
	out := where + ": " + joinRefusals(parts) + goneNote
	if len(r.Head) < 40 && !strings.HasPrefix(r.Head, "refs/") {
		out += fmt.Sprintf("\nIf %s is not yours to answer for (the user said to drop it), stop answering for it: `sr-session refs untrack --head %s --reason '<why>'`, and `sr-session refs track --head %s` takes it back.", r.Head, r.Head, r.Head)
	}
	vm.put(memoKey, out)
	vm.put(qkey, out)
	return out
}

// verifyMemo keeps, in the session's store, what verifying a range answered, so a Stop pays
// only for the ranges whose commits, rules or stored verdicts changed since the last one.
// A nil memo remembers nothing.
type verifyMemo struct {
	store sessionstate.Store
	mu    sync.Mutex

	hashMu sync.Mutex
	facts  map[string]string
	// plugins is the sorted hashes of the plugin rules a folder loads (nil: none).
	plugins func(folder string) []string
	hashes  map[string][]string // (folder, its rules) -> their hashes, read once per Stop
}

// ruleHashes is the hash of every guard's rule, read from disk once per (folder, rule set) for
// the Stop: every range of a folder loads the same rules.
func (m *verifyMemo) ruleHashes(folder string, guards []declaration.FileGuard) ([]string, error) {
	dirs := make([]string, len(guards))
	plugins := make([]bool, len(guards))
	for i, g := range guards {
		dirs[i], plugins[i] = g.Dir, g.Origin.FromPlugin()
	}
	k := folder + "\x00" + strings.Join(dirs, "\x00")
	m.hashMu.Lock() // held while hashing: ranges of one folder wait for the one hashing, not repeat it
	defer m.hashMu.Unlock()
	if h, ok := m.hashes[k]; ok {
		return h, nil
	}
	h, err := changeset.RuleHashesAt(folder, dirs, plugins)
	if err != nil {
		return nil, err
	}
	if m.hashes == nil {
		m.hashes = map[string][]string{}
	}
	m.hashes[k] = h
	return h, nil
}

const verifyMemoPrefix = "verify-memo:"

// key is the digest of everything a range's verify answer depends on: the repository, the two
// commits, the rules that load (their hashes, and the ones that failed to load), who asks, how
// the refusal is worded, and the tip of the results ref (a new `sr-checks run` moves it).
func (m *verifyMemo) key(r sessionstate.TrackedRange, p HookPayload, where string, rng gitrepo.Range, guards []declaration.FileGuard, broken []string, resultsTip string) string {
	if m == nil || m.store == nil {
		return ""
	}
	got, err := m.ruleHashes(r.Folder, guards)
	if err != nil {
		return "" // a rule that cannot be hashed is never memoized
	}
	hashes := make([]string, 0, len(guards))
	for i, g := range guards {
		hashes = append(hashes, g.Qualified()+"="+got[i])
	}
	sort.Strings(hashes)
	h := sha256.New()
	for _, part := range [][]string{{repoOf(r.Folder), rng.Base, rng.Head, p.AgentID, fmt.Sprint(p.IsSubagent()), where, r.Head, resultsTip}, hashes, broken} {
		for _, x := range part {
			fmt.Fprintf(h, "%d:%s;", len(x), x)
		}
		h.Write([]byte{0})
	}
	return verifyMemoPrefix + hex.EncodeToString(h.Sum(nil))
}

// isHexSHA reports whether s is a full object name.
func isHexSHA(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

// quickKey is the memo key of a range that follows a local branch at a known commit, from facts
// read once per repository (the branch tips, the remote default's tip) and once per folder (the
// hash of its .sloprail, the tip of the results ref): no git process per range. The range's base
// is a function of its head and the remote default, so neither is resolved. "" when a row needs
// more (an explicit base, a bare commit, a branch that is gone): it is verified in full.
func (m *verifyMemo) quickKey(r sessionstate.TrackedRange, p HookPayload, cm *coverMemo) string {
	if m == nil || m.store == nil || r.Head == "" || strings.HasPrefix(r.Head, "detached/") {
		return ""
	}
	base := ""
	if r.Base != "" && r.AddedBy != sessionstate.RangeAuto {
		if !isHexSHA(r.Base) {
			return "" // a moving revision name: resolved in full
		}
		base = r.Base
	}
	var sha string
	switch name := strings.TrimPrefix(r.Head, "refs/heads/"); {
	case strings.HasPrefix(r.Head, "refs/") && cm.branches(r.Folder)[r.Head] != "":
		sha = cm.branches(r.Folder)[r.Head]
	case strings.HasPrefix(r.Head, "refs/") && !strings.HasPrefix(r.Head, "refs/heads/") && isHexSHA(r.HeadSHA):
		sha = r.HeadSHA // a ref that is gone: verified at the commit it last pointed at
	case strings.HasPrefix(r.Head, "refs/") && !strings.HasPrefix(r.Head, "refs/heads/"):
		return ""
	case cm.branches(r.Folder)[name] != "":
		sha = cm.branches(r.Folder)[name]
	case isHexSHA(r.Head):
		sha = r.Head // a bare commit
	case isHexSHA(r.HeadSHA):
		sha = r.HeadSHA // a branch that is gone: verified at the commit it last pointed at
	default:
		return ""
	}
	repo := repoOf(r.Folder)
	common := commonOf(r.Folder)
	def, ok := cm.defaultTips[common]
	if !ok {
		def, _ = gitrepo.RemoteDefaultTip(r.Folder)
		cm.defaultTips[common] = def
	}
	folder := m.folderFacts(r.Folder)
	if folder == "" {
		return ""
	}
	h := sha256.New()
	for _, x := range []string{"quick", repo, r.Folder, r.Head, sha, base, def, p.AgentID, fmt.Sprint(p.IsSubagent()), folder} {
		fmt.Fprintf(h, "%d:%s;", len(x), x)
	}
	return verifyMemoPrefix + hex.EncodeToString(h.Sum(nil))
}

// untrackedRules fingerprints the files under .sloprail that git neither tracks nor ignores
// (path and content): the rules a full verify loads from disk and the tracked-files hash misses.
func untrackedRules(folder string) string {
	files, err := gitrepo.UnignoredFiles(folder, ".sloprail")
	if err != nil {
		return "unreadable"
	}
	sort.Strings(files)
	h := sha256.New()
	for _, f := range files {
		body, err := os.ReadFile(filepath.Join(folder, f))
		if err != nil {
			body = []byte("unreadable")
		}
		fmt.Fprintf(h, "%d:%s%d:", len(f), f, len(body))
		h.Write(body)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// pluginRuleHashes is the hash of every plugin rule the folder loads, sorted: a plugin lives
// outside the repository, so an update to it is not seen in the hash of the .sloprail.
func pluginRuleHashes(quiet *cobra.Command, folder string, reg *module.Registry) []string {
	var out []string
	for _, g := range newNatureDeclarations(quiet, folder, reg).FileGuards {
		if !g.Origin.FromPlugin() {
			continue
		}
		h, err := changeset.RuleHashAt(folder, g.Dir, true)
		if err != nil {
			h = "unreadable"
		}
		out = append(out, g.Qualified()+"="+h)
	}
	sort.Strings(out)
	return out
}

// folderFacts is what, besides commits, a folder's verify answer rests on: the hash of its
// .sloprail (every rule, as on disk) and the tip of its results ref. "" when either cannot be
// read. Read once per folder per Stop.
func (m *verifyMemo) folderFacts(folder string) string {
	m.hashMu.Lock()
	defer m.hashMu.Unlock()
	if v, ok := m.facts[folder]; ok {
		return v
	}
	v := ""
	if h, err := changeset.RuleHashAt(folder, filepath.Join(folder, ".sloprail"), false); err == nil {
		if cache, err := checkrun.OpenLocalCache(folder); err == nil {
			v = h + "@" + cache.Tip() + "@" + untrackedRules(folder)
			if m.plugins != nil {
				v += "@" + strings.Join(m.plugins(folder), ",")
			}
		}
	}
	if m.facts == nil {
		m.facts = map[string]string{}
	}
	m.facts[folder] = v
	return v
}

func (m *verifyMemo) get(key string) (string, bool) {
	if m == nil || m.store == nil || key == "" {
		return "", false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok, err := m.store.Meta(key)
	if err != nil || !ok || len(v) == 0 {
		return "", false
	}
	return v[1:], v[0] == 'R' || v[0] == 'P'
}

func (m *verifyMemo) put(key, answer string) {
	if m == nil || m.store == nil || key == "" {
		return
	}
	tag := "P"
	if answer != "" {
		tag = "R"
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	_ = m.store.SetMeta(key, tag+answer)
}

// headRevision is the revision a tracked range's head names now: its branch, else the commit it
// was tracked at (with a note that the branch is gone).
func headRevision(r sessionstate.TrackedRange) (rev, note string) {
	head := r.Head
	switch {
	case strings.HasPrefix(head, "refs/"), isCommitHead(r.Folder, head):
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
		return r.HeadSHA, goneBranchNote(r)
	}
	return head, ""
}

func goneBranchNote(r sessionstate.TrackedRange) string {
	return fmt.Sprintf(" The branch %q is gone: verified at the commit it last pointed at (%s). Re-track the range under another head (`sr-session refs track`) or untrack it with a reason (`sr-session refs untrack`).", r.Head, shortRev(r.HeadSHA))
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
			for i := range ranges { // the base each range is judged from now, not the one stored
				head, _ := headRevision(ranges[i])
				ranges[i].Base = effectiveBase(ranges[i], head)
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

// checkTrackBase refuses a base that would empty the range: the head itself or one of its
// descendants. Dropping a range is `refs untrack`, which takes a reason and is listed at Stop.
func checkTrackBase(dir, base, headSHA string) error {
	rng, err := gitrepo.ResolveRange(dir, base, headSHA)
	if err != nil {
		return fmt.Errorf("sloprail: %w", err)
	}
	if rng.Empty() { // merge-base(base, head) is the head: base is the head or a descendant
		return fmt.Errorf("sloprail: --base %s is the head or a descendant of it: the range would hold no commit, and that is `sr-session refs untrack --reason '<why>'`, not a track", base)
	}
	return nil
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
			shown := base
			if base == "" {
				// No explicit base: stored empty, so every Stop reads the default (the merge base
				// with the remote default branch) afresh. An explicit base is used as given.
				started := ""
				if f, found, _ := s.reg.Folder(s.rs.ID, dir); found {
					started = f.BaseRef
				}
				shown = autoBase(dir, headSHA, started)
			} else if err := checkTrackBase(dir, base, headSHA); err != nil {
				return err
			}
			if err := s.reg.TrackRange(sessionstate.TrackedRange{
				SessionID: s.rs.ID, Folder: dir, Head: headName, HeadSHA: headSHA, Base: base,
				AddedBy: sessionstate.RangeAgent, AgentID: s.ownerOf(dir),
			}); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "tracking %s in %s from %s\n", headName, dir, shortRev(shown))
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
			out, err := gitrepo.ResolveRange(dir, "HEAD", headRef(dir, head))
			if err != nil {
				return fmt.Errorf("sloprail: the tip of %s in %s could not be read (%v), so the untrack could not be recorded against it", head, dir, err)
			}
			tip := out.Head
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
func headRef(folder, head string) string {
	if strings.HasPrefix(head, "refs/") || isCommitHead(folder, head) {
		return head
	}
	return "refs/heads/" + head
}

// lazyRecordedCitations is recordedCitations computed on first call and remembered: one Stop,
// however many ranges, reads every sub-agent's store at most once.
func lazyRecordedCitations(p HookPayload, store sessionstate.Store) func() map[string][]transcript.Citation {
	var once sync.Once
	var got map[string][]transcript.Citation
	return func() map[string][]transcript.Citation {
		once.Do(func() { got = recordedCitations(p, store) })
		return got
	}
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

// coveredWhenGone reports whether a range whose folder no longer exists is held by another
// tracked range: its tip is reachable from that range's live head, so verifying that one judges
// these commits too. A range that cannot be shown held is verified (and refused, if unreadable).
func coveredWhenGone(r sessionstate.TrackedRange, all []sessionstate.TrackedRange, m *coverMemo) bool {
	if r.HeadSHA == "" {
		return false
	}
	if _, err := os.Stat(r.Folder); err == nil {
		return false
	}
	for _, o := range all {
		if !o.Tracked() || o.Folder == r.Folder || o.AgentID != r.AgentID {
			continue
		}
		if _, err := os.Stat(o.Folder); err != nil {
			continue
		}
		tip, note := m.headRevision(o)
		if note != "" {
			continue
		}
		if name, ok := strings.CutPrefix(tip, "refs/heads/"); ok {
			// One git call per (repository, commit) lists every branch holding it.
			if m.branchesHolding(o.Folder, r.HeadSHA)[name] {
				return true
			}
			continue
		}
		if in, err := m.isAncestor(o.Folder, r.HeadSHA, tip); err == nil && in {
			return true
		}
	}
	return false
}

// coveredByBranch reports whether a range tracked at a bare commit (a detached HEAD) is held by
// another tracked range of the same folder that is on a branch: the commit is in that range
// (reachable from the branch's live tip, not from its base), so verifying the branch judges it
// as well. A commit older than the branch's base, or one the branch cannot be shown to hold, is
// not covered: it is verified on its own.
func coveredByBranch(r sessionstate.TrackedRange, all []sessionstate.TrackedRange, m *coverMemo) bool {
	if _, err := os.Stat(r.Folder); err != nil {
		return false // a folder that is gone holds no branch: no git call is made for it
	}
	commit := r.Head
	switch {
	case m.isCommitHead(r.Folder, r.Head):
	case r.HeadSHA != "":
		// A branch that is gone (renamed, deleted): the commit it last pointed at.
		if _, note := m.headRevision(r); note == "" {
			return false
		}
		commit = r.HeadSHA
	default:
		return false
	}
	for _, o := range all {
		if !o.Tracked() || o.Folder != r.Folder || o.AgentID != r.AgentID || o.HeadSHA == "" || o.Base == "" || o.Head == r.Head || m.isCommitHead(o.Folder, o.Head) {
			continue
		}
		tip, note := m.headRevision(o) // the live tip
		if note != "" {
			continue // a branch that is gone holds nothing now
		}
		if name, ok := strings.CutPrefix(tip, "refs/heads/"); ok {
			if !m.branchesHolding(r.Folder, commit)[name] {
				continue // one git call per commit lists every branch that holds it
			}
		} else if in, err := m.isAncestor(r.Folder, commit, tip); err != nil || !in {
			continue
		}
		if o.Base != gitrepo.EmptyTree {
			if before, err := m.isAncestor(r.Folder, commit, o.Base); err != nil || before {
				continue
			}
		}
		return true
	}
	return false
}

// collapseByRepo picks, for each branch of each repository (per agent), the one tracked row that
// answers for it: worktrees of one repository share every branch, so a branch registered once
// per worktree is one range. The row with an explicit base wins, then the latest. A row at a bare commit, or of a folder that no longer exists, is its own. The result is indexed like
// ranges; an untracked row is never picked here (the caller lists it).
func collapseByRepo(ranges []sessionstate.TrackedRange, agentID string, m *coverMemo) []bool {
	pick := make([]bool, len(ranges))
	best := map[string]int{}
	rank := func(r sessionstate.TrackedRange) int {
		n := 0
		if r.Base != "" && r.AddedBy != sessionstate.RangeAuto {
			n += 2
		}
		if st, err := os.Stat(r.Folder); err == nil && st.IsDir() {
			n++
		}
		return n
	}
	for i, r := range ranges {
		if !r.Tracked() || (agentID != "" && r.AgentID != agentID) {
			continue
		}
		if st, err := os.Stat(r.Folder); err != nil || !st.IsDir() {
			// A folder that is gone (coveredWhenGone decides): rows naming the same head at the
			// same commit are one.
			k := "gone\x00" + r.AgentID + "\x00" + r.Head + "\x00" + r.HeadSHA
			if _, dup := best[k]; !dup {
				best[k] = i
			}
			continue
		}
		if m.isCommitHead(r.Folder, r.Head) {
			pick[i] = true // a bare commit: its own range
			continue
		}
		head := strings.TrimPrefix(r.Head, "refs/heads/")
		// One range per (repository, ref, tip): clones of one remote are one repository, but the
		// same ref at another tip is another range, verified where its commit is.
		tip := m.branches(r.Folder)[head]
		if tip == "" {
			tip = r.HeadSHA
		}
		key := repoOf(r.Folder) + "\x00" + r.AgentID + "\x00" + head + "\x00" + tip
		if j, ok := best[key]; !ok || rank(r) >= rank(ranges[j]) {
			best[key] = i
		}
	}
	for _, i := range best {
		pick[i] = true
	}
	return pick
}

// coverMemo remembers, for one Stop, what git said about a head or a pair of commits, so the
// pairwise coverage check spawns one git process per distinct question, not one per pair.
// It is used from the sequential part of the Stop only.
type coverMemo struct {
	commitHead  map[string]bool
	revs        map[string][2]string
	anc         map[string][2]any
	holding     map[string]map[string]bool
	branchSets  map[string]map[string]string
	defaultTips map[string]string
}

func newCoverMemo() *coverMemo {
	return &coverMemo{commitHead: map[string]bool{}, revs: map[string][2]string{}, anc: map[string][2]any{}, holding: map[string]map[string]bool{}, branchSets: map[string]map[string]string{}, defaultTips: map[string]string{}}
}

// branchesHolding is the local branches whose tip has commit among its ancestors, one git call
// per (repository, commit).
func (m *coverMemo) branchesHolding(folder, commit string) map[string]bool {
	k := commonOf(folder) + "\x00" + commit
	if v, ok := m.holding[k]; ok {
		return v
	}
	v := map[string]bool{}
	if out, err := exec.Command("git", "-C", folder, "for-each-ref", "--contains", commit, "--format=%(refname:short)", "refs/heads").Output(); err == nil {
		for _, ln := range strings.Split(string(out), "\n") {
			if ln = strings.TrimSpace(ln); ln != "" {
				v[ln] = true
			}
		}
	}
	m.holding[k] = v
	return v
}

func (m *coverMemo) isCommitHead(folder, head string) bool {
	k := folder + "\x00" + head
	if v, ok := m.commitHead[k]; ok {
		return v
	}
	v := isCommitHead(folder, head)
	m.commitHead[k] = v
	return v
}

// branches is the local branches of the repository folder belongs to and the commits they stand
// at, one git call each.
func (m *coverMemo) branches(folder string) map[string]string {
	k := commonOf(folder) // this clone's refs, never another's
	if v, ok := m.branchSets[k]; ok {
		return v
	}
	v := map[string]string{}
	if out, err := exec.Command("git", "-C", folder, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads", "refs/remotes").Output(); err == nil {
		for _, ln := range strings.Split(string(out), "\n") {
			if f := strings.Fields(ln); len(f) == 2 {
				if name, ok := strings.CutPrefix(f[0], "refs/heads/"); ok {
					v[name] = f[1] // a branch by its name
				}
				v[f[0]] = f[1] // and every ref by its full name
			}
		}
	}
	m.branchSets[k] = v
	return v
}

func (m *coverMemo) headRevision(r sessionstate.TrackedRange) (string, string) {
	k := r.Folder + "\x00" + r.Head + "\x00" + r.HeadSHA
	if v, ok := m.revs[k]; ok {
		return v[0], v[1]
	}
	if r.Head != "" && !strings.HasPrefix(r.Head, "refs/") && !strings.HasPrefix(r.Head, "detached/") && m.branches(r.Folder)[r.Head] != "" {
		// A local branch that stands: its ref is the revision (what headRevision settles with
		// two git calls per row).
		m.revs[k] = [2]string{"refs/heads/" + r.Head, ""}
		return "refs/heads/" + r.Head, ""
	}
	if strings.HasPrefix(r.Head, "refs/") {
		// A full ref name: standing, it is its own revision; gone, the commit it last pointed at.
		if set := m.branches(r.Folder); len(set) > 0 && (strings.HasPrefix(r.Head, "refs/heads/") || strings.HasPrefix(r.Head, "refs/remotes/")) {
			if set[r.Head] != "" {
				m.revs[k] = [2]string{r.Head, ""}
				return r.Head, ""
			}
			if r.HeadSHA != "" {
				m.revs[k] = [2]string{r.HeadSHA, goneBranchNote(r)}
				return r.HeadSHA, goneBranchNote(r)
			}
		}
	}
	if r.Head != "" && r.HeadSHA != "" && !strings.HasPrefix(r.Head, "refs/") && !strings.HasPrefix(r.Head, "detached/") && !isHexSHA(r.Head) {
		if set := m.branches(r.Folder); len(set) > 0 && set[r.Head] == "" {
			// A branch no longer in the repository: the commit it last pointed at.
			m.revs[k] = [2]string{r.HeadSHA, goneBranchNote(r)}
			return r.HeadSHA, goneBranchNote(r)
		}
	}
	rev, note := headRevision(r)
	m.revs[k] = [2]string{rev, note}
	return rev, note
}

func (m *coverMemo) isAncestor(folder, a, b string) (bool, error) {
	k := folder + "\x00" + a + "\x00" + b
	if v, ok := m.anc[k]; ok {
		err, _ := v[1].(error)
		return v[0].(bool), err
	}
	in, err := gitrepo.IsAncestor(folder, a, b)
	m.anc[k] = [2]any{in, err}
	return in, err
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

// isCommitHead reports whether a tracked head names a bare commit (a detached HEAD) rather than
// a branch: a full object id (40 or 64 hex digits) that is no local branch's name. Decided by
// what the name is, never by its length: a branch may be named at any length.
func isCommitHead(folder, head string) bool {
	if strings.HasPrefix(head, "refs/") || (len(head) != 40 && len(head) != 64) {
		return false
	}
	for _, c := range head {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return exec.Command("git", "-C", folder, "rev-parse", "--verify", "--quiet", "refs/heads/"+head).Run() != nil
}

// prunedReason is why a row the removed first-sight rule made is dropped.
const prunedReason = "pruned: tracked at first sight, never moved"

// pruneUnmovedAuto untracks, once (a pruned row is not touched again), every row the engine
// tracked by itself that never moved: its recorded tip is the one it was registered at AND its
// branch still stands there. The removed first-sight rule tracked every branch of a folder it
// met late (a folder that is not the session's root); those rows owe nothing. The branch a folder
// has checked out is kept: the session stands on it. Explicit rows, moved rows, commit heads and rows whose
// branch cannot be read are kept. A branch that moves later is tracked again by the next hook.
func pruneUnmovedAuto(reg sessionstate.Store, sessionID string) error {
	ranges, err := reg.Ranges(sessionID)
	if err != nil {
		return err
	}
	folders, err := reg.Folders(sessionID)
	if err != nil {
		return err
	}
	lateFolder := map[string]bool{} // a folder that is not the session's own root: where first sight over-tracked
	for _, f := range folders {
		if f.Role != sessionstate.FolderRoot && f.Role != sessionstate.FolderSubagentWorktree {
			lateFolder[filepath.Clean(f.Path)] = true
		}
	}
	checkedOut := map[string]string{}      // folder -> the branch it has checked out, read once
	live := map[string]map[string]string{} // folder -> branch -> tip
	liveTips := func(folder string) map[string]string {
		if m, ok := live[folder]; ok {
			return m
		}
		m := map[string]string{}
		if out, err := exec.Command("git", "-C", folder, "for-each-ref", "--format=%(refname:short) %(objectname)", "refs/heads").Output(); err == nil {
			for _, ln := range strings.Split(string(out), "\n") {
				if f := strings.Fields(ln); len(f) == 2 {
					m[f[0]] = f[1]
				}
			}
		}
		live[folder] = m
		return m
	}
	for _, r := range ranges {
		if !r.Tracked() || r.AddedBy != sessionstate.RangeAuto || r.FirstTip == "" || r.HeadSHA != r.FirstTip || !lateFolder[filepath.Clean(r.Folder)] {
			continue
		}
		cur, seenCur := checkedOut[r.Folder]
		if !seenCur {
			head, _, ok := trackedHead(r.Folder)
			if ok {
				cur = head
			}
			checkedOut[r.Folder] = cur
		}
		if cur != "" && cur == r.Head {
			continue
		}
		if tip, ok := liveTips(r.Folder)[r.Head]; !ok || tip != r.HeadSHA {
			continue
		}
		if err := reg.UntrackRange(sessionID, r.Folder, r.Head, prunedReason, r.AgentID, r.HeadSHA); err != nil {
			return err
		}
	}
	return nil
}

// foreignPrunedReason is why a row a worktree made for another worktree's branch is dropped.
const foreignPrunedReason = "pruned: the branch is checked out in another worktree"

// pruneForeignAuto untracks, once, every row the engine tracked by itself for a branch that is
// checked out in a DIFFERENT worktree than the row's folder. Observation used to track every
// branch visible in the shared ref namespace, so N sub-agent worktrees of one repository held N
// rows each (N x N). The worktree standing on a branch keeps its own row; a branch a folder moved
// and then left (checked out nowhere) is kept, as is a row whose other worktree holds no tracked
// row for the branch (a removed folder's range moved to the root) or one whose base is not the
// pruned row's or older (a narrower range would let the pruned row's commits escape). Explicit rows and rows of vanished folders are kept.
func pruneForeignAuto(reg sessionstate.Store, sessionID string) error {
	ranges, err := reg.Ranges(sessionID)
	if err != nil {
		return err
	}
	held := map[string]sessionstate.TrackedRange{} // folder\x00branch -> the tracked row of the worktree that stands on it
	for _, r := range ranges {
		if r.Tracked() {
			held[realPath(r.Folder)+"\x00"+r.Head] = r
		}
	}
	cover := newCoverMemo()
	elsewhere := map[string]map[string]string{} // folder -> branch -> the other worktree on it, read once
	for _, r := range ranges {
		if !r.Tracked() || r.AddedBy != sessionstate.RangeAuto || isCommitHead(r.Folder, r.Head) {
			continue
		}
		if st, err := os.Stat(r.Folder); err != nil || !st.IsDir() {
			continue
		}
		m, ok := elsewhere[r.Folder]
		if !ok {
			m = checkedOutElsewhere(r.Folder)
			elsewhere[r.Folder] = m
		}
		other := m[r.Head]
		if other == "" {
			continue
		}
		keep, ok := held[other+"\x00"+r.Head]
		if !ok || repoOf(keep.Folder) != repoOf(r.Folder) {
			continue // that worktree answers for nothing: this row may be all that holds the work
		}
		// The surviving row must cover everything this one does: its base is this base or older.
		// A sub-agent that checked the branch out later has a narrower range; the commits before
		// it would escape.
		covers, err := cover.isAncestor(r.Folder, effectiveBase(keep, r.Head), effectiveBase(r, r.Head))
		if err != nil || !covers {
			continue
		}
		if err := reg.UntrackRange(sessionID, r.Folder, r.Head, foreignPrunedReason, r.AgentID, r.HeadSHA); err != nil {
			return err
		}
	}
	return nil
}

// settleRootAgents is the sub-agent registry's answer for the ROOT's Stop (see settleAgents): which
// agents' ranges are left for later. Only when some range is an agent's or the registry knows an
// agent; a sub-agent's own Stop waits for no one. An agent the registry does not know is judged.
func settleRootAgents(cmd *cobra.Command, root sessionstate.Store, sessionID string, p HookPayload, ranges []sessionstate.TrackedRange) agentPlan {
	if p.AgentID != "" {
		return agentPlan{}
	}
	hasAgent := false
	for _, r := range ranges {
		if r.AgentID != "" {
			hasAgent = true
		}
	}
	if !hasAgent {
		if known, err := root.Agents(sessionID); err != nil || len(known) == 0 {
			return agentPlan{}
		}
	}
	return settleAgents(cmd, root, sessionID, p, agentClock())
}

// stopNoticesKey carries, in a command's context, the collector of a Stop's notices.
type stopNoticesKey struct{}

// stopNotices are what a Stop wants said without refusing: shown to the user when the Stop
// passes, and appended to the refusal when it does not.
type stopNotices struct {
	mu    sync.Mutex
	lines []string
}

// withStopNotices returns the command with a collector in its context, and the collector.
func withStopNotices(cmd *cobra.Command) *stopNotices {
	n := &stopNotices{}
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	cmd.SetContext(context.WithValue(ctx, stopNoticesKey{}, n))
	return n
}

// addStopNotice records notices on the command's collector; false when it has none.
func addStopNotice(cmd *cobra.Command, lines ...string) bool {
	ctx := cmd.Context()
	if ctx == nil {
		return false
	}
	n, ok := ctx.Value(stopNoticesKey{}).(*stopNotices)
	if !ok {
		return false
	}
	n.mu.Lock()
	n.lines = uniqueLines(append(n.lines, lines...))
	n.mu.Unlock()
	return true
}

func (n *stopNotices) text() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return strings.Join(n.lines, "\n")
}

// uniqueLines drops repeated lines, keeping the first of each, in order.
func uniqueLines(lines []string) []string {
	seen := map[string]bool{}
	out := lines[:0:0]
	for _, l := range lines {
		if !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	return out
}

// groupRefusals words a range's file-guard refusals, one line per distinct reason: rules that
// refused for the same reason (the same "not judged yet — run ..." for the same range) are
// named together, with the files of the subjects they could not judge, instead of the line
// repeated once per rule.
func groupRefusals(refusals []checkrun.FileGuardResult, outcomes []checkrun.CheckOutcome) []string {
	var order []string
	rules := map[string][]string{}
	for _, f := range refusals {
		if _, ok := rules[f.Reason]; !ok {
			order = append(order, f.Reason)
		}
		rules[f.Reason] = append(rules[f.Reason], f.Attribution)
	}
	var out []string
	for _, reason := range order {
		names := uniqueLines(rules[reason])
		line := reason + " (file-guard " + strings.Join(names, ", ")
		if strings.HasPrefix(reason, "not judged yet") {
			var files []string
			for _, o := range outcomes {
				if o.Status == "missing" && o.Subject != "" {
					files = append(files, o.Subject)
				}
			}
			if files = uniqueLines(files); len(files) > 0 && len(names) > 1 {
				line += "; subjects: " + strings.Join(files, ", ")
			}
		}
		out = append(out, line+")")
	}
	return out
}

// runningAgentOf is the still-running agent a tracked range belongs to: the agent its own row
// names, or the one whose row holds the same branch of the same repository. "" when none.
func runningAgentOf(r sessionstate.TrackedRange, running map[string]bool, branches map[string]string) string {
	if !r.Tracked() {
		return ""
	}
	if r.AgentID != "" {
		if running[r.AgentID] {
			return r.AgentID
		}
		return ""
	}
	return branches[repoOf(r.Folder)+"\x00"+r.Head]
}
