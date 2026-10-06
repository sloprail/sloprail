package checkrun

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCollapseUnavailableReportsOneOutage(t *testing.T) {
	in := []FileGuardResult{
		{Name: "a", Refused: true, Reason: "the file-guard \"a\" could not be evaluated (x)", Unavailable: "usage limit"},
		{Name: "real", Refused: true, Reason: "forbidden words"},
		{Name: "b", Refused: true, Reason: "the file-guard \"b\" could not be evaluated (x)", Unavailable: "usage limit"},
	}
	out := collapseUnavailable(in)
	require.Len(t, out, 2)
	assert.Equal(t, "real", out[0].Name)
	assert.True(t, out[1].Refused)
	assert.Contains(t, out[1].Reason, "judges unavailable: 2 checks not evaluated: usage limit")
	assert.Contains(t, out[1].Reason, "a, b")

	same := []FileGuardResult{{Name: "real", Refused: true, Reason: "x"}}
	assert.Equal(t, same, collapseUnavailable(same))
}
