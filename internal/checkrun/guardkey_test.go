package checkrun

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/changeset"
	"github.com/sloprail/sloprail/internal/declaration"
)

func twoFilePayload(extra bool) (changeset.Payload, changeset.Payload) {
	cs := changeset.Changeset{
		Commits: []changeset.Commit{{SHA: "c1", Trailers: map[string][]string{changeset.TrailerCitesUser: {"words"}}}},
		Files: []changeset.File{
			{Path: "a.md", Status: "M", Commits: []string{"c1"}, NewContent: "x"},
			{Path: "b.md", Status: "M", Commits: []string{"c1"}, NewContent: "y"},
		},
	}
	if extra {
		// A later commit touching neither file, carrying a citation.
		cs.Commits = append(cs.Commits, changeset.Commit{SHA: "c2", Trailers: map[string][]string{changeset.TrailerCitesTool: {"ls: cannot access"}}})
	}
	a := changeset.NewPayload(cs, changeset.Subject{ID: "a.md", Files: []string{"a.md"}}, "")
	b := changeset.NewPayload(cs, changeset.Subject{ID: "b.md", Files: []string{"b.md"}}, "")
	return a, b
}

// #291: a commit that touches none of a subject's files, whatever citation it carries, must
// not move that subject's key, for a rule that requires a citation and one that does not.
func TestGuardKey_ACommitOutsideTheSubjectDoesNotMoveItsKey(t *testing.T) {
	for _, g := range []declaration.FileGuard{
		{},
		{Require: []declaration.Prerequisite{{Citation: &declaration.CitationPrerequisite{}}}},
	} {
		key := func(p changeset.Payload) string {
			k, err := guardKey(g, p)
			require.NoError(t, err)
			return k
		}
		a0, b0 := twoFilePayload(false)
		a1, b1 := twoFilePayload(true)
		assert.Equal(t, key(a0), key(a1))
		assert.Equal(t, key(b0), key(b1))
	}
}

// What grounds the subject's own files still moves its key.
func TestGuardKey_TheSubjectsOwnCitationsMoveItsKey(t *testing.T) {
	g := declaration.FileGuard{}
	key := func(p changeset.Payload) string {
		k, err := guardKey(g, p)
		require.NoError(t, err)
		return k
	}
	a0, _ := twoFilePayload(false)
	a1, _ := twoFilePayload(false)
	a1.Changeset.Commits[0].Trailers = map[string][]string{changeset.TrailerCitesUser: {"better words"}}
	assert.NotEqual(t, key(a0), key(a1))
}

// What a subject's checks receive matches what its key covers: only the citations
// grounding its own files, never another file's, and an empty list rather than
// null. Reverting the scoping would hand a check citations the key ignores, and a
// stale verdict would be reused (#291).
func TestSubjectChangeset_ChecksSeeOnlyTheSubjectsCitations(t *testing.T) {
	cite := func(q, sha, file string) changeset.Citation {
		var c changeset.Citation
		c.Quote, c.Commits, c.Files = q, []string{sha}, []string{file}
		return c
	}
	cs := changeset.Changeset{
		Commits: []changeset.Commit{{SHA: "c1"}, {SHA: "c2"}},
		Files: []changeset.File{
			{Path: "a.md", Status: "M", Commits: []string{"c1"}},
			{Path: "b.md", Status: "M", Commits: []string{"c2"}},
		},
		Citations: []changeset.Citation{cite("words for a", "c1", "a.md"), cite("words for b", "c2", "b.md")},
	}
	a := subjectChangeset(cs, changeset.Subject{ID: "a.md", Files: []string{"a.md"}})
	assert.Equal(t, cs.EvidenceForSubject(changeset.Subject{ID: "a.md", Files: []string{"a.md"}}), a.Citations)
	for _, c := range a.Citations {
		assert.NotEqual(t, "words for b", c.Quote, "b.md's citation is no input of a.md's checks")
	}
	none := subjectChangeset(cs, changeset.Subject{ID: "c.md", Files: []string{"c.md"}})
	assert.NotNil(t, none.Citations, "never null: a check may iterate it")
	assert.Empty(t, none.Citations)
	assert.Len(t, cs.Citations, 2, "the range's own changeset is left as it was")
}

// Cited commits accumulate: a file changed by two cited commits hands its checks
// both commits' proof, and the earlier commit's quote is in the key though only
// the later one grounds the file — a reviewer judging that evidence must not be
// reused when it changes (TestReview_CitedEditAfterCitedTransitionKeepsEvidence).
func TestSubjectChangeset_EveryCommitOfTheFileIsEvidence(t *testing.T) {
	build := func(first string) changeset.Payload {
		cs := changeset.Changeset{
			Commits: []changeset.Commit{
				{SHA: "c1", Trailers: map[string][]string{changeset.TrailerCitesTool: {first}}},
				{SHA: "c2", Trailers: map[string][]string{changeset.TrailerCitesTool: {"PROOF-TWO"}}},
			},
			Files: []changeset.File{{Path: "a.md", Status: "M", Commits: []string{"c1", "c2"}, NewContent: "x"}},
		}
		TrustTrailers(&cs)
		sub := changeset.Subject{ID: "a.md", Files: []string{"a.md"}}
		return changeset.NewPayload(subjectChangeset(cs, sub), sub, "")
	}
	p := build("PROOF-ONE")
	var quotes []string
	for _, c := range p.Changeset.Citations {
		quotes = append(quotes, c.Quote)
	}
	assert.Equal(t, []string{"PROOF-ONE", "PROOF-TWO"}, quotes, "both steps' proof reaches the checks")

	key := func(p changeset.Payload) string {
		k, err := guardKey(declaration.FileGuard{}, p)
		require.NoError(t, err)
		return k
	}
	assert.NotEqual(t, key(p), key(build("PROOF-ONE-REWORDED")), "the earlier commit's evidence is in the key")
}
