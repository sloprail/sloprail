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
