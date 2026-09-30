package changeset

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func reachableSet(shas ...string) func(string) (bool, error) {
	return func(s string) (bool, error) {
		for _, x := range shas {
			if x == s {
				return true, nil
			}
		}
		return false, nil
	}
}

func TestPickWatermark_NewestReachable(t *testing.T) {
	wm, dropped, err := PickWatermark([]string{"h2", "h1", "h0"}, reachableSet("h2", "h1", "h0"))
	require.NoError(t, err)
	assert.Equal(t, "h2", wm)
	assert.Empty(t, dropped)
}

func TestPickWatermark_AnOrphanedNewestFallsToTheNextAndIsReportedDropped(t *testing.T) {
	wm, dropped, err := PickWatermark([]string{"h2", "h1", "h0"}, reachableSet("h0"))
	require.NoError(t, err)
	assert.Equal(t, "h0", wm)
	assert.Equal(t, "h2", dropped, "the newest pass that had to be given up")
}

func TestPickWatermark_NoneReachableOrNoneRecorded(t *testing.T) {
	wm, dropped, err := PickWatermark([]string{"h1"}, reachableSet())
	require.NoError(t, err)
	assert.Empty(t, wm)
	assert.Equal(t, "h1", dropped)

	wm, dropped, err = PickWatermark(nil, reachableSet())
	require.NoError(t, err)
	assert.Empty(t, wm)
	assert.Empty(t, dropped)
}

func TestPickWatermark_AnErrorIsNotUnreachable(t *testing.T) {
	boom := errors.New("git broke")
	_, _, err := PickWatermark([]string{"h1", "h0"}, func(string) (bool, error) { return false, boom })
	assert.ErrorIs(t, err, boom)
}
