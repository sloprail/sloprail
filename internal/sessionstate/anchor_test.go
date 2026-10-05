package sessionstate

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// What the cap drops is a prefix of the history by time: no point older than the anchor is kept,
// or it would run before the anchor in the walk and not see the state the dropped ones set.
func TestCompactCitationPoints_WhatGoesIsAPrefixByTime(t *testing.T) {
	points := []json.RawMessage{
		pt(t, map[string]any{"foreign": true, "before": map[string]any{"exists": false}, "after": state("h2"), "at": 1}),
		cited(t, 2),
	}
	for i := 10; i < 50; i++ {
		points = append(points, uncited(t, fmt.Sprintf("f%d", i), fmt.Sprintf("t%d", i), i, ""))
	}
	got := CompactCitationPoints(points)
	ats := atsOf(t, got)
	require.NotEmpty(t, ats)
	anchorAt := ats[0]
	for _, at := range ats[1:] {
		assert.Greater(t, at, anchorAt, "nothing older than the anchor is kept")
	}
	assert.NotContains(t, ats, 2, "the old cited change goes with the prefix it belongs to")
	assert.Equal(t, 49, ats[len(ats)-1])
	assert.Len(t, ats, MaxUncitedPoints+1)
}
