package changeset

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wireOf is a payload as a check reads it on stdin.
func wireOf(t *testing.T, p Payload) map[string]any {
	t.Helper()
	raw, err := json.Marshal(p)
	require.NoError(t, err)
	var wire map[string]any
	require.NoError(t, json.Unmarshal(raw, &wire))
	return wire
}

// Units of execution come in order of first appearance: the subjects of one batch id
// together, in the order printed, and each subject without one on its own.
func TestBatches_GroupByIDInOrderOfFirstAppearance(t *testing.T) {
	got := Batches([]Subject{
		{ID: "a", Batch: "b1"}, {ID: "lone"}, {ID: "b", Batch: "b2"}, {ID: "c", Batch: "b1"}, {ID: "other"},
	})
	assert.Equal(t, []Group{
		{Batch: "b1", Subjects: []int{0, 3}},
		{Subjects: []int{1}},
		{Batch: "b2", Subjects: []int{2}},
		{Subjects: []int{4}},
	}, got)
	assert.Empty(t, Batches(nil))
}

// A subject handed alone keeps the payload rules have always had: `subject`, the default
// one included (its empty context must not make it disappear), and no `batch`.
// sr:proves fileguard/batch-payload-only-when-batched
func TestPayload_ASubjectHandedAloneKeepsTheSubjectPayload(t *testing.T) {
	cs := Changeset{Files: []File{{Path: "a.md"}}}
	wire := wireOf(t, NewPayload(cs, Whole(cs), ""))
	assert.Equal(t, map[string]any{"id": DefaultSubjectID, "files": []any{"a.md"}, "context": map[string]any{}}, wire["subject"])
	assert.NotContains(t, wire, "batch")

	wire = wireOf(t, NewPayload(cs, Subject{ID: "x", Files: []string{"a.md"}, Context: map[string]any{}}, ""))
	assert.Equal(t, map[string]any{"id": "x", "files": []any{"a.md"}, "context": map[string]any{}}, wire["subject"])
	assert.NotContains(t, wire, "batch")
}

// A batch is handed `batch` in place of `subject`, whatever its size: a batch of one
// included, so the shape a rule reads never depends on how many subjects a run left.
// sr:proves fileguard/batch-payload-only-when-batched
func TestPayload_ABatchIsHandedTheBatchPayloadEvenAlone(t *testing.T) {
	cs := Changeset{Files: []File{{Path: "a.md"}, {Path: "b.md"}}}
	for name, subs := range map[string][]Subject{
		"two": {{ID: "a", Files: []string{"a.md"}, Batch: "b1"}, {ID: "b", Files: []string{"b.md"}, Fingerprint: "f", Batch: "b1"}},
		"one": {{ID: "a", Files: []string{"a.md"}, Batch: "b1"}},
	} {
		t.Run(name, func(t *testing.T) {
			wire := wireOf(t, NewBatchPayload(cs, Batch{ID: "b1", Subjects: subs}, "/t.jsonl"))
			assert.NotContains(t, wire, "subject")
			assert.Equal(t, map[string]any{"kind": "Changeset"}, wire["event"])
			assert.Equal(t, "/t.jsonl", wire["transcriptPath"])
			b := wire["batch"].(map[string]any)
			assert.Equal(t, "b1", b["id"])
			entries := b["subjects"].([]any)
			require.Len(t, entries, len(subs))
			first := entries[0].(map[string]any)
			assert.Equal(t, map[string]any{"id": "a", "files": []any{"a.md"}, "context": map[string]any{}}, first,
				"each subject as it would be handed alone, its batch named once on the batch")
		})
	}
}

// A batch's checks are handed every citation that is evidence for one of its subjects, once.
func TestEvidenceForBatch_IsTheUnionOfItsSubjects(t *testing.T) {
	cs := Changeset{
		Files: []File{{Path: "a.md", Commits: []string{"c1"}}, {Path: "b.md", Commits: []string{"c2"}}, {Path: "c.md", Commits: []string{"c3"}}},
		Citations: []Citation{
			{Commits: []string{"c1", "c2"}}, {Commits: []string{"c2"}}, {Commits: []string{"c3"}},
		},
	}
	got := cs.EvidenceForBatch(Batch{Subjects: []Subject{{Files: []string{"a.md"}}, {Files: []string{"b.md"}}}})
	assert.Equal(t, []Citation{cs.Citations[0], cs.Citations[1]}, got)
}

// `batch` is read from the subjects script; an empty one is a bug in the script, not "no batch".
// sr:proves fileguard/subject-contract
func TestParseSubjects_Batch(t *testing.T) {
	cs := Changeset{Files: []File{{Path: "a.md"}, {Path: "b.md"}}}
	got, err := ParseSubjects([]byte(`[{"id":"x","files":["a.md"],"batch":"b1"},{"id":"y","files":["b.md"]}]`), cs)
	require.NoError(t, err)
	assert.Equal(t, "b1", got[0].Batch)
	assert.Empty(t, got[1].Batch)

	_, err = ParseSubjects([]byte(`[{"id":"x","files":["a.md"],"batch":""}]`), cs)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty batch")
}
