package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/changeset"
	"github.com/sloprail/sloprail/internal/guardrail"
)

func compileMatch(t *testing.T, src string) func(changeset.Scope) (bool, error) {
	t.Helper()
	m, err := guardrail.CompileFileMatch(src)
	require.NoError(t, err)
	return changesetSelector(m, map[string]any{})
}

func TestChangesetMarkers_AreFilemodsScanInTheChangesetsType(t *testing.T) {
	got := changesetMarkers("package x\n// sr:invariant order.total\nfunc f() {}\n// sr:rule a.b\n")
	require.Len(t, got, 2)
	assert.Equal(t, changeset.Marker{Kind: "invariant", FQN: "order.total", Line: 2}, got[0])
	assert.Equal(t, "rule", got[1].Kind)
	assert.NotNil(t, changesetMarkers("nothing here"), "never nil: JSON renders [] and a check reading .newMarkers[] never meets null")
	assert.Empty(t, changesetMarkers("nothing here"))
}

func TestChangesetSelector_AGlobSelectsByPath(t *testing.T) {
	sel := compileMatch(t, "docs/**")
	ok, err := sel(changeset.Scope{Path: "docs/a.md"})
	require.NoError(t, err)
	assert.True(t, ok)
	ok, err = sel(changeset.Scope{Path: "src/a.go"})
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestChangesetSelector_TheScopeCarriesStatusMarkersOldMarkersAndTrailers(t *testing.T) {
	sel := compileMatch(t, `status == "M" && any(markers, {.kind == "invariant"}) && any(oldMarkers, {.fqn == "a.b"}) && "Sloprail-Refactor" in keys(trailers)`)
	scope := changeset.Scope{
		Path: "x.go", Status: "M",
		Markers:    []changeset.Marker{{Kind: "invariant", FQN: "a.b", Line: 1}},
		OldMarkers: []changeset.Marker{{Kind: "invariant", FQN: "a.b", Line: 1}},
		Trailers:   map[string][]string{"Sloprail-Refactor": {"move-only"}},
	}
	ok, err := sel(scope)
	require.NoError(t, err)
	assert.True(t, ok)

	scope.Status = "D"
	ok, err = sel(scope)
	require.NoError(t, err)
	assert.False(t, ok, "status is part of the scope")
}

func TestChangesetSelector_NoMarkersIsFalseNotAnError(t *testing.T) {
	sel := compileMatch(t, `any(markers, {.kind == "invariant"})`)
	ok, err := sel(changeset.Scope{Path: "x.go", Status: "A"})
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestChangesetSelector_AMatchThatCannotDecideIsAnErrorNotAMiss(t *testing.T) {
	sel := compileMatch(t, `context["goal"].active`)
	_, err := sel(changeset.Scope{Path: "x.go", Status: "M"})
	assert.Error(t, err, "a match that could not decide has not decided the file is none of the rule's business")
}

func TestChangesetSelector_ContextIsTheCallersToGive(t *testing.T) {
	m, err := guardrail.CompileFileMatch(`context["goal"].active == true`)
	require.NoError(t, err)
	sel := changesetSelector(m, map[string]any{"goal": map[string]any{"active": true}})
	ok, err := sel(changeset.Scope{Path: "x.go", Status: "M"})
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestChangesetSelector_ARenameIsAskedOnTheOldPathToo(t *testing.T) {
	sel := compileMatch(t, "memories/**")
	ok, err := changeset.Selects(sel, changeset.Scope{Path: "archive/x.md", OldPath: "memories/x.md", Status: "R"})
	require.NoError(t, err)
	assert.True(t, ok)
	ok, err = changeset.Selects(sel, changeset.Scope{Path: "archive/x.md", OldPath: "notes/x.md", Status: "R"})
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestMarkersWireAndTrailersWire_AreNeverNil(t *testing.T) {
	assert.NotNil(t, markersWire(nil))
	assert.Empty(t, markersWire(nil))
	assert.Equal(t, []any{map[string]any{"kind": "k", "fqn": "f", "line": 3}}, markersWire([]changeset.Marker{{Kind: "k", FQN: "f", Line: 3}}))
	assert.NotNil(t, trailersWire(nil))
	assert.Equal(t, map[string]any{"K": []any{"a", "b"}}, trailersWire(map[string][]string{"K": {"a", "b"}}))
}

// userRecord writes a minimal session record in which the user said `said`.
func userRecord(t *testing.T, said string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(
		`{"type":"user","uuid":"u1","parentUuid":null,"sessionId":"s1","cwd":"/x","message":{"role":"user","content":"`+said+`"}}`+"\n"), 0o644))
	return path
}

func TestResolveChangesetCitations_GroundsTrailersAndSaysWhichFilesTheyCover(t *testing.T) {
	record := userRecord(t, "please adopt a decision log")
	cs := changeset.Changeset{
		Commits: []changeset.Commit{
			{SHA: "c1", Trailers: map[string][]string{changeset.TrailerCitesUser: {"adopt a decision log"}}},
			{SHA: "c2", Trailers: map[string][]string{changeset.TrailerCitesUser: {"the user never said this"}}},
		},
		Files: []changeset.File{{Path: "a.md", Commits: []string{"c1"}}, {Path: "b.md", Commits: []string{"c2"}}},
	}

	missed := resolveChangesetCitations(&cs, record, t.TempDir())

	require.Len(t, cs.Citations, 1)
	assert.Equal(t, "adopt a decision log", cs.Citations[0].Quote)
	assert.Equal(t, []string{"c1"}, cs.Citations[0].Commits)
	assert.Equal(t, []string{"a.md"}, cs.Citations[0].Files, "a citation covers the files its commit changed")
	require.Len(t, missed, 1)
	assert.Equal(t, "the user never said this", missed[0].Quote, "an unresolvable quote is reported, never a citation")
	assert.Equal(t, 1, len(cs.ForFile(cs.Files[0])))
	assert.Empty(t, cs.ForFile(cs.Files[1]))
}

func TestResolveChangesetCitations_WithNoRecordThereAreNoCitations(t *testing.T) {
	cs := changeset.Changeset{Commits: []changeset.Commit{{SHA: "c1", Trailers: map[string][]string{changeset.TrailerCitesUser: {"x"}}}}}
	assert.Nil(t, resolveChangesetCitations(&cs, "", t.TempDir()))
	assert.Empty(t, cs.Citations)
}
