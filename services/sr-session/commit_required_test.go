package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/changeset"
	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/gitrepo"
	"github.com/sloprail/sloprail/internal/sessionstate"
)

// ownsTree is a10n's rule: a sub-agent owns its tree iff its git top level differs
// from the one the session began in.
func TestOwnsTree(t *testing.T) {
	repo := initRepo(t)
	commitFile(t, repo, "a.txt", "one")
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "pkg"), 0o755))
	worktree := filepath.Join(t.TempDir(), "agent-1")
	runGit(t, repo, "worktree", "add", "-q", "--detach", worktree)

	// The root's record: it began in the repo, and later stood in the worktree.
	dir := t.TempDir()
	rootRecord := filepath.Join(dir, "root.jsonl")
	require.NoError(t, os.WriteFile(rootRecord, []byte(
		`{"type":"user","uuid":"u1","parentUuid":null,"cwd":"`+repo+`","message":{"role":"user","content":"hi"}}`+"\n"+
			`{"type":"assistant","uuid":"a1","parentUuid":"u1","cwd":"`+worktree+`","message":{"role":"assistant","content":"x"}}`+"\n"), 0o644))
	noCwd := filepath.Join(dir, "nocwd.jsonl")
	require.NoError(t, os.WriteFile(noCwd, []byte(`{"type":"user","uuid":"u1","parentUuid":null,"message":{"role":"user","content":"hi"}}`+"\n"), 0o644))

	sub := func(record, cwd string) HookPayload {
		return HookPayload{TranscriptPath: record, Cwd: cwd, AgentID: "agent-1"}
	}

	assert.True(t, ownsTree(HookPayload{Cwd: repo}), "the session's root owns its tree")
	assert.False(t, ownsTree(sub(rootRecord, repo)), "a sub-agent in the root's own tree does not")
	assert.False(t, ownsTree(sub(rootRecord, filepath.Join(repo, "pkg"))), "nor one that stands in a subdirectory of it")
	assert.True(t, ownsTree(sub(rootRecord, worktree)), "a sub-agent in a worktree of its own does")

	// The root having cd'd INTO the worktree later changes nothing: the root's tree
	// is where it began.
	assert.True(t, ownsTree(sub(rootRecord, worktree)))
	assert.False(t, ownsTree(sub(rootRecord, repo)))

	// Every doubt is a gate.
	assert.True(t, ownsTree(sub(noCwd, repo)), "a root directory that cannot be determined gates")
	assert.True(t, ownsTree(sub(filepath.Join(dir, "missing.jsonl"), repo)), "a root record that is not there gates")
	assert.True(t, ownsTree(sub("", repo)), "no root record at all gates")
	assert.True(t, ownsTree(sub(rootRecord, t.TempDir())), "a sub-agent tree that is not a repository gates")
}

// An uncommitted guarded path that is a FIFO, a device, or a link to one is read
// the safe way: it is still owed a commit, and reading it must not block.
func TestUncommittedScope_NeverBlocksOnAFifoOrADevice(t *testing.T) {
	repo := initRepo(t)
	commitFile(t, repo, "a.txt", "one")
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "docs"), 0o755))
	fifo := filepath.Join(t.TempDir(), "fifo")
	require.NoError(t, syscall.Mkfifo(fifo, 0o644))
	require.NoError(t, os.Symlink(fifo, filepath.Join(repo, "docs", "to-fifo.md")))
	require.NoError(t, os.Symlink("/dev/zero", filepath.Join(repo, "docs", "to-zero.md")))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "docs", "real.md"), []byte("// sr:invariant a.b\n"), 0o644))

	changes, err := gitrepo.UncommittedChanges(repo)
	require.NoError(t, err)
	require.Len(t, changes, 3)

	done := make(chan map[string]int, 1)
	go func() {
		markers := map[string]int{}
		for _, c := range changes {
			markers[c.Path] = len(uncommittedScope(repo, c).Markers)
		}
		done <- markers
	}()
	select {
	case got := <-done:
		assert.Equal(t, 1, got["docs/real.md"], "a regular file's markers are still read")
		assert.Equal(t, 0, got["docs/to-fifo.md"])
		assert.Equal(t, 0, got["docs/to-zero.md"])
	case <-time.After(20 * time.Second):
		t.Fatal("reading an uncommitted FIFO or device blocked")
	}
}

// --- commitRequired over a real repository and a real store ---

// crRepo is a repository with docs/a.md and notes/n.md committed, and a config that
// caps the loop breaker at `cap` refusals in a row (0 turns it off).
func crRepo(t *testing.T, cap int) string {
	t.Helper()
	repo := initRepo(t)
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "docs"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "notes"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "docs", "a.md"), []byte("a long enough body to be seen as a rename\nsecond line\nthird\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "notes", "n.md"), []byte("a long enough body to be seen as a rename\nsecond line\nthird\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(repo, ".sloprail"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repo, ".sloprail", "config.yaml"), []byte("stop_hook_block_cap: "+strconv.Itoa(cap)+"\n"), 0o644))
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-m", "seed")
	return repo
}

func docsGuard(mutate ...func(*declaration.FileGuard)) []declaration.FileGuard {
	g := declaration.FileGuard{Name: "docs", Match: "docs/**"}
	for _, m := range mutate {
		m(&g)
	}
	return []declaration.FileGuard{g}
}

// capturing is a command whose stderr can be read back.
func capturing() (*cobra.Command, *bytes.Buffer) {
	var buf bytes.Buffer
	c := &cobra.Command{}
	c.SetOut(&buf)
	c.SetErr(&buf)
	return c, &buf
}

func owed(t *testing.T, repo string, guards []declaration.FileGuard, store sessionstate.Store) string {
	t.Helper()
	return commitRequired(discard(), HookPayload{Cwd: repo}, guards, store, map[string]any{})
}

func TestCommitRequired_AnUncommittedGuardedPathIsOwedACommitAndACleanTreeIsNot(t *testing.T) {
	repo := crRepo(t, 0)
	assert.Equal(t, "", owed(t, repo, docsGuard(), openStore(t)), "a clean tree owes nothing")

	require.NoError(t, os.WriteFile(filepath.Join(repo, "docs", "b.md"), []byte("new"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "notes", "scratch.md"), []byte("scratch"), 0o644))
	got := owed(t, repo, docsGuard(), openStore(t))
	assert.Contains(t, got, "Commit your work before ending this turn")
	assert.Contains(t, got, "docs/b.md (new) — file-guard/docs")
	assert.NotContains(t, got, "scratch.md", "a path no rule selects never triggers it")
	assert.Contains(t, got, "Sloprail-Cites-User")
}

func TestCommitRequired_NoGuardsNoRepositoryNoSubagentTreeOwesNothing(t *testing.T) {
	repo := crRepo(t, 0)
	require.NoError(t, os.WriteFile(filepath.Join(repo, "docs", "b.md"), []byte("new"), 0o644))
	assert.Equal(t, "", owed(t, repo, nil, nil), "no file-guards, no gate")
	assert.Equal(t, "", owed(t, t.TempDir(), docsGuard(), nil), "no repository, nothing can be committed")

	// A sub-agent working in the root's own tree leaves its work to the root's Stop.
	record := filepath.Join(t.TempDir(), "root.jsonl")
	require.NoError(t, os.WriteFile(record, []byte(`{"type":"user","uuid":"u1","parentUuid":null,"cwd":"`+repo+`","message":{"role":"user","content":"hi"}}`+"\n"), 0o644))
	sub := HookPayload{TranscriptPath: record, Cwd: repo, AgentID: "agent-1"}
	assert.Equal(t, "", commitRequired(discard(), sub, docsGuard(), nil, map[string]any{}))
}

func TestCommitRequired_AMatchThatCannotBeCompiledRefusesRatherThanPassing(t *testing.T) {
	repo := crRepo(t, 0)
	require.NoError(t, os.WriteFile(filepath.Join(repo, "docs", "b.md"), []byte("new"), 0o644))
	got := owed(t, repo, docsGuard(func(g *declaration.FileGuard) { g.Match = `path ==` }), nil)
	assert.Contains(t, got, "could not be compiled")
	assert.Contains(t, got, "must not be read as approval")
}

func TestCommitRequired_ADeletionIsOwedOnlyWhenTheRuleAdmitsDeletions(t *testing.T) {
	repo := crRepo(t, 0)
	require.NoError(t, os.Remove(filepath.Join(repo, "docs", "a.md")))
	assert.Equal(t, "", owed(t, repo, docsGuard(), nil), "deletions: skip")
	got := owed(t, repo, docsGuard(func(g *declaration.FileGuard) { g.Deletions = declaration.DeletionsInclude }), nil)
	assert.Contains(t, got, "docs/a.md (deleted)")
}

// A rename out of a guarded path is a change to the guarded file, under `skip` too
// (a rename is not a deletion); a rename between unguarded paths is nobody's.
func TestCommitRequired_ARenameOutOfAGuardedPathIsOwedACommit(t *testing.T) {
	repo := crRepo(t, 0)
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "archive"), 0o755))
	runGit(t, repo, "mv", "docs/a.md", "archive/a.md")
	got := owed(t, repo, docsGuard(), nil)
	assert.Contains(t, got, "archive/a.md (renamed) — file-guard/docs")

	assert.Equal(t, "", owed(t, repo, docsGuard(func(g *declaration.FileGuard) { g.Deletions = declaration.DeletionsOnly }), nil),
		"`deletions: only` is about deleted files, and a rename is not one")

	runGit(t, repo, "commit", "-q", "-m", "archive it")
	runGit(t, repo, "mv", "notes/n.md", "notes/m.md")
	assert.Equal(t, "", owed(t, repo, docsGuard(), nil), "a rename between unguarded paths owes nothing")
}

func TestUncommittedScope_ARenameKeepsItsOldPathAndMarkersOfBothSides(t *testing.T) {
	repo := crRepo(t, 0)
	require.NoError(t, os.WriteFile(filepath.Join(repo, "docs", "a.md"), []byte("// sr:invariant a.b\nbody line one\nbody line two\nthree\n"), 0o644))
	runGit(t, repo, "commit", "-am", "marker")
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "archive"), 0o755))
	runGit(t, repo, "mv", "docs/a.md", "archive/a.md")

	changes, err := gitrepo.UncommittedChanges(repo)
	require.NoError(t, err)
	var scope changeset.Scope
	for _, c := range changes {
		if c.Path == "archive/a.md" {
			scope = uncommittedScope(repo, c)
		}
	}
	assert.Equal(t, "R", scope.Status)
	assert.Equal(t, "docs/a.md", scope.OldPath)
	require.Len(t, scope.OldMarkers, 1, "the markers HEAD's old path carried")
	require.Len(t, scope.Markers, 1, "and the ones the working tree's new path carries")
}

// An untracked nested repository (a clone the agent made to look at) is not this
// repository's to commit, however guarded the path it sits under.
func TestCommitRequired_AnUntrackedNestedRepositoryIsNotOwed(t *testing.T) {
	repo := crRepo(t, 0)
	nested := filepath.Join(repo, "docs", "clone")
	require.NoError(t, os.MkdirAll(nested, 0o755))
	runGit(t, nested, "init", "--initial-branch=main")
	require.NoError(t, os.WriteFile(filepath.Join(nested, "x.md"), []byte("theirs"), 0o644))
	assert.Equal(t, "", owed(t, repo, docsGuard(), nil))
}

// A guarded path that is a FIFO or a link to one is still owed a commit, and
// deciding that must not block on it.
func TestCommitRequired_AFifoOnAGuardedPathIsOwedAndNeverBlocks(t *testing.T) {
	repo := crRepo(t, 0)
	fifo := filepath.Join(t.TempDir(), "fifo")
	require.NoError(t, syscall.Mkfifo(fifo, 0o644))
	require.NoError(t, os.Symlink(fifo, filepath.Join(repo, "docs", "to-fifo.md")))

	done := make(chan string, 1)
	go func() { done <- owed(t, repo, docsGuard(), nil) }()
	select {
	case got := <-done:
		assert.Contains(t, got, "docs/to-fifo.md")
	case <-time.After(20 * time.Second):
		t.Fatal("commit-required blocked on a FIFO")
	}
}

func TestCommitRequired_ARuleLaunchedByItsOwnCheckIsNotEnforced(t *testing.T) {
	repo := crRepo(t, 0)
	require.NoError(t, os.WriteFile(filepath.Join(repo, "docs", "b.md"), []byte("new"), 0o644))
	t.Setenv(LaunchedByEnv, "docs")
	assert.Equal(t, "", owed(t, repo, docsGuard(), nil))
}

func TestCommitRequired_ARefusalNamesAtMostTwentyPathsAndCountsTheRest(t *testing.T) {
	repo := crRepo(t, 0)
	for i := 0; i < 23; i++ {
		require.NoError(t, os.WriteFile(filepath.Join(repo, "docs", "f"+strconv.Itoa(100+i)+".md"), []byte("x"), 0o644))
	}
	got := owed(t, repo, docsGuard(), nil)
	assert.Equal(t, 20, strings.Count(got, "— file-guard/docs"))
	assert.Contains(t, got, "... and 3 more")
}

// The loop breaker: after stop_hook_block_cap refusals in a row for the SAME set the
// gate says so on stderr and lets the turn end; changing the set starts the count
// again, and so does a clean tree.
func TestCommitRequired_TheLoopBreakerReleasesAfterTheCapForTheSameSet(t *testing.T) {
	repo := crRepo(t, 2)
	store := openStore(t)
	require.NoError(t, os.WriteFile(filepath.Join(repo, "docs", "b.md"), []byte("new"), 0o644))
	cmd, stderr := capturing()
	p := HookPayload{Cwd: repo}
	ask := func() string { return commitRequired(cmd, p, docsGuard(), store, map[string]any{}) }

	assert.NotEmpty(t, ask(), "refusal 1")
	assert.NotEmpty(t, ask(), "refusal 2")
	assert.Empty(t, stderr.String())
	assert.Equal(t, "", ask(), "the cap was reached: the turn is let end")
	assert.Contains(t, stderr.String(), "reaching stop_hook_block_cap (2)")
	assert.Contains(t, stderr.String(), "still uncommitted and unjudged")

	assert.NotEmpty(t, ask(), "and the count starts again")

	// A different set is a different count: one more path, fresh.
	require.NoError(t, os.WriteFile(filepath.Join(repo, "docs", "c.md"), []byte("new"), 0o644))
	assert.NotEmpty(t, ask())
	assert.NotEmpty(t, ask())
	assert.Equal(t, "", ask())
}

func TestCommitRequired_ACleanTreeResetsTheLoopBreaker(t *testing.T) {
	repo := crRepo(t, 2)
	store := openStore(t)
	require.NoError(t, os.WriteFile(filepath.Join(repo, "docs", "b.md"), []byte("new"), 0o644))
	assert.NotEmpty(t, owed(t, repo, docsGuard(), store))
	assert.NotEmpty(t, owed(t, repo, docsGuard(), store))

	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-m", "commit it")
	assert.Equal(t, "", owed(t, repo, docsGuard(), store))
	_, present, err := store.Meta(sessionstate.MetaCommitRequired)
	require.NoError(t, err)
	assert.False(t, present, "committing clears the count")
}

func TestCommitRequired_ACapOfZeroNeverReleases(t *testing.T) {
	repo := crRepo(t, 0)
	store := openStore(t)
	require.NoError(t, os.WriteFile(filepath.Join(repo, "docs", "b.md"), []byte("new"), 0o644))
	for i := 0; i < 6; i++ {
		assert.NotEmpty(t, owed(t, repo, docsGuard(), store), "refusal %d", i+1)
	}
}

func TestSetKey_NamesTheSetAndItsStatusesNotTheirOrderOfDiscovery(t *testing.T) {
	a := []uncommittedGuarded{{Path: "a", Status: 'A'}, {Path: "b", Status: 'M'}}
	assert.Equal(t, setKey(a), setKey([]uncommittedGuarded{{Path: "a", Status: 'A', Rules: []string{"x"}}, {Path: "b", Status: 'M'}}), "the rules are not part of it")
	assert.NotEqual(t, setKey(a), setKey([]uncommittedGuarded{{Path: "a", Status: 'M'}, {Path: "b", Status: 'M'}}), "a status change is a new set")
	assert.NotEqual(t, setKey(a), setKey(a[:1]), "committing part of the work is a new set")
}

func TestStatusWord(t *testing.T) {
	assert.Equal(t, "new", statusWord('A'))
	assert.Equal(t, "deleted", statusWord('D'))
	assert.Equal(t, "renamed", statusWord('R'))
	assert.Equal(t, "modified", statusWord('M'))
}

func TestFailClosed_SaysTheStateCouldNotBeRead(t *testing.T) {
	assert.Contains(t, failClosed(errors.New("boom")), "must not be read as clean")
	assert.True(t, isNotARepo(gitrepo.ErrNotARepository))
	assert.False(t, isNotARepo(errors.New("boom")))
}
