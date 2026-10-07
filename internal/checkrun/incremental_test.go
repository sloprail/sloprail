package checkrun

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/gitrepo"
)

func (f *evalFixture) baseAt(t *testing.T) string {
	t.Helper()
	rng, err := gitrepo.ResolveRange(f.repo, f.base, "HEAD")
	require.NoError(t, err)
	return f.newEvaluation(t, f.results).effectiveBase(f.guard, rng).Base
}

// The effective base is the head of the latest complete PASSING run: a fail never advances
// it, a pass does, and a stored fail is judged again by the next run (only a pass is a hit).
// sr:proves fileguard/passes-not-re-examined
func TestIncremental_EffectiveBaseAdvancesOnlyOnPass(t *testing.T) {
	f := newEvalFixture(t, nil).withSession(t)
	assert.Equal(t, f.base, f.baseAt(t), "no run yet: the requested base")

	f.commitDoc(t, "docs/a.md", "FORBIDDEN")
	r, refused := f.evaluate(t, f.results)
	require.True(t, refused)
	assert.Contains(t, r.Reason, "forbidden words")
	require.Equal(t, 1, f.runs(t))
	assert.Equal(t, f.base, f.baseAt(t), "a FAIL never advances the base")

	_, refused = f.evaluate(t, f.results)
	require.True(t, refused, "still refused")
	assert.Equal(t, 2, f.runs(t), "only a pass is a hit")

	fixed := f.commitDoc(t, "docs/a.md", "clean now")
	_, refused = f.evaluate(t, f.results)
	require.False(t, refused)
	require.Equal(t, 3, f.runs(t))
	assert.Equal(t, fixed, f.baseAt(t), "a pass advances the base to its head")

	// Only what changed since is examined: a new file is judged alone, the old one is not
	// looked at again.
	f.commitDoc(t, "docs/b.md", "clean b")
	_, refused = f.evaluate(t, f.results)
	require.False(t, refused)
	assert.Equal(t, 4, f.runs(t))
	_, outcomes := f.evaluateOver(t, f.base, "HEAD", true, false)
	for _, o := range outcomes {
		assert.NotContains(t, o.Reason, "docs/a.md")
	}
}

// evaluateOver runs (or, verify, only reads) the rule over base..head.
func (f *evalFixture) evaluateOver(t *testing.T, base, head string, verify, whole bool) ([]FileGuardResult, []CheckOutcome) {
	t.Helper()
	rng, err := gitrepo.ResolveRange(f.repo, base, head)
	require.NoError(t, err)
	p := f.params(t, f.results)
	p.Range, p.Verify, p.WholeRange = rng, verify, whole
	return Evaluate(p)
}

// Verify computes the same effective base from the stored runs, so what run judged verifies
// over the original, wider range, and nothing is executed.
// sr:proves fileguard/passes-not-re-examined
func TestIncremental_RunAndVerifyAgreeOnTheBase(t *testing.T) {
	f := newEvalFixture(t, nil).withSession(t)
	f.commitDoc(t, "docs/a.md", "clean a")
	_, refused := f.evaluate(t, f.results)
	require.False(t, refused)
	f.commitDoc(t, "docs/b.md", "clean b")
	_, refused = f.evaluate(t, f.results)
	require.False(t, refused)
	require.Equal(t, 2, f.runs(t))

	got, _ := f.evaluateOver(t, f.base, "HEAD", true, false)
	assert.Empty(t, got, "verify finds the stored passes from the same effective base")
	assert.Equal(t, 2, f.runs(t), "verify executed nothing")
	got, _ = f.evaluateOver(t, f.base, "HEAD", true, true)
	assert.NotEmpty(t, got, "the whole range (show) is a change nobody judged as one: it starts from the requested base")
}

// A base that is not an ancestor of the head is ignored, and so is one before the requested base.
// sr:proves fileguard/passes-not-re-examined
func TestIncremental_ABaseThatIsNotAnAncestorIsIgnored(t *testing.T) {
	f := newEvalFixture(t, nil)
	f.commitDoc(t, "docs/a.md", "clean")
	_, refused := f.evaluate(t, f.results)
	require.False(t, refused)
	runGit(t, f.repo, "checkout", "-q", "-b", "other", f.base)
	f.commitDoc(t, "docs/z.md", "clean z")
	assert.Equal(t, f.base, f.baseAt(t))
}

// A rule with a `subjects:` script is judged over the range it is asked about: its subjects'
// fingerprints depend on more than the diff, so a base that only moves forward would never ask again.
func TestIncremental_ASubjectsScriptRuleKeepsTheRequestedBase(t *testing.T) {
	f := newEvalFixture(t, nil)
	f.commitDoc(t, "docs/a.md", "clean")
	_, refused := f.evaluate(t, f.results)
	require.False(t, refused)
	assert.NotEqual(t, f.base, f.baseAt(t), "premise: a plain rule's base advanced")
	f.guard.Subjects = "./subjects.sh"
	assert.Equal(t, f.base, f.baseAt(t))
}

// Passes chain: B1..H1 then H1..H2 advance the base from B1 to H2, but a pass over a NARROW range
// B2..H (B2 after B1) leaves B1..B2 unjudged and advances nothing; a FAIL anywhere is no pass.
// sr:proves fileguard/passes-not-re-examined
func TestIncremental_EffectiveBaseChainsPassesAndIgnoresNarrowOnes(t *testing.T) {
	f := newEvalFixture(t, nil).withSession(t)
	c1 := f.commitDoc(t, "docs/a.md", "clean a")
	f.commitDoc(t, "docs/b.md", "clean b")
	c3 := f.commitDoc(t, "docs/c.md", "clean c")
	over := func(base, head string) {
		t.Helper()
		got, _ := f.evaluateOver(t, base, head, false, false)
		require.Empty(t, got)
	}
	baseFor := func(head string) string {
		t.Helper()
		rng, err := gitrepo.ResolveRange(f.repo, f.base, head)
		require.NoError(t, err)
		return f.newEvaluation(t, f.results).effectiveBase(f.guard, rng).Base
	}

	over(c1, c3) // narrow: B2=c1 is after B1=f.base
	assert.Equal(t, f.base, baseFor(c3), "a pass over B2..H does not cover B1..B2")
}

// sr:proves fileguard/passes-not-re-examined
func TestIncremental_SequentialPassesChain(t *testing.T) {
	f := newEvalFixture(t, nil).withSession(t)
	c1 := f.commitDoc(t, "docs/a.md", "clean a")
	c2 := f.commitDoc(t, "docs/b.md", "clean b")
	c3 := f.commitDoc(t, "docs/c.md", "clean c")
	for _, span := range [][2]string{{f.base, c1}, {c1, c2}} {
		got, _ := f.evaluateOver(t, span[0], span[1], false, false)
		require.Empty(t, got)
	}
	rng, err := gitrepo.ResolveRange(f.repo, f.base, c3)
	require.NoError(t, err)
	assert.Equal(t, c2, f.newEvaluation(t, f.results).effectiveBase(f.guard, rng).Base, "B1..H1 then H1..H2 reach H2")
}

// sr:proves fileguard/passes-not-re-examined
func TestIncremental_AFailInTheChainStopsIt(t *testing.T) {
	f := newEvalFixture(t, nil).withSession(t)
	c1 := f.commitDoc(t, "docs/a.md", "clean a")
	c2 := f.commitDoc(t, "docs/b.md", "FORBIDDEN")
	c3 := f.commitDoc(t, "docs/c.md", "clean c")
	run := func(base, head string) bool {
		t.Helper()
		got, _ := f.evaluateOver(t, base, head, false, false)
		return len(got) > 0
	}
	assert.False(t, run(f.base, c1))
	assert.True(t, run(c1, c2), "the forbidden file is refused")
	rng, err := gitrepo.ResolveRange(f.repo, f.base, c3)
	require.NoError(t, err)
	assert.Equal(t, c1, f.newEvaluation(t, f.results).effectiveBase(f.guard, rng).Base, "the base reaches the last pass and stops at the fail")
}
