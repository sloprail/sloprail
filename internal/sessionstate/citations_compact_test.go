package sessionstate

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func pt(t *testing.T, fields map[string]any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(fields)
	require.NoError(t, err)
	return b
}

func state(hash string) map[string]any { return map[string]any{"exists": true, "hash": hash} }

func uncited(t *testing.T, from, after string, at int, by string) json.RawMessage {
	f := map[string]any{"betweenTurns": true, "from": state(from), "before": map[string]any{"exists": false}, "after": state(after), "at": at, "fromAt": at - 1}
	if by != "" {
		f["by"] = by
	}
	return pt(t, f)
}

func cited(t *testing.T, at int) json.RawMessage {
	return pt(t, map[string]any{
		"cites":  []map[string]any{{"quote": "q", "path": "/r.jsonl", "line": at}},
		"before": state("b"), "after": state(fmt.Sprintf("a%d", at)), "at": at,
	})
}

func atsOf(t *testing.T, points []json.RawMessage) []int {
	t.Helper()
	var out []int
	for _, p := range points {
		var v struct{ At int }
		require.NoError(t, json.Unmarshal(p, &v))
		out = append(out, v.At)
	}
	return out
}

// A later hook that finds the file still not as the agent left it records the same stretch again:
// it is one stretch, not one more per cycle. The first is kept, naming the newest work.
func TestCompactCitationPoints_AStretchRecordedAgainIsOne(t *testing.T) {
	var points []json.RawMessage
	for i := 1; i <= 500; i++ {
		points = append(points, uncited(t, "eb4d", "9ced", i, fmt.Sprintf("job %d", i)))
	}
	got := CompactCitationPoints(points)
	require.Len(t, got, 1)
	assert.Equal(t, []int{1}, atsOf(t, got), "the first sighting stays: that is when the file stopped being the agent's")
	var v struct{ By string }
	require.NoError(t, json.Unmarshal(got[0], &v))
	assert.Equal(t, "job 500", v.By, "what a refusal names is the newest work that may be running")
}

// A stretch that differs is another stretch, and a cited change is never folded into an uncited one.
func TestCompactCitationPoints_DifferentStretchesAndCitedChangesStay(t *testing.T) {
	points := []json.RawMessage{
		uncited(t, "a", "b", 1, ""),
		uncited(t, "b", "a", 2, ""),
		uncited(t, "a", "b", 3, ""), // not the point before it: alternating states are three stretches
		cited(t, 4),
		cited(t, 5),
	}
	assert.Equal(t, []int{1, 2, 3, 4, 5}, atsOf(t, CompactCitationPoints(points)))
}

func TestCompactCitationPoints_EachKindKeepsItsNewest(t *testing.T) {
	var points []json.RawMessage
	for i := 1; i <= 400; i++ {
		points = append(points, uncited(t, fmt.Sprintf("f%d", i), fmt.Sprintf("t%d", i), i, ""))
		if i%2 == 0 {
			points = append(points, cited(t, 1000+i))
		}
	}
	got := CompactCitationPoints(points)
	uncitedKept, citedKept := 0, 0
	var lastUncited, lastCited int
	for _, at := range atsOf(t, got) {
		if at >= 1000 {
			citedKept++
			lastCited = at
		} else {
			uncitedKept++
			lastUncited = at
		}
	}
	assert.Equal(t, MaxUncitedPoints, uncitedKept)
	assert.Equal(t, 200, citedKept, "cited changes under their cap all stay: they are the grounds a refusal hands back")
	assert.Equal(t, 400, lastUncited, "it is the newest that stay")
	assert.Equal(t, 1400, lastCited)
	// Order is the history's order.
	ats := atsOf(t, got)
	for i := 1; i < len(ats); i++ {
		if ats[i-1] < 1000 && ats[i] < 1000 {
			assert.Less(t, ats[i-1], ats[i])
		}
	}
}

func TestBoundBy(t *testing.T) {
	long := strings.Repeat("x", 5000)
	var parts []string
	for i := 0; i < 30; i++ {
		parts = append(parts, fmt.Sprintf("job-%d %s", i, long))
	}
	got := BoundBy(strings.Join(parts, "; "))
	entries := strings.Split(got, "; ")
	assert.Len(t, entries, MaxByEntries)
	assert.True(t, strings.HasPrefix(entries[len(entries)-1], "job-29"), "the newest are the ones kept")
	for _, e := range entries {
		assert.LessOrEqual(t, len(e), MaxByEntryLen+len("…"))
	}
	assert.Equal(t, got, BoundBy(got), "bounding is idempotent")
	assert.Equal(t, "a; b", BoundBy("a; a; b"))
	cut := CutEntry("h" + strings.Repeat("é", 200))
	assert.True(t, utf8.ValidString(cut), "a cut never splits a character")
	assert.True(t, strings.HasSuffix(cut, "…"))
}

func TestCompactCitations_ABloatedHistoryShrinksAndKeepsWhatIsStillNeeded(t *testing.T) {
	long := strings.Repeat("z", 20000)
	history := map[string][]json.RawMessage{}
	for _, path := range []string{"a.md", "b.md", "gone.md"} {
		for i := 1; i <= 300; i++ {
			history[path] = append(history[path], uncited(t, "f", "t", i, "nohup "+long))
		}
	}
	history["a.md"] = append(history["a.md"], cited(t, 9000))
	history["gone.md"] = nil
	raw, err := json.Marshal(history)
	require.NoError(t, err)
	require.Greater(t, len(raw), 10_000_000)

	out, changed, err := CompactCitations(string(raw))
	require.NoError(t, err)
	require.True(t, changed)
	assert.Less(t, len(out), 5_000)

	var got map[string][]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Equal(t, []int{1, 9000}, atsOf(t, got["a.md"]), "the cited change, and the stretch it follows")
	assert.Len(t, got["b.md"], 1)
	assert.NotContains(t, got, "gone.md", "a path with no point left is not kept")

	again, changed, err := CompactCitations(out)
	require.NoError(t, err)
	assert.False(t, changed, "a compact history is left as it is")
	assert.Equal(t, out, again)
}

func TestCompactCycle(t *testing.T) {
	var by []string
	for i := 0; i < 50; i++ {
		by = append(by, fmt.Sprintf("cmd %d %s", i, strings.Repeat("y", 300)))
	}
	raw, err := json.Marshal(map[string]any{"state": "ended", "detached": true, "detachedBy": by, "tasks": true, "tasksBy": []string{"one"}, "end": map[string]any{"a.md": state("h")}})
	require.NoError(t, err)
	out, changed := CompactCycle(string(raw))
	require.True(t, changed)
	var got struct {
		State      string
		Detached   bool
		DetachedBy []string
		TasksBy    []string
		End        map[string]map[string]any
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Equal(t, "ended", got.State)
	assert.True(t, got.Detached)
	assert.Len(t, got.DetachedBy, MaxByEntries)
	assert.Equal(t, []string{"one"}, got.TasksBy)
	assert.Contains(t, got.End, "a.md", "how the agent left each file is not touched")
	_, changed = CompactCycle(out)
	assert.False(t, changed)
}

// A store written before the bound (user_version 8) with a history that grew to tens of megabytes is
// migrated by the engine on open: the history shrinks to the work in the tree, what is still owed (a
// cited change, a pending one, the cycle's marks) stays, and the file gives its space back.
func TestOpen_AFatStoreFromBeforeTheBoundIsCompactedAndKeepsWhatIsOwed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	old, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	files, err := migrationFiles()
	require.NoError(t, err)
	require.Equal(t, "009_compact_citations.sql", files[8], "the migration this test is about")
	for _, f := range files[:8] {
		body, err := migrationFS.ReadFile("migrations/" + f)
		require.NoError(t, err)
		_, err = old.Exec(string(body))
		require.NoError(t, err, f)
	}
	_, err = old.Exec(`PRAGMA user_version = 8`)
	require.NoError(t, err)

	long := strings.Repeat("n", 40000)
	history := map[string][]json.RawMessage{}
	for _, p := range []string{"memories/a.md", "memories/b.md", "memories/c.md"} {
		for i := 1; i <= 250; i++ {
			history[p] = append(history[p], uncited(t, "eb4d", "9ced", i, "nohup "+long))
		}
	}
	owed := cited(t, 77777)
	history["memories/a.md"] = append(history["memories/a.md"], owed)
	raw, err := json.Marshal(history)
	require.NoError(t, err)
	require.Greater(t, len(raw), 25_000_000)
	pending := `[{"path":"memories/p.md","abs":"/r/memories/p.md","point":{"before":{"exists":false},"after":{"exists":true,"hash":"x"},"at":5}}]`
	cycle, err := json.Marshal(map[string]any{"state": "ended", "detached": true, "detachedBy": []string{"nohup " + long}, "end": map[string]any{"memories/a.md": state("h")}})
	require.NoError(t, err)
	for k, v := range map[string]string{"citations": string(raw), "cited_pending": pending, "cited_cycle": string(cycle), "baseline_commit": "abc", "cited_content:h": "hello"} {
		_, err = old.Exec(`INSERT INTO meta (key, value) VALUES (?, ?)`, k, v)
		require.NoError(t, err)
	}
	require.NoError(t, old.Close())
	before, err := os.Stat(path)
	require.NoError(t, err)
	require.Greater(t, before.Size(), int64(25_000_000))

	s, err := Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })

	got, ok, err := s.Meta(MetaCitations)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Less(t, len(got), 10_000)
	var h map[string][]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(got), &h))
	assert.Equal(t, []int{1, 77777}, atsOf(t, h["memories/a.md"]), "the owed cited change stays beside the stretch before it")
	assert.Len(t, h["memories/b.md"], 1)

	for key, want := range map[string]string{"cited_pending": pending, "baseline_commit": "abc", "cited_content:h": "hello"} {
		v, ok, err := s.Meta(key)
		require.NoError(t, err)
		require.True(t, ok, key)
		assert.Equal(t, want, v, "%s is not this migration's", key)
	}
	cyc, _, err := s.Meta(MetaCitedCycle)
	require.NoError(t, err)
	assert.Contains(t, cyc, `"memories/a.md"`, "how the agent left each file stays")
	assert.Less(t, len(cyc), 1_000)

	after, err := os.Stat(path)
	require.NoError(t, err)
	assert.Less(t, after.Size(), int64(5_000_000), "the file gives back what the history held")

	// Opening it again changes nothing.
	require.NoError(t, s.Close())
	s2, err := Open(path)
	require.NoError(t, err)
	defer s2.Close()
	again, _, err := s2.Meta(MetaCitations)
	require.NoError(t, err)
	assert.Equal(t, got, again)
}

// A store that is already within the bound is not rewritten, and one with no history opens as before.
func TestOpen_AFreshStoreHasNothingToCompact(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer s.Close()
	_, ok, err := s.Meta(MetaCitations)
	require.NoError(t, err)
	assert.False(t, ok)
}
