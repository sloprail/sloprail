package harness

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

type markedHarness struct{ fakeHarness }

func (markedHarness) ProjectMarkers() []string { return []string{".m"} }

func TestProjectMarkers(t *testing.T) {
	assert.Equal(t, []string{".m"}, ProjectMarkers(markedHarness{fakeHarness{name: "m"}}))
	assert.Empty(t, ProjectMarkers(fakeHarness{name: "plain"}), "a harness without the seam has no markers")
}
