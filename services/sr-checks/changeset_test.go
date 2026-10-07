package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/declaration"
)

func fileGuards(names ...string) declaration.Loaded {
	var l declaration.Loaded
	for _, n := range names {
		l.FileGuards = append(l.FileGuards, declaration.FileGuard{Name: n})
	}
	return l
}

func TestFindFileGuard_ByFolderNameOrQualifiedName(t *testing.T) {
	loaded := fileGuards("size", "docs")
	g, err := findFileGuard(loaded, "docs")
	require.NoError(t, err)
	assert.Equal(t, "docs", g.Name)
	g, err = findFileGuard(loaded, g.Qualified())
	require.NoError(t, err)
	assert.Equal(t, "docs", g.Name)
}

func TestFindFileGuard_UnknownNamesWhatIsLoaded(t *testing.T) {
	_, err := findFileGuard(fileGuards("size", "docs"), "nope")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `no file-guard "nope" is loaded`)
	assert.Contains(t, err.Error(), "file-guard/size")
	assert.Contains(t, err.Error(), "file-guard/docs")
}

func TestFindFileGuard_AmbiguityIsAnErrorNotAFirstWins(t *testing.T) {
	loaded := fileGuards("size")
	loaded.FileGuards = append(loaded.FileGuards, declaration.FileGuard{Name: "size", Origin: declaration.Origin{Plugin: "acme", Root: "/plugins/acme"}})
	_, err := findFileGuard(loaded, "size")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "names 2 file-guards")

	g, err := findFileGuard(loaded, loaded.FileGuards[1].Qualified())
	require.NoError(t, err)
	assert.True(t, g.Origin.FromPlugin(), "the qualified name picks the plugin's")
}

// sr:proves fileguard/unloadable-guard-refuses
func TestBrokenFileGuards_OnlyFileGuardsThatFailedToLoadAreNamed(t *testing.T) {
	var none declaration.Loaded
	assert.Empty(t, brokenFileGuards(none), "no file-guards and none invalid is clean")
	assert.Empty(t, brokenFileGuards(fileGuards("ok")))

	l := fileGuards("ok")
	l.Invalid = []declaration.Invalid{
		{Nature: declaration.NatureFileGuard, Name: "broken", Reason: "bad yaml"},
		{Nature: declaration.NatureGate, Name: "other-nature", Reason: "x"},
	}
	got := brokenFileGuards(l)
	require.Len(t, got, 1)
	assert.Contains(t, got[0], "broken")
	assert.Contains(t, got[0], "bad yaml")
}
