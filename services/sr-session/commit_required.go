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
	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/guardrail"
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
func commitRequired(cmd *cobra.Command, p HookPayload, guards []declaration.FileGuard, store sessionstate.Store, context map[string]any) string {
	if len(guards) == 0 || !ownsTree(p) {
		return ""
	}
	root, err := gitrepo.Root(p.Cwd)
	if err != nil {
		if isNotARepo(err) {
			return "" // no repository, so nothing can be committed
		}
		return failClosed(err)
	}
	changes, err := gitrepo.UncommittedChanges(root)
	if err != nil {
		return failClosed(err)
	}
	if len(changes) == 0 {
		resetCommitRequired(cmd, store)
		return ""
	}

	byPath := map[string]*uncommittedGuarded{}
	for _, g := range guards {
		if isLaunchedBy(os.Getenv, g.Name) {
			continue
		}
		match, err := guardrail.CompileFileMatch(g.Match)
		if err != nil {
			return fmt.Sprintf("the file-guard %q could not be evaluated: its match %q could not be compiled (%v); refusing because a rule that could not decide must not be read as approval", g.Name, g.Match, err)
		}
		selects := changesetSelector(match, context)
		for _, c := range changes {
			if !changeset.Admits(changeset.DeletionMode(g.Deletions), c.Status) {
				continue
			}
			scope := uncommittedScope(root, c)
			ok, err := selects(scope)
			if err != nil {
				return fmt.Sprintf("the file-guard %q could not be evaluated on the uncommitted %s: %v; refusing because a rule that could not decide must not be read as approval", g.Name, c.Path, err)
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
	if len(byPath) == 0 {
		resetCommitRequired(cmd, store)
		return ""
	}

	owed := make([]uncommittedGuarded, 0, len(byPath))
	for _, u := range byPath {
		owed = append(owed, *u)
	}
	sort.Slice(owed, func(i, j int) bool { return owed[i].Path < owed[j].Path })

	if commitRequiredReleased(cmd, p, store, owed) {
		return ""
	}
	return commitRequiredMessage(owed)
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
			oldMarkers = changesetMarkers(text)
		}
	}
	if c.Status != 'D' {
		if b, err := os.ReadFile(filepath.Join(root, c.Path)); err == nil {
			markers = changesetMarkers(string(b))
		}
	} else {
		markers = oldMarkers
	}
	return changeset.Scope{Path: c.Path, Status: status, Markers: markers, OldMarkers: oldMarkers, Trailers: map[string][]string{}}
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

// failClosed is the refusal for a git failure: a tree whose status could not be
// read is not a clean one.
func failClosed(err error) string {
	return fmt.Sprintf("could not tell whether your work is committed (%v); refusing because a state that could not be read must not be read as clean", err)
}

func isNotARepo(err error) bool { return errors.Is(err, gitrepo.ErrNotARepository) }

// ownsTree reports whether this agent is the one whose commits the tree's rules
// judge: the session's root always, and a sub-agent only when it works in a tree
// of its own. A sub-agent whose tree is the session's own (the root's record was
// written in it) shares the root's uncommitted work and is not gated for it; one
// whose root record cannot be found is not gated either — declining to gate is
// the safe failure here, since the root's own Stop still refuses uncommitted work.
func ownsTree(p HookPayload) bool {
	if !p.IsSubagent() {
		return true
	}
	record, err := p.record()
	if err != nil || record == "" {
		return false
	}
	sessionDir := transcript.SessionDirOfSubagent(record)
	if sessionDir == "" {
		return false
	}
	rootRecord := filepath.Join(filepath.Dir(sessionDir), filepath.Base(sessionDir)+".jsonl")
	if _, err := os.Stat(rootRecord); err != nil {
		return false
	}
	shared, _ := transcript.BelongsToTree(rootRecord, p.Cwd)
	return !shared
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
