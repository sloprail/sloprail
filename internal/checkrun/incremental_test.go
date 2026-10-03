package checkrun

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/changeset"
	"github.com/sloprail/sloprail/internal/gitrepo"
)

func (f *evalFixture) hash(t *testing.T) string {
	t.Helper()
	h, err := changeset.RuleHashAt(f.repo, f.guard.Dir, false)
	require.NoError(t, err)
	return h
}

func (f *evalFixture) baseAt(t *testing.T, hash string) string {
	t.Helper()
	rng, err := gitrepo.ResolveRange(f.repo, f.base, "HEAD")
	require.NoError(t, err)
	return f.newEvaluation(t, f.results).effectiveBase(f.guard, hash, rng).Base
}

// The effective base is the head of the latest complete PASSING run: a fail never advances
// it, a pass does, and a stored fail is replayed (no re-roll) until its input changes.
func TestIncremental_EffectiveBaseAdvancesOnlyOnPass(t *testing.T) {
	f := newEvalFixture(t, nil).withSession(t)
	hash := f.hash(t)
	assert.Equal(t, f.base, f.baseAt(t, hash), "no run yet: the requested base")

	f.commitDoc(t, "docs/a.md", "FORBIDDEN")
	r, refused := f.evaluate(t, f.results)
	require.True(t, refused)
	assert.Contains(t, r.Reason, "forbidden words")
	require.Equal(t, 1, f.runs(t))
	assert.Equal(t, f.base, f.baseAt(t, hash), "a FAIL never advances the base")

	_, refused = f.evaluate(t, f.results)
	require.True(t, refused, "the stored fail is replayed")
	assert.Equal(t, 1, f.runs(t), "no re-roll")

	fixed := f.commitDoc(t, "docs/a.md", "clean now")
	_, refused = f.evaluate(t, f.results)
	require.False(t, refused)
	require.Equal(t, 2, f.runs(t))
	assert.Equal(t, fixed, f.baseAt(t, hash), "a pass advances the base to its head")

	// Only what changed since is examined: a new file is judged alone, the old one is not
	// looked at again.
	f.commitDoc(t, "docs/b.md", "clean b")
	_, refused = f.evaluate(t, f.results)
	require.False(t, refused)
	assert.Equal(t, 3, f.runs(t))
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
func TestIncremental_ABaseThatIsNotAnAncestorIsIgnored(t *testing.T) {
	f := newEvalFixture(t, nil)
	hash := f.hash(t)
	f.commitDoc(t, "docs/a.md", "clean")
	_, refused := f.evaluate(t, f.results)
	require.False(t, refused)
	runGit(t, f.repo, "checkout", "-q", "-b", "other", f.base)
	f.commitDoc(t, "docs/z.md", "clean z")
	assert.Equal(t, f.base, f.baseAt(t, hash))
}

// A rule with a `subjects:` script is judged over the range it is asked about: its subjects'
// fingerprints depend on more than the diff, so a base that only moves forward would never ask again.
func TestIncremental_ASubjectsScriptRuleKeepsTheRequestedBase(t *testing.T) {
	f := newEvalFixture(t, nil)
	hash := f.hash(t)
	f.commitDoc(t, "docs/a.md", "clean")
	_, refused := f.evaluate(t, f.results)
	require.False(t, refused)
	assert.NotEqual(t, f.base, f.baseAt(t, hash), "premise: a plain rule's base advanced")
	f.guard.Subjects = "./subjects.sh"
	assert.Equal(t, f.base, f.baseAt(t, hash))
}
