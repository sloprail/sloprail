package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/filemod"
	"github.com/sloprail/sloprail/internal/natures"
	"github.com/sloprail/sloprail/internal/sessionstate"
)

// These pin a file-guard's `deletions:` filter at every place a guard is
// dispatched on a file event — the after-check (Post)
// path, and the pre-tool event binding — and the revalidation consequence: a
// refusal a guard left on a file that has since been deleted is settled whenever
// the guard does not refuse the delete, so it cannot stay outstanding forever.

// deletionModes is every value a guard's `deletions:` can take, the absent key
// (the default) included.
var deletionModes = []declaration.Deletions{"", declaration.DeletionsSkip, declaration.DeletionsInclude, declaration.DeletionsOnly}

// wantRuns is which file kinds a guard with each `deletions:` value runs on — the
// contract the dispatch tests below hold both paths to.
func wantRuns(mode declaration.Deletions, kind string) bool {
	del := declaration.IsFileDeleteKind(kind)
	switch mode.Mode() {
	case declaration.DeletionsInclude:
		return true
	case declaration.DeletionsOnly:
		return del
	default: // skip
		return !del
	}
}

// refusingGuard is a guard whose one script check appends the event kind it was
// handed to a ledger and then REFUSES, so a test can tell "ran" from "did not run"
// both by the ledger and by whether a refusal came back.
func refusingGuard(t *testing.T, mode declaration.Deletions) (declaration.FileGuard, string) {
	t.Helper()
	dir := t.TempDir()
	ledger := filepath.Join(dir, "ledger")
	writeExecutable(t, dir, "refuse.sh", `#!/bin/sh
kind="$(jq -r '.event.kind')"
echo "$kind" >> "`+ledger+`"
echo "{\"reason\":\"REFUSED-$kind\"}"
exit 1
`)
	return declaration.FileGuard{
		Name:      "g",
		Match:     "docs/**",
		Deletions: mode,
		Checks:    []declaration.Check{{Script: "./refuse.sh"}},
		Dir:       dir,
	}, ledger
}

func ledgerLines(t *testing.T, path string) []string {
	t.Helper()
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	return strings.Fields(string(body))
}

// preEvent is a Pre file event of a kind, carrying what filemod would: a known
// result on create/update, the lost bytes and their markers on a delete.
func preEvent(kind, path string) event.Event {
	f := map[string]any{filemod.FieldPath: path}
	switch kind {
	case declaration.KindPreFileCreate, declaration.KindPreFileUpdate:
		f[filemod.FieldNewContent] = "body"
		f[filemod.FieldResultKnown] = true
		f[filemod.FieldNewMarkers] = []any{}
	case declaration.KindPreFileDelete:
		f[filemod.FieldOldContent] = "body"
		f[filemod.FieldOldMarkers] = []any{}
	}
	return event.Event{Kind: kind, Fields: f}
}

// postFileEvent is a Post file event of a kind (the settled-state shape).
func postFileEvent(kind, path string) event.Event {
	f := map[string]any{filemod.FieldPath: path}
	switch kind {
	case declaration.KindPostFileCreate, declaration.KindPostFileUpdate:
		f[filemod.FieldNewContent] = "body"
		f[filemod.FieldNewMarkers] = []any{}
	case declaration.KindPostFileDelete:
		f[filemod.FieldOldContent] = "body"
		f[filemod.FieldOldMarkers] = []any{}
	}
	return event.Event{Kind: kind, Fields: f}
}

// The AFTER-CHECK path applies the same filter to the Post kinds.
func TestRunFileGuardsPost_DeletionsFilter(t *testing.T) {
	kinds := []string{declaration.KindPostFileCreate, declaration.KindPostFileUpdate, declaration.KindPostFileDelete}
	for _, mode := range deletionModes {
		for _, kind := range kinds {
			t.Run(string(mode.Mode())+"/"+kind, func(t *testing.T) {
				g, ledger := refusingGuard(t, mode)
				root := t.TempDir()
				rev := newRevalidation(t)
				results := runFileGuardsPost(discard(), []declaration.FileGuard{g},
					[]event.Event{postFileEvent(kind, "docs/a.md")}, rev, hookScope{}, root, map[string]natures.ContextState{}, nil)
				if wantRuns(mode, kind) {
					require.Len(t, results, 1, "deletions=%q must run on %s", mode, kind)
					assert.True(t, results[0].Refused)
					assert.Contains(t, results[0].Reason, "REFUSED-"+kind)
					assert.Equal(t, []string{kind}, ledgerLines(t, ledger))
				} else {
					assert.Empty(t, results, "deletions=%q must not run on %s", mode, kind)
					assert.Empty(t, ledgerLines(t, ledger), "the check must not even start")
				}
			})
		}
	}
}

// A guard that includes deletions and matches by MARKER still selects a delete:
// the deleted file's markers are its oldMarkers. Without that, `deletions:
// include` would be inert for every marker-scoped guard.
func TestRunFileGuardsPost_MarkerMatchSelectsDeleteByOldMarkers(t *testing.T) {
	g, ledger := refusingGuard(t, declaration.DeletionsInclude)
	g.Match = `any(markers, .kind == "invariant")`
	del := event.Event{Kind: declaration.KindPostFileDelete, Fields: map[string]any{
		filemod.FieldPath:       "src/pinned.go",
		filemod.FieldOldContent: "// sr:invariant x",
		filemod.FieldOldMarkers: []any{marker("invariant", "x", 1)},
	}}
	results := runFileGuardsPost(discard(), []declaration.FileGuard{g}, []event.Event{del},
		newRevalidation(t), hookScope{}, t.TempDir(), map[string]natures.ContextState{}, nil)
	require.Len(t, results, 1, "a delete of a marker-carrying file is selected by its oldMarkers")
	assert.Equal(t, []string{declaration.KindPostFileDelete}, ledgerLines(t, ledger))
}

// fileMarkers reads oldMarkers on a delete, and newMarkers everywhere else — an
// update whose result dropped every marker reads as marker-less, never as the
// markers it just removed.
func TestFileMarkers_DeleteReadsOldMarkers(t *testing.T) {
	was := []any{marker("invariant", "x", 1)}
	for _, kind := range []string{declaration.KindPreFileDelete, declaration.KindPostFileDelete} {
		del := event.Event{Kind: kind, Fields: map[string]any{filemod.FieldOldMarkers: was}}
		assert.Equal(t, was, fileMarkers(del), kind)
	}
	upd := event.Event{Kind: declaration.KindPostFileUpdate, Fields: map[string]any{
		filemod.FieldOldMarkers: was,
		filemod.FieldNewMarkers: []any{},
	}}
	assert.Empty(t, fileMarkers(upd), "an update that stripped its markers is marker-less")
	assert.NotNil(t, fileMarkers(upd))
}

// A file-guard binds nothing at pre-tool, whatever its `deletions:`: it judges the
// settled file at Stop, and only a gate acts before a write.
func TestNaturePreToolBoundKinds_FileGuardsBindNothing(t *testing.T) {
	for _, mode := range deletionModes {
		loaded := declaration.Loaded{FileGuards: []declaration.FileGuard{{Name: "g", Deletions: mode}}}
		assert.Empty(t, naturePreToolBoundKinds(loaded), "deletions=%q", mode)
	}
}

// --- the re-fire path ---------------------------------------------------------

// newRevalidation is a revalidation over a fresh session store.
func newRevalidation(t *testing.T) *revalidation {
	t.Helper()
	store, err := sessionstate.Open(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	return &revalidation{store: store}
}

// seedRefusal records that guard g refused path at some content — the state an
// earlier cycle leaves when the file was not fine.
func seedRefusal(t *testing.T, rev *revalidation, guard, path string) {
	t.Helper()
	require.NoError(t, rev.Record(fileGuardRevKey(guard), subject{Path: path, Fingerprint: "0123456789abcdef0123456789abcdef01234567"}, false))
	out, err := rev.store.OutstandingRefusals()
	require.NoError(t, err)
	require.Len(t, out, 1, "the seeded refusal is outstanding")
}

func outstandingFor(t *testing.T, rev *revalidation, guard string) []string {
	t.Helper()
	out, err := rev.store.OutstandingRefusals()
	require.NoError(t, err)
	var paths []string
	for _, r := range out {
		if r.Guardrail == fileGuardRevKey(guard) {
			paths = append(paths, r.Path)
		}
	}
	return paths
}

// A guard that SKIPS deletions refused a file; the file is then deleted. The
// guard is not run on the PostFileDelete (it does not cover it) — and its
// refusal, which it can now never be asked to clear, is settled rather than left
// outstanding to re-add a vanished path every cycle.
func TestRunFileGuardsPost_SkipGuardSettlesRefusalOnDeletedFile(t *testing.T) {
	for _, mode := range []declaration.Deletions{"", declaration.DeletionsSkip} {
		t.Run(string(mode.Mode()), func(t *testing.T) {
			g, ledger := refusingGuard(t, mode)
			rev := newRevalidation(t)
			seedRefusal(t, rev, g.Name, "docs/a.md")

			results := runFileGuardsPost(discard(), []declaration.FileGuard{g},
				[]event.Event{postFileEvent(declaration.KindPostFileDelete, "docs/a.md")}, rev, hookScope{}, t.TempDir(), map[string]natures.ContextState{}, nil)

			assert.Empty(t, results, "the skip guard does not run on the delete")
			assert.Empty(t, ledgerLines(t, ledger))
			assert.Empty(t, outstandingFor(t, rev, g.Name), "its refusal on the now-deleted file is settled")
		})
	}
}

// A guard that includes deletions and PASSES the delete has judged the file's
// last state and found it fine: its earlier refusal is settled too.
func TestRunFileGuardsPost_IncludeGuardPassingDeleteSettlesRefusal(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, dir, "ok.sh", "#!/bin/sh\ncat >/dev/null\nexit 0\n")
	g := declaration.FileGuard{Name: "g", Match: "docs/**", Deletions: declaration.DeletionsInclude,
		Checks: []declaration.Check{{Script: "./ok.sh"}}, Dir: dir}
	rev := newRevalidation(t)
	seedRefusal(t, rev, g.Name, "docs/a.md")

	results := runFileGuardsPost(discard(), []declaration.FileGuard{g},
		[]event.Event{postFileEvent(declaration.KindPostFileDelete, "docs/a.md")}, rev, hookScope{}, t.TempDir(), map[string]natures.ContextState{}, nil)

	assert.Empty(t, results)
	assert.Empty(t, outstandingFor(t, rev, g.Name))
}

// A guard that includes deletions and REFUSES the delete keeps its refusal
// outstanding: that is its live answer, and it must re-fire until the file is
// back and fine.
func TestRunFileGuardsPost_IncludeGuardRefusingDeleteKeepsRefusal(t *testing.T) {
	g, _ := refusingGuard(t, declaration.DeletionsInclude)
	rev := newRevalidation(t)
	seedRefusal(t, rev, g.Name, "docs/a.md")

	results := runFileGuardsPost(discard(), []declaration.FileGuard{g},
		[]event.Event{postFileEvent(declaration.KindPostFileDelete, "docs/a.md")}, rev, hookScope{}, t.TempDir(), map[string]natures.ContextState{}, nil)

	require.Len(t, results, 1)
	assert.True(t, results[0].Refused)
	assert.Equal(t, []string{"docs/a.md"}, outstandingFor(t, rev, g.Name), "a refused delete stays outstanding")
}

// Settling touches only the guard's own refusal on the deleted path, and writes
// nothing where there was nothing outstanding.
func TestSettleGone_OnlyEndsAnOutstandingRefusal(t *testing.T) {
	rev := newRevalidation(t)
	key := fileGuardRevKey("g")

	// Never judged: nothing is written.
	require.NoError(t, rev.SettleGone(key, "docs/never.md"))
	_, ok, err := rev.store.FileCheck("docs/never.md", key)
	require.NoError(t, err)
	assert.False(t, ok, "settling a path the guard never judged writes no row")

	// Refused, then settled: no longer outstanding, and the refused content still
	// does not license a skip if it comes back.
	fp := "0123456789abcdef0123456789abcdef01234567"
	require.NoError(t, rev.Record(key, subject{Path: "docs/a.md", Fingerprint: fp}, false))
	require.NoError(t, rev.SettleGone(key, "docs/a.md"))
	out, err := rev.store.OutstandingRefusals()
	require.NoError(t, err)
	assert.Empty(t, out)
	skip, err := rev.Skip(key, subject{Path: "docs/a.md", Fingerprint: fp})
	require.NoError(t, err)
	assert.False(t, skip, "the refused bytes, restored, are judged again rather than exempted")

	// Another guard's refusal on the same path is not this guard's to settle.
	other := fileGuardRevKey("other")
	require.NoError(t, rev.Record(other, subject{Path: "docs/b.md", Fingerprint: fp}, false))
	require.NoError(t, rev.SettleGone(key, "docs/b.md"))
	out, err = rev.store.OutstandingRefusals()
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, other, out[0].Guardrail)

	// Nil-safe, like every revalidation method.
	var nilRev *revalidation
	assert.NoError(t, nilRev.SettleGone(key, "docs/a.md"))
}
