package changeset

import (
	"testing"

	"github.com/sloprail/sloprail/internal/transcript"
	"github.com/stretchr/testify/assert"
)

// A file left byte-identical to a state a cited commit made is grounded by that commit's
// citation, though the commit that last changed it is uncited.
func TestForFile_RestoringACitedStateIsGroundedByContent(t *testing.T) {
	cs := Changeset{Citations: []Citation{
		{Citation: transcript.Citation{Quote: "a"}, Commits: []string{"c1"}},
	}}
	restored := File{Path: "x.md", Commits: []string{"c1", "c2", "c3"}, Substantive: []string{"c1", "c2", "c3"}, SameContent: []string{"c1", "c3"}}
	got := cs.ForFile(restored)
	assert.Len(t, got, 1, "the revert of an uncited tweak is grounded by the cited state it restored")

	partial := File{Path: "x.md", Commits: []string{"c1", "c2", "c3"}, Substantive: []string{"c1", "c2", "c3"}, SameContent: nil}
	assert.Empty(t, cs.ForFile(partial), "a change to content no cited commit made stays uncited")

	uncitedState := File{Path: "x.md", Commits: []string{"c2", "c3"}, Substantive: []string{"c2", "c3"}, SameContent: []string{"c2", "c3"}}
	assert.Empty(t, cs.ForFile(uncitedState), "restoring an uncited state grounds nothing")
}
