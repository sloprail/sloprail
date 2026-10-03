package checkrun

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/changeset"
	"github.com/sloprail/sloprail/internal/declaration"
)

func TestGuardKey_RangeCitationsMoveEveryGuardsKey(t *testing.T) {
	mk := func(trailer string) changeset.Payload {
		cs := changeset.Changeset{
			Commits: []changeset.Commit{{SHA: "c1", Trailers: map[string][]string{changeset.TrailerCitesUser: {trailer}}}},
			Files:   []changeset.File{{Path: "a.md", Status: "M", Commits: []string{"c1"}, NewContent: "x"}},
		}
		return changeset.NewPayload(cs, changeset.Whole(cs), "")
	}
	g := declaration.FileGuard{}
	key := func(p changeset.Payload) string {
		k, err := guardKey(g, p)
		require.NoError(t, err)
		return k
	}
	assert.Equal(t, key(mk("words")), key(mk("words")))
	assert.NotEqual(t, key(mk("words")), key(mk("better words")))
}
