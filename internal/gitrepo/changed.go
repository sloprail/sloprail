package gitrepo

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Change is one path that differs from the point a cycle measures from.
//
// Two fields rather than a status letter, because the status letter is git's
// vocabulary and the events are the engine's. What the rest of the engine needs
// from git is the one fact it cannot recover by looking at the tree — whether
// the path was there at the baseline — and git's A/M/D/R alphabet says that
// among other things it does not need.
type Change struct {
	// Path is relative to the REPOSITORY ROOT — not to the directory Changed
	// was asked about — in git's own spelling, which is already clean: no
	// "./", no "..", no absolute paths, forward slashes throughout.
	//
	// The two are the same only when the caller asks from the root, and the
	// distinction is the whole of F1: the two commands behind this answer
	// disagree about it by default, and the union of their answers used to
	// carry both conventions at once. Root() names what these resolve against.
	Path string

	// ExistedAtBaseline reports whether the path was present in the baseline
	// commit.
	//
	// This is the half of a classification the working tree cannot answer. Now
	// is a stat away; before is a fact about a commit the tree may since have
	// left behind.
	ExistedAtBaseline bool
}

// Root reports the repository root containing dir.
//
// Exists because Change.Path is relative to the ROOT while a caller holds the
// directory a hook was invoked in, and those are the same only when the hook
// happened to be invoked at the top of the tree. A consumer resolving a change
// against the wrong one stats a path that is not there and concludes the file
// was deleted — which is F1, arrived at from the consumer's side rather than
// the producer's.
//
// Asked of git rather than walked for a .git entry, because git already answers
// this and its answer accounts for what a hand-rolled walk would not: a linked
// worktree, whose .git is a file, and a repository reached through a symlink.
func Root(dir string) (string, error) {
	out, err := run(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// Changed reports every path in dir that differs from commit — work that has
// been committed since, work that is staged, work that is merely written, and
// work that was never added to git at all.
//
// All four, because a cycle's difference covers committed and uncommitted work
// alike. An agent that commits mid-cycle leaves a tree with nothing outstanding
// in it, and a comparison that only read what is outstanding would report that
// the cycle changed nothing.
//
// Two questions to git rather than one, because no single git command answers
// both halves. `git diff <commit>` compares the tree against the commit and
// covers everything git is tracking, committed or not — but a file the agent
// created and never staged is not tracked, so it appears in no diff at all.
// That file is a change the baseline does not have, and it is precisely what an
// agent writing scratch output produces, so it is asked for separately.
//
// What the two questions together are blind to, stated so it is a decision:
//
// A file created, staged, and then deleted before the cycle ends is reported as
// no change at all. The diff sees no difference (the path is in neither the
// worktree nor the baseline commit) and the untracked listing skips it (the
// path is tracked, having been added). That is the honest answer for what this
// reports — the tree ends the cycle exactly as the baseline had it, and there
// is no content left to judge — but it does mean the intermediate state is
// invisible. Reading the index as a third question would surface it and would
// also start reporting paths that are not in the tree the rules are judging.
// See TestChanged_StagedThenDeletedIsReportedAsNoChange.
//
// A submodule is reported as ONE path — the gitlink — and never as the files
// inside it. Adding one reports .gitmodules and the submodule's own path;
// changing a file INSIDE the submodule reports nothing, because the parent
// repository's diff only notices a submodule when the commit it points at
// moves. The gitlink is then handed to file rules as though it were a file,
// which is what a rule bound to a path pattern will match on. See
// TestChanged_SubmoduleIsAGitlinkNotItsContents.
func Changed(dir, commit string) ([]Change, error) {
	if commit == "" {
		// Nothing to measure from. Not an error: a repository with no commit
		// yet is an ordinary state, and the caller has already been told the
		// baseline is unavailable.
		return nil, nil
	}

	// The changes are taken even when the diff could not classify everything in
	// it, and the problem is carried out alongside them.
	//
	// An unreadable STATUS is not an unreadable diff: the paths git did classify
	// are still real changes, and dropping ninety-nine of them because a
	// hundredth used a letter this does not know is the silence the engine
	// exists to prevent. What the caller must not do is mistake the partial
	// answer for a complete one, which is what the error is for.
	tracked, unclassified := diffAgainst(dir, commit)
	if tracked == nil && unclassified != nil {
		// Nothing came back at all, so this is git failing rather than one
		// status being unreadable.
		return nil, unclassified
	}
	untracked, err := untrackedPaths(dir)
	if err != nil {
		return nil, err
	}

	// Keyed by path so that the two questions cannot report one file twice.
	// They overlap in a real case: a tracked file deleted from the index and
	// then rewritten on disk is reported by the diff as a deletion and by the
	// untracked listing as a new file. The diff's answer is the one that knows
	// about the baseline, so it wins — hence untracked is folded in second and
	// only where the diff said nothing.
	byPath := make(map[string]bool, len(tracked)+len(untracked))
	for path, existed := range tracked {
		byPath[path] = existed
	}
	for _, path := range untracked {
		if _, seen := byPath[path]; seen {
			continue
		}
		// Untracked means in no commit, so it was not in the baseline commit
		// either. What this cannot distinguish is a file the agent created from
		// one that was already lying in the tree when the session began: git
		// holds no record of either, so neither the baseline commit nor any
		// reflog can separate them. Reported as a create, which errs towards
		// showing a rule a file it may already have seen rather than towards
		// hiding one the agent just wrote.
		byPath[path] = false
	}

	changes := make([]Change, 0, len(byPath))
	for path, existed := range byPath {
		changes = append(changes, Change{Path: path, ExistedAtBaseline: existed})
	}
	// Sorted so that a cycle dispatches its events in the same order twice over
	// the same tree. Map iteration would not, and a hook that writes a ledger
	// would then have a different one to assert against on every run.
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes, unclassified
}

// diffAgainst asks git what differs between the working tree and a commit,
// tracked files only.
//
// `--name-status` because the status is what carries the baseline half of the
// answer: whether a path was there before is exactly what A, M and D encode,
// and re-deriving it by reading the commit's tree separately would be a second
// question that could disagree with the first.
//
// `-z` because a path is bytes, not a line. Git escapes and quotes any path
// holding a newline, a quote or a non-ASCII byte in its default output, so a
// line-oriented reader either mis-splits "a\nb.md" into two entries or reads
// the literal quotes as part of the name. Under -z the paths are emitted raw
// and NUL-terminated, and nothing needs unquoting.
//
// `-M` asks for rename detection explicitly. It is NOT what makes the rename
// case work: git's own diff.renames default is on, so this flag changes nothing
// about the output today, and a test asserting the rename split passes with it
// removed. It is here so the behaviour does not depend on a configurable
// default — `diff.renames=false` in a user's config, or a future change to
// git's default, would otherwise turn one R entry into a separate D and A
// silently. Both shapes are handled below, so either way the events are right;
// the flag only fixes which shape arrives.
func diffAgainst(dir, commit string) (map[string]bool, error) {
	out, err := run(dir, "diff", "--name-status", "-z", "-M", commit)
	if err != nil {
		return nil, err
	}
	// Returned as-is, partial map and problem together: an unreadable status is
	// a fact about one path, not about the diff.
	return parseNameStatus(out)
}

// parseNameStatus reads `git diff --name-status -z` output.
//
// The format is a flat sequence of NUL-terminated fields, NOT one record per
// NUL: a status is one field and each path it governs is another. Most statuses
// take one path, and the rename and copy statuses take two — the source and the
// destination — which is why this is a cursor over fields rather than a loop
// over pairs. Read as pairs, a single rename shifts every subsequent entry by
// one field and the whole remainder of the diff is misread as paths standing in
// for statuses.
func parseNameStatus(out string) (map[string]bool, error) {
	fields := strings.Split(out, "\x00")
	// Every field is NUL-TERMINATED rather than NUL-separated, so splitting
	// leaves one empty field after the last record. Git emits nothing at all
	// for an empty diff, so this also covers the no-changes case — and it is
	// dropped wherever it appears rather than only at the end, because an empty
	// field is never a status and never a path git tracks.
	kept := fields[:0]
	for _, f := range fields {
		if f != "" {
			kept = append(kept, f)
		}
	}
	fields = kept

	changed := make(map[string]bool)
	var problems []error
	for i := 0; i < len(fields); {
		status := fields[i]
		i++

		// The letter is the status; a rename and a copy carry a similarity
		// score after it (R100, C75), so only the first byte is the verb.
		verb := status[0]
		want := 1
		if verb == 'R' || verb == 'C' {
			want = 2
		}
		if i+want > len(fields) {
			return nil, fmt.Errorf("gitrepo: status %q names %d path(s) but the diff output ended", status, want)
		}
		paths := fields[i : i+want]
		i += want

		switch verb {
		case 'A':
			// Added: not in the commit, in the tree now.
			changed[paths[0]] = false
		case 'D':
			// Deleted: in the commit, gone from the tree now.
			changed[paths[0]] = true
		case 'M', 'T':
			// Modified, or the type changed (a file became a symlink). Present
			// on both sides either way.
			changed[paths[0]] = true
		case 'R':
			// A rename is a delete and a create, and git reports it as ONE
			// entry naming both paths. Split back into the two events it
			// actually is: the old path was there and is gone, the new one was
			// not in the commit. Reported as a single "renamed" event instead,
			// every rule bound to creation or deletion would miss a file
			// arriving and a file leaving.
			changed[paths[0]] = true  // source: existed, now gone
			changed[paths[1]] = false // destination: new
		case 'C':
			// A copy leaves the source untouched, so only the destination is a
			// difference. Naming the source as well would report a file that
			// has not changed, which untouched_stays_silent forbids.
			changed[paths[1]] = false
		case 'U':
			// Unmerged: the path is in a conflicted state mid-merge. Both sides
			// have it in some form, so it is an update rather than either
			// extreme.
			//
			// Defensive, and knowingly unreachable from Changed's own call.
			// `git diff --name-status <commit>` compares the tree against a
			// commit and reports a conflicted file as M, never U; the U status
			// belongs to --cached and to worktree-vs-index comparisons. Kept
			// rather than deleted because the alternative for an unknown status
			// is to drop the path, and a conflicted file dropped is a file
			// changed and never judged. Costing one arm to not depend on that
			// reading of git's alphabet is the cheaper side of the trade.
			changed[paths[0]] = true
		default:
			// The path is UNCLASSIFIABLE, so it is dropped — but the rest of
			// the diff is kept and the caller is told.
			//
			// This used to fail the whole parse, which made one unrecognised
			// letter cost the entire cycle: the caller turns an error here into
			// zero events, so a single status nobody had seen before silently
			// disabled every file rule for that turn. That is the exact silence
			// this engine exists to prevent, and it is worse than the unknown
			// path itself — one file nobody could classify becomes every file
			// nobody judged.
			//
			// Collected rather than returned at once, so that a second unknown
			// status does not hide the first.
			problems = append(problems, fmt.Errorf("gitrepo: unrecognised diff status %q for %q", status, paths[0]))
		}
	}
	return changed, errors.Join(problems...)
}

// untrackedPaths lists files git is not tracking at all.
//
// `--exclude-standard` so that .gitignore is honoured. A project ignores build
// output, dependency trees and editor droppings precisely because they are not
// its work, and reporting them would put node_modules in front of every
// guardrail on the first cycle a dependency is installed — the flood
// untouched_stays_silent exists to prevent, arriving through a different door.
//
// Directories are not listed as such: without --directory git names each file
// inside an untracked directory individually, which is what the events want
// since they are about files.
//
// `--full-name` is what makes this answer the same QUESTION as the diff.
// `ls-files` names paths relative to the CURRENT DIRECTORY, while `git diff
// --name-status` names them from the repository root wherever it is run — so
// without this flag the union of the two carries two path conventions at once,
// and they differ exactly when the cwd is not the root.
//
// That is not a cosmetic difference. The consumer joins these paths onto the
// repository root to decide whether a file exists NOW, so a cwd-relative path
// does not resolve, reads as absent, and a file that was at the baseline and is
// still on disk classifies as a DELETE — every modified file outside the
// invocation subdirectory dispatched as a deletion of a file that is still
// there. See TestChanged_FromASubdirectoryReportsRepositoryRelativePaths.
func untrackedPaths(dir string) ([]string, error) {
	out, err := run(dir, "ls-files", "-z", "--others", "--exclude-standard", "--full-name")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths, nil
}
