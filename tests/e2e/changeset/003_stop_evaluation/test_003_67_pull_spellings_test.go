package e2e

import (
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T003_67: bringing the remote's commits in is not the session's work, however it is spelled:
// a merge pull (a merge commit whose second parent is upstream's), a fast-forward-only pull, a
// plain `git merge --ff-only`, a rebase. Upstream's violating commit is never refused; the
// session's own commit made beside it still is.
//
// A fast-forward brings nothing but upstream's commits, and tells them from a commit the session
// made and pushed in the same command only by their having been on the remote at the PREVIOUS
// observation: so a fast-forward spelling fetches in an earlier command (a one-command
// `git pull --ff-only` is tracked, the over-tracking the rule accepts).
func TestT003_67_UpstreamCommitsBroughtInAnyWayAreNotTheSessionsWork(t *testing.T) {
	spellings := []struct {
		name    string
		pull    func(main string) string
		own     bool // the session has a commit of its own beside upstream's (so the pull merges)
		fetched bool // upstream was fetched by an earlier command (a fast-forward is told from a push that way)
	}{
		{"merge pull", func(main string) string { return "git pull -q --no-rebase --no-edit origin " + main }, true, false},
		{"rebase pull", func(main string) string { return "git pull -q --rebase origin " + main }, true, false},
		{"fast-forward only pull", func(main string) string { return "git pull -q --ff-only origin " + main }, false, true},
		{"fetch and merge --ff-only", func(main string) string { return "git merge -q --ff-only origin/" + main }, false, true},
		{"fetch and rebase", func(main string) string { return "git fetch -q origin && git rebase -q origin/" + main }, true, false},
	}
	for i, sp := range spellings {
		t.Run(sp.name, func(t *testing.T) {
			e, proj, led := project(t, docsRule)
			land := remoteWithUpstream(t, e, proj)
			main := e.Git(proj, "branch", "--show-current")
			land("docs/upstream.md", "FORBIDDEN but someone else's and reviewed", "upstream edits docs")
			sess := "s-003-67-" + string(rune('a'+i))

			var turns []harness.Turn
			if sp.own {
				turns = append(turns, harness.CommitFile("c1", "docs/own.md", "clean words", "add own"))
			}
			if sp.fetched {
				turns = append(turns, Bash("p0", "git fetch -q origin"))
			}
			turns = append(turns, Bash("p1", sp.pull(main)))
			e.Run(proj, sess, "bring upstream in", Turns("done", turns...))
			if got := stopRefusals(e, proj, sess); got != "" {
				t.Fatalf("a commit only brought in from upstream was judged:\n%s", got)
			}
			for _, run := range ledger(t, led) {
				if strings.Contains(strings.Join(paths(run.Files), " "), "upstream.md") {
					t.Fatalf("upstream's file was handed to the rule: %v", paths(run.Files))
				}
			}
			blocks := stopBlocks(e, proj, sess)

			e.Run(proj, sess, "own violation", Turns("done",
				harness.CommitFile("c2", "docs/own-bad.md", "FORBIDDEN words", "add own bad"),
			))
			got := newBlocks(e, proj, sess, blocks)
			if !strings.Contains(got, refusalText) || !strings.Contains(got, "docs/own-bad.md") {
				t.Fatalf("the session's own violation was not refused:\n%s", got)
			}
			if strings.Contains(got, "upstream.md") {
				t.Fatalf("the refusal names upstream's file:\n%s", got)
			}
		})
	}
}

// T003_67 (one-command spellings): a fast-forward done in the SAME command that fetches
// (`git pull --ff-only`, `git fetch && git merge --ff-only`) brings in only upstream's commits,
// and the range base is always the merge base with the remote default branch, so the range is
// empty: no refusal, no judge for upstream's commit.
func TestT003_67_OneCommandFastForwardPullIsNotTheSessionsWork(t *testing.T) {
	spellings := []struct {
		name string
		pull func(main string) string
	}{
		{"pull --ff-only", func(main string) string { return "git pull -q --ff-only origin " + main }},
		{"fetch && merge --ff-only", func(main string) string {
			return "git fetch -q origin && git merge -q --ff-only origin/" + main
		}},
	}
	for i, sp := range spellings {
		t.Run(sp.name, func(t *testing.T) {
			e, proj, led := project(t, docsRule)
			land := remoteWithUpstream(t, e, proj)
			main := e.Git(proj, "branch", "--show-current")
			land("docs/upstream.md", "FORBIDDEN but someone else's and reviewed", "upstream edits docs")
			sess := "s-003-67-one-" + string(rune('a'+i))

			e.Run(proj, sess, "bring upstream in", Turns("done", Bash("p1", sp.pull(main))))
			got := stopRefusals(e, proj, sess)
			if got != "" {
				t.Fatalf("a one-command fast-forward pull of upstream's commit was judged:\n%s", got)
			}
			for _, run := range ledger(t, led) {
				if strings.Contains(strings.Join(paths(run.Files), " "), "upstream.md") {
					t.Fatalf("no judge may be asked for upstream's commit: %v", paths(run.Files))
				}
			}
		})
	}
}
