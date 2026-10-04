package checkrun

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/changeset"
	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/gitrepo"
)

// manyRules adds n more committed rules to the fixture, all matching docs/**, and returns
// every rule (the fixture's own first). The commit that adds them is the fixture's new base.
func (f *evalFixture) manyRules(t *testing.T, n int) []declaration.FileGuard {
	t.Helper()
	guards := []declaration.FileGuard{f.guard}
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("docs%02d", i)
		dir := filepath.Join(f.repo, ".sloprail", "file-guard", name)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "check.sh"), []byte(countingCheck(f.ledger)), 0o755))
		guards = append(guards, declaration.FileGuard{Name: name, Match: "docs/**", Dir: dir, Checks: []declaration.Check{{Script: "./check.sh"}}})
	}
	runGit(t, f.repo, "add", "-A")
	runGit(t, f.repo, "commit", "-m", "more rules")
	f.base = runGit(t, f.repo, "rev-parse", "HEAD")
	return guards
}

func (f *evalFixture) hashOf(t *testing.T, g declaration.FileGuard) string {
	t.Helper()
	h, err := changeset.RuleHashAt(f.repo, g.Dir, false)
	require.NoError(t, err)
	return h
}

// judgeOver judges the given rules over base..head (a run; verify when verify is set).
func (f *evalFixture) judgeOver(t *testing.T, guards []declaration.FileGuard, base, head string, verify bool) []FileGuardResult {
	t.Helper()
	p := f.params(t, f.results)
	rng, err := gitrepo.ResolveRange(f.repo, base, head)
	require.NoError(t, err)
	p.Guards, p.Range, p.Verify = guards, rng, verify
	got, _ := Evaluate(p)
	return got
}

func (f *evalFixture) rangeTo(t *testing.T, base, head string) gitrepo.Range {
	t.Helper()
	rng, err := gitrepo.ResolveRange(f.repo, base, head)
	require.NoError(t, err)
	return rng
}

// One evaluation answers every rule's effective base from one shared ancestry memo. Each rule
// must get exactly the base it would get from an evaluation of its own (no sharing at all):
// B1..H1 then H1..H2 reach H2; a rule only ever judged over a narrow range stays at B1.
func TestAncestryMemo_SharedEvaluationAgreesWithOneEvaluationPerRule(t *testing.T) {
	f := newEvalFixture(t, nil).withSession(t)
	guards := f.manyRules(t, 6)
	narrow := guards[len(guards)-1]
	wide := guards[:len(guards)-1]

	c1 := f.commitDoc(t, "docs/a.md", "clean a")
	c2 := f.commitDoc(t, "docs/b.md", "clean b")
	c3 := f.commitDoc(t, "docs/c.md", "clean c")

	require.Empty(t, f.judgeOver(t, wide, f.base, c1, false))
	require.Empty(t, f.judgeOver(t, wide, c1, c2, false))
	require.Empty(t, f.judgeOver(t, []declaration.FileGuard{narrow}, c1, c2, false), "a pass over c1..c2 only")

	shared := f.newEvaluation(t, f.results)
	rng := f.rangeTo(t, f.base, c3)
	for _, g := range guards {
		alone := f.newEvaluation(t, f.results).effectiveBase(g, f.hashOf(t, g), rng)
		got := shared.effectiveBase(g, f.hashOf(t, g), rng)
		assert.Equal(t, alone, got, g.Name)
		if g.Name == narrow.Name {
			assert.Equal(t, f.base, got.Base, "the narrow pass leaves base..c1 unjudged: nothing advances")
		} else {
			assert.Equal(t, c2, got.Base, "%s: B1..H1 then H1..H2 reach H2", g.Name)
		}
	}
}

// The memo is keyed by the PAIR: (a, b) and (a, c) are different questions, and (a, b) and
// (b, a) too. A branch that is not a descendant of the earlier passes gets no advance, whichever
// range was asked first.
func TestAncestryMemo_AnUnrelatedBranchDoesNotInheritAnotherRangesAnswer(t *testing.T) {
	f := newEvalFixture(t, nil).withSession(t)
	guards := f.manyRules(t, 3)
	c1 := f.commitDoc(t, "docs/a.md", "clean a")
	c2 := f.commitDoc(t, "docs/b.md", "clean b")
	require.Empty(t, f.judgeOver(t, guards, f.base, c1, false))
	require.Empty(t, f.judgeOver(t, guards, c1, c2, false))

	runGit(t, f.repo, "checkout", "-q", "-b", "other", f.base)
	z := f.commitDoc(t, "docs/z.md", "clean z")
	runGit(t, f.repo, "checkout", "-q", "main")

	main, other := f.rangeTo(t, f.base, c2), f.rangeTo(t, f.base, z)
	for _, order := range [][2]gitrepo.Range{{main, other}, {other, main}} {
		ev := f.newEvaluation(t, f.results) // one evaluation, both ranges, both orders
		for _, g := range guards {
			for _, rng := range order {
				got := ev.effectiveBase(g, f.hashOf(t, g), rng)
				if rng.Head == z {
					assert.Equal(t, f.base, got.Base, "%s: c1 and c2 are not ancestors of z", g.Name)
				} else {
					assert.Equal(t, c2, got.Base, g.Name)
				}
			}
		}
	}

	ev := f.newEvaluation(t, f.results)
	assert.True(t, ev.isAncestor(f.base, c2))
	assert.False(t, ev.isAncestor(c2, f.base), "(a, b) and (b, a) are different questions")
	assert.True(t, ev.isAncestor(c1, c2))
	assert.False(t, ev.isAncestor(c1, z), "(c1, c2) true must not answer (c1, z)")
	assert.True(t, ev.isAncestor(c1, c2), "and still true after")
	assert.True(t, ev.isAncestor(gitrepo.EmptyTree, z))
	assert.False(t, ev.isAncestor(c1, "0000000000000000000000000000000000000000"), "an unknown commit is no ancestry")
}

// Many rules judged by one evaluation at concurrency > 1: the ancestry memo is hit from the
// pool's goroutines at once (run under -race). Run, then verify, both over the original range.
func TestAncestryMemo_ManyRulesInOneEvaluationUnderConcurrency(t *testing.T) {
	t.Setenv(StopConcurrencyEnv, "8")
	f := newEvalFixture(t, nil).withSession(t)
	guards := f.manyRules(t, 31)
	c1 := f.commitDoc(t, "docs/a.md", "clean a")
	c2 := f.commitDoc(t, "docs/b.md", "clean b")
	require.Empty(t, f.judgeOver(t, guards, f.base, c1, false))
	require.Empty(t, f.judgeOver(t, guards, c1, c2, false))
	ran := f.runs(t)
	assert.Equal(t, 2*len(guards), ran)

	c3 := f.commitDoc(t, "docs/c.md", "clean c")
	require.Empty(t, f.judgeOver(t, guards, f.base, c3, false), "one run over the wider range")
	assert.Equal(t, ran+len(guards), f.runs(t), "each rule judged only c2..c3: one check each")
	assert.Empty(t, f.judgeOver(t, guards, f.base, c3, true), "and verify finds every rule's passes")
	assert.Equal(t, ran+len(guards), f.runs(t), "verify ran nothing")

	f.commitDoc(t, "docs/d.md", "FORBIDDEN")
	got := f.judgeOver(t, guards, f.base, "HEAD", false)
	assert.Len(t, got, len(guards), "every rule refuses the forbidden file")
}
