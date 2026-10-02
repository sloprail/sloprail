package checkrun

import (
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/declaration"
)

func TestLoadContextMap_WithoutAStoreEveryDeclaredContextIsInactive(t *testing.T) {
	got := LoadContextMap(io.Discard, nil, []declaration.Context{{Name: "goal"}, {Name: "research"}})
	require.Len(t, got, 2)
	assert.False(t, got["goal"].Active)
	assert.NotNil(t, got["goal"].Payload, "a match reading context[...] never meets nil")
	wire := ContextMatchValue(got)
	assert.Equal(t, false, wire["goal"].(map[string]any)["active"])
}
