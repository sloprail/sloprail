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
		{Name: "b", Refused: true, Reason: "the file-guard \"b\" could not be evaluated (x)", Unavailable: "authentication"},
		{Name: "a", Refused: true, Reason: "the file-guard \"a\" could not be evaluated (x)", Unavailable: "usage limit"},
		{Name: "script", Refused: true, Reason: "the script refused: nope"},
	}
	out := collapseUnavailable(in)
	require.Len(t, out, 3, "the two real refusals stay, the outage is one")
	assert.Equal(t, "real", out[0].Name)
	assert.Equal(t, "script", out[1].Name)
	assert.Equal(t, "forbidden words", out[0].Reason)
	assert.True(t, out[2].Refused)
	assert.Contains(t, out[2].Reason, "judges unavailable: 3 rules not evaluated: usage limit, authentication")
	assert.Contains(t, out[2].Reason, "Not decided: a, b")

	same := []FileGuardResult{{Name: "real", Refused: true, Reason: "x"}}
	assert.Equal(t, same, collapseUnavailable(same))
}
