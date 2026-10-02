package changeset

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSubjects(t *testing.T) {
	cs := Changeset{Files: []File{{Path: "a.md"}, {Path: "b.md"}}}

	got, err := ParseSubjects([]byte(`[{"id":"x","files":["a.md"],"fingerprint":"v1"},{"id":"y","files":["b.md"]}]`), cs)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "x", got[0].ID)
	assert.Equal(t, "v1", got[0].Fingerprint)
	assert.Empty(t, got[1].Fingerprint, "the fingerprint is optional")

	for name, in := range map[string]string{
		"not json":        `nope`,
		"empty":           `[]`,
		"no id":           `[{"files":["a.md"]}]`,
		"duplicate id":    `[{"id":"x","files":["a.md"]},{"id":"x","files":["b.md"]}]`,
		"no files":        `[{"id":"x","files":[]}]`,
		"unselected file": `[{"id":"x","files":["c.md"]}]`,
	} {
		_, err := ParseSubjects([]byte(in), cs)
		assert.Error(t, err, name)
	}
}
