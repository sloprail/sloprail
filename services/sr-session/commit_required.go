package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/changeset"
	"github.com/sloprail/sloprail/internal/checkrun"
	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/filemod"
	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/guardrail"
	"github.com/sloprail/sloprail/internal/module"
	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/sloprail/sloprail/internal/transcript"
)

// Commit required.
//
// A file-guard judges COMMITS, so work that is not committed cannot be judged and
// a turn must not end on it. At Stop, if any uncommitted change touches a path
// some file-guard's `match` selects, the Stop is refused with "commit these".
//
// Always on, and with no configuration: whether a project has file-guards is
// already the switch. It never commits for the agent — a10n's silent checkpoint
// commits squashed unrelated work together, and were replaced by refusing — and
// it refuses in the harness's real blocking form (`block`), because a bare string
// returned instead let the agent end its turn anyway.
//
// A rename counts when its new path OR the path it left is selected
// (changeset.Selects): moving a file out of a guarded path is a change to it.
//
// Only paths a rule actually SELECTS count: scratch files and unguarded paths
// never trigger it, and under `deletions: skip` an uncommitted deletion is not
// selected.
//
// Only an agent that owns the tree is gated. A sub-agent working in the session's
// own tree leaves its work where the root's Stop will see it, and refusing the
// sub-agent for the root's uncommitted work blocked a10n's read-only sub-agent
// seventeen times in a row. A sub-agent in a worktree of its own owns that tree
// and is gated on it.
//
// A folder the session registered (not its own tree) where a BACKGROUND sub-agent the registry
// holds as running has worked is that agent's half-done work, which the root must not commit and
// so cannot act on a refusal for: the root's Stop names it ("being worked on by sub-agent <id>")
// and does not refuse, as the tracked ranges leave a running background agent's ranges unjudged.
// Anything else (a foreground, unknown, stale, gone or finished agent) is refused as ever. The
// store attributes by folder, not by path, so the root's own tree is never excused this way.
//
// A loop breaker ends it: after the project's stop_hook_block_cap refusals in a
// row for the SAME uncommitted set the gate says so on stderr and stops refusing,
// so a Stop refusal cannot become a deny loop the agent can only escape by
// unrelated means (a10n's drove an agent to 49 ScheduleWakeup calls). The count is
// this gate's own, keyed on the set, so committing part of the work starts it again.

// maxListedPaths bounds how many uncommitted paths a refusal names.
const maxListedPaths = 20

// uncommittedGuarded is one uncommitted path and the rules that select it.
type uncommittedGuarded struct {
	Path   string
	Status byte
	Rules  []string
}

// commitRequired returns the refusal to end the Stop with, or "" when the tree
// owes no commit (or this agent does not own it, or the loop breaker released it).
//
// It covers the session's own tree and every other folder the session registered for this
// agent (an ad-hoc repository a command ran in), each under ITS OWN rules: reg is what loads
// them. reg may be nil, which covers the own tree only.
func commitRequired(cmd *cobra.Command, p HookPayload, guards []declaration.FileGuard, store sessionstate.Store, reg ...*module.Registry) string {
	if !ownsTree(p) {
		return ""
	}
	var owed []uncommittedGuarded
	covered := map[string]bool{} // trees already walked: one listed twice would owe its paths twice
	if len(guards) > 0 {
		root, err := gitrepo.Root(p.Cwd)
		if err != nil {
			if !isNotARepo(err) {
				return failClosed(err)
			}
		} else {
			o, refusal := owedIn(root, guards)
			if refusal != "" {
				return refusal
			}
			owed = append(owed, o...)
			covered[treeKey(root)] = true
		}
	}
	if len(reg) > 0 && reg[0] != nil {
		quiet := quietCmd()
		others, err := sessionFolders(p)
		if err != nil {
			return failClosed(err) // a registry that could not be read is not "nothing else was committed"
		}
		busy := foldersOfRunningAgents(cmd, p, others)
		for _, f := range others {
			if key := treeKey(f.Path); covered[key] {
				continue
			} else {
				covered[key] = true
			}
			loaded := newNatureDeclarations(quiet, f.Path, reg[0])
			if len(loaded.FileGuards) == 0 {
				continue
			}
			o, refusal := owedIn(f.Path, loaded.FileGuards)
			if refusal != "" {
				return refusal
			}
			if agent, ok := busy[treeKey(f.Path)]; ok && len(o) > 0 {
				// A sub-agent the registry holds as running is mid-work here: the root must not commit its
				// half-done work, so this is said, not refused. Once it stops, the folder is owed again.
				noteBeingWorkedOn(cmd, f.Path, agent)
				continue
			}
			for _, u := range o {
				u.Path = filepath.Join(f.Path, u.Path)
				owed = append(owed, u)
			}
		}
	}
	if len(owed) == 0 {
		resetCommitRequired(cmd, store)
		return ""
	}
	sort.Slice(owed, func(i, j int) bool { return owed[i].Path < owed[j].Path })
	if commitRequiredReleased(cmd, p, store, owed) {
		return ""
	}
	return commitRequiredMessage(owed)
}

// owedIn is the uncommitted paths of the tree at root that some rule of guards selects, or
// the refusal for a tree or a rule that could not be read.
func owedIn(root string, guards []declaration.FileGuard) ([]uncommittedGuarded, string) {
	changes, err := gitrepo.UncommittedChanges(root)
	if err != nil {
		return nil, failClosed(err)
	}
	if len(changes) == 0 {
		return nil, ""
	}
	byPath := map[string]*uncommittedGuarded{}
	for _, g := range guards {
		if isLaunchedBy(os.Getenv, g.Name) {
			continue
		}
		match, err := guardrail.CompileFileMatch(g.Match)
		if err != nil {
			return nil, fmt.Sprintf("the file-guard %q could not be evaluated: its match %q could not be compiled (%v); refusing because a rule that could not decide must not be read as approval", g.Name, g.Match, err)
		}
		selects := checkrun.Selector(match)
		for _, c := range changes {
			if !changeset.Admits(changeset.DeletionMode(g.Deletions), c.Status) {
				continue
			}
			scope := uncommittedScope(root, c)
			ok, err := changeset.Selects(selects, scope)
			if err != nil {
				return nil, fmt.Sprintf("the file-guard %q could not be evaluated on the uncommitted %s: %v; refusing because a rule that could not decide must not be read as approval", g.Name, c.Path, err)
			}
			if !ok {
				continue
			}
			u := byPath[c.Path]
			if u == nil {
				u = &uncommittedGuarded{Path: c.Path, Status: c.Status}
				byPath[c.Path] = u
			}
			u.Rules = append(u.Rules, g.Qualified())
		}
	}
	owed := make([]uncommittedGuarded, 0, len(byPath))
	for _, u := range byPath {
		owed = append(owed, *u)
	}
	return owed, ""
}

// uncommittedScope is what a rule's match is asked about an uncommitted change:
// the same scope it is asked about a committed one, from the working tree and
// HEAD — no trailers, because nothing has been committed to carry any.
func uncommittedScope(root string, c gitrepo.Uncommitted) changeset.Scope {
	status := string(c.Status)
	oldPath := c.Path
	if c.OldPath != "" {
		oldPath = c.OldPath
	}
	var oldMarkers, markers []changeset.Marker
	if c.Status != 'A' {
		if text, ok := gitrepo.ContentAt(root, "HEAD", oldPath); ok {
			oldMarkers = checkrun.Markers(text)
		}
	}
	if c.Status != 'D' {
		// The engine's one safe read: regular files only once links are followed,
		// capped. A path that is a FIFO, a device or a link to one is still an
		// uncommitted guarded path — it just has no markers to read — and must never
		// block the Stop (opening a FIFO waits for a writer; /dev/zero never ends).
		if text, ok := filemod.ReadRegular(filepath.Join(root, c.Path), filemod.MaxContentReadBytes); ok {
			markers = checkrun.Markers(text)
		}
	} else {
		markers = oldMarkers
	}
	return changeset.Scope{Path: c.Path, OldPath: c.OldPath, Status: status, Markers: markers, OldMarkers: oldMarkers, Trailers: map[string][]string{}}
}

func commitRequiredMessage(owed []uncommittedGuarded) string {
	var b strings.Builder
	b.WriteString("Commit your work before ending this turn. File-guards judge commits, and these guarded paths have uncommitted changes:\n")
	for i, u := range owed {
		if i == maxListedPaths {
			fmt.Fprintf(&b, "  ... and %d more\n", len(owed)-maxListedPaths)
			break
		}
		fmt.Fprintf(&b, "  - %s (%s) — %s\n", u.Path, statusWord(u.Status), strings.Join(u.Rules, ", "))
	}
	b.WriteString("Commit these (git add <paths> && git commit); they are never committed for you. ")
	b.WriteString("Where a change is grounded in what the user asked, put the user's exact words in the commit message as a trailer, `Sloprail-Cites-User: <quote>` (or `Sloprail-Cites-Tool: <quote>` for a tool's output).")
	return b.String()
}

func statusWord(s byte) string {
	switch s {
	case 'A':
		return "new"
	case 'D':
		return "deleted"
	case 'R':
		return "renamed"
	}
	return "modified"
}

// unknownCommitState opens the refusal for a tree or registry that could not be read: work is
// not known to be owed a commit, so the tracked ranges are still verified beside it.
const unknownCommitState = "could not tell whether your work is committed"

// failClosed is the refusal for a git failure: a tree whose status could not be
// read is not a clean one.
func failClosed(err error) string {
	return fmt.Sprintf(unknownCommitState+" (%v); refusing because a state that could not be read must not be read as clean", err)
}

func isNotARepo(err error) bool { return errors.Is(err, gitrepo.ErrNotARepository) }

// ownsTree reports whether this agent is the one whose commits the tree's rules
// judge: the session's root always, and a sub-agent only when its git tree is not
// the root's — a10n's rule. `git rev-parse --show-toplevel` of the sub-agent's cwd
// against that of the directory the session began in: the same top level means the
// sub-agent works in the root's own tree, whose commits the root's Stop judges
// (refusing the sub-agent for them blocked a10n's read-only sub-agent 17 times in a
// row), and a different one means a worktree of its own.
//
// The root's directory is where its record says it began, never where it last
// stood, so a root that has since `cd`'d into a worktree does not change the
// answer. No other record is consulted. Every doubt is a gate: a root directory
// that cannot be determined, or a top level that cannot be read, means the
// sub-agent IS gated — declining to gate on a guess is how a sub-agent's commit
// goes unjudged.
func ownsTree(p HookPayload) bool {
	if !p.IsSubagent() {
		return true
	}
	rootRecord, err := p.sessionRecord()
	if err != nil || rootRecord == "" {
		return true
	}
	rootCwd, err := transcript.StartCwd(rootRecord)
	if err != nil || rootCwd == "" {
		return true
	}
	rootTree, err := gitrepo.Root(rootCwd)
	if err != nil {
		return true
	}
	subTree, err := gitrepo.Root(p.Cwd)
	if err != nil {
		return true
	}
	return !sameDir(rootTree, subTree)
}

// sameDir compares two directories as the filesystem sees them (macOS reports
// /var where the disk holds /private/var).
func sameDir(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return ra == rb
}

// commitRequiredReleased is the loop breaker. It counts this refusal, and reports
// true — after saying so on stderr — once the same set has been refused
// stop_hook_block_cap times in a row.
func commitRequiredReleased(cmd *cobra.Command, p HookPayload, store sessionstate.Store, owed []uncommittedGuarded) bool {
	if store == nil {
		return false
	}
	limit, err := declaration.StopHookBlockCap(dotDir(p.Cwd))
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: stop_hook_block_cap not read, using %d: %v\n", limit, err)
	}
	if limit == 0 {
		return false
	}
	key := setKey(owed)
	count := 0
	if v, ok, _ := store.Meta(sessionstate.MetaCommitRequired); ok {
		if k, n, found := strings.Cut(v, ":"); found && k == key {
			count, _ = strconv.Atoi(n)
		}
	}
	if count >= limit {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"sloprail: commit required was refused %d times in a row for the same uncommitted paths, reaching stop_hook_block_cap (%d) — letting the turn end; the paths are still uncommitted and unjudged\n",
			count, limit)
		resetCommitRequired(cmd, store)
		return true
	}
	if err := store.SetMeta(sessionstate.MetaCommitRequired, key+":"+strconv.Itoa(count+1)); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: commit-required count not recorded:", err)
	}
	return false
}

func resetCommitRequired(cmd *cobra.Command, store sessionstate.Store) {
	if store == nil {
		return
	}
	if err := store.DeleteMeta(sessionstate.MetaCommitRequired); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: commit-required count not reset:", err)
	}
}

// setKey names a set of uncommitted paths and their statuses.
func setKey(owed []uncommittedGuarded) string {
	h := sha256.New()
	for _, u := range owed {
		fmt.Fprintf(h, "%c %s\n", u.Status, u.Path)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// treeKey names a tree by its real path, so one reached by a symlinked spelling is the same tree.
func treeKey(path string) string {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	return filepath.Clean(path)
}

// foldersOfRunningAgents maps the trees (treeKey) among folders that a background sub-agent the
// registry holds as running has worked in to that agent's id. The policy is the tracked ranges':
// only a background agent that settleAgents still waits for counts; an agent the registry does not
// know, a foreground one, a stale one, one whose process is gone is not there, so its folder is
// owed as ever. Only the root's Stop defers (a sub-agent's own Stop never does), and anything that
// cannot be read yields no folder: a doubt is a refusal.
//
// A silent agent not yet stale is waited for, as for ranges. The store attributes by folder, not by path: a folder where the root and a running agent both
// wrote is the agent's, and the root's own tree is never asked about (the agent there shares the
// root's work, which must stay owed).
func foldersOfRunningAgents(cmd *cobra.Command, p HookPayload, folders []sessionstate.Folder) map[string]string {
	busy := map[string]string{}
	if p.AgentID != "" || len(folders) == 0 {
		return busy
	}
	rs, err := resolveRootSession(p)
	if err != nil {
		return busy
	}
	if _, err := os.Stat(rs.Path); err != nil {
		return busy
	}
	root, err := sessionstate.Open(rs.Path)
	if err != nil {
		return busy
	}
	defer root.Close()
	plan := settleAgents(cmd, root, rs.ID, p, agentClock())
	if len(plan.Waiting) == 0 {
		return busy
	}
	agents, err := root.Agents(rs.ID)
	if err != nil {
		return busy
	}
	for _, a := range agents {
		if !plan.Waiting[a.AgentID] || !a.Running() || !a.Background {
			continue
		}
		mark := func(folder string) {
			if _, taken := busy[treeKey(folder)]; !taken {
				busy[treeKey(folder)] = a.AgentID
			}
		}
		for _, f := range a.Folders {
			mark(f)
		}
		for _, r := range a.Ranges {
			mark(r.Folder)
		}
		for _, f := range folders {
			if f.AgentID == a.AgentID {
				mark(f.Path)
			}
		}
	}
	return busy
}

// noteBeingWorkedOn says, without refusing, that a folder's uncommitted work is a running
// sub-agent's: shown to the user when the Stop passes, appended to a refusal that exists anyway.
func noteBeingWorkedOn(cmd *cobra.Command, folder, agent string) {
	line := fmt.Sprintf("uncommitted changes in %s are being worked on by sub-agent %s; not yours to commit until it stops", folder, agent)
	if !addStopNotice(cmd, line) {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: "+line)
	}
}
