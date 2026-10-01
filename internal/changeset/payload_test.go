package changeset

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPayload_WireShapeIsTheDesignedOne(t *testing.T) {
	dir := initRepo(t)
	base := put(t, dir, "seed", map[string]string{"a.go": "1\n"})
	head := put(t, dir, "edit\n\nSloprail-Cites-User: q", map[string]string{"a.go": "2\n", "README.md": "r"})
	cs, err := Build(dir, rng(base, head), Options{Scan: scan, Select: func(s Scope) (bool, error) { return s.Path == "a.go", nil }})
	require.NoError(t, err)

	raw, err := json.Marshal(NewPayload(cs, Whole(cs), "/t.jsonl", nil))
	require.NoError(t, err)
	var wire map[string]any
	require.NoError(t, json.Unmarshal(raw, &wire))

	assert.Equal(t, map[string]any{"kind": "Changeset"}, wire["event"])
	assert.Equal(t, "/t.jsonl", wire["transcriptPath"])
	assert.Equal(t, map[string]any{}, wire["context"])
	assert.Equal(t, map[string]any{"id": "changeset", "files": []any{"a.go"}, "context": map[string]any{}}, wire["subject"], "range is omitted until subjects: exists")

	c := wire["changeset"].(map[string]any)
	for _, k := range []string{"base", "head", "commits", "files", "others", "citations"} {
		assert.Contains(t, c, k)
	}
	assert.Equal(t, []any{}, c["citations"], "a list, never null")
	commit := c["commits"].([]any)[0].(map[string]any)
	assert.Equal(t, map[string]any{"Sloprail-Cites-User": []any{"q"}}, commit["trailers"])
	file := c["files"].([]any)[0].(map[string]any)
	for _, k := range []string{"path", "status", "oldPath", "oldContent", "newContent", "oldMarkers", "newMarkers", "diff"} {
		assert.Contains(t, file, k)
	}
	assert.Equal(t, []any{map[string]any{"path": "README.md", "status": "A"}}, c["others"])
}

func TestWhole_IsEveryFileOfTheChangeset(t *testing.T) {
	s := Whole(Changeset{Files: []File{{Path: "a"}, {Path: "b"}}})
	assert.Equal(t, DefaultSubjectID, s.ID)
	assert.Equal(t, []string{"a", "b"}, s.Files)
	assert.Nil(t, s.Range)
	assert.Equal(t, []string{}, Whole(Changeset{}).Files)
}

func TestEnv_NamesTheSnapshotAndBothEnds(t *testing.T) {
	assert.Equal(t, []string{"SR_TREE=/tmp/t", "SR_BASE=b", "SR_HEAD=h"}, Env("/tmp/t", "b", "h"))
}

func TestTrailerScope_KeysAreCanonicalAndValuesKeepCommitOrder(t *testing.T) {
	got := TrailerScope([]Commit{
		{Trailers: map[string][]string{"K": {"1", "2"}}},
		{Trailers: map[string][]string{"K": {"3"}, "L": {"x"}}},
	})
	assert.Equal(t, map[string][]string{"K": {"1", "2", "3"}, "L": {"x"}}, got)
	assert.NotNil(t, TrailerScope(nil))
}
