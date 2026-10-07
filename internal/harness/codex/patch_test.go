package codex

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/harness"
)

func TestParsePatch_EnvelopeShapes(t *testing.T) {
	ops, ok := parsePatch("*** Begin Patch\n*** Add File: a.txt\n+one\n+two\n*** Delete File: b.txt\n*** Update File: c.txt\n*** Move to: d.txt\n@@ func f\n ctx\n-old\n+new\n*** End of File\n*** End Patch\n")
	require.True(t, ok)
	require.Len(t, ops, 3)
	assert.Equal(t, fileOp{kind: opAdd, path: "a.txt", add: []string{"one", "two"}}, ops[0])
	assert.Equal(t, fileOp{kind: opDelete, path: "b.txt"}, ops[1])
	assert.Equal(t, "d.txt", ops[2].moveTo)
	require.Len(t, ops[2].hunks, 1)
	assert.Equal(t, hunk{context: "func f", old: []string{"ctx", "old"}, new: []string{"ctx", "new"}, atEOF: true}, ops[2].hunks[0])

	_, ok = parsePatch("just some text")
	assert.False(t, ok)
}

func TestApply_Hunks(t *testing.T) {
	before := "a\nb\nc\nb\nd\n"
	op := func(h ...hunk) fileOp { return fileOp{kind: opUpdate, hunks: h} }

	got, ok := op(hunk{old: []string{"c"}, new: []string{"C"}}).apply(before)
	assert.True(t, ok)
	assert.Equal(t, "a\nb\nC\nb\nd\n", got)

	got, ok = op(hunk{context: "c", old: []string{"b"}, new: []string{"B"}}).apply(before)
	assert.True(t, ok)
	assert.Equal(t, "a\nb\nc\nB\nd\n", got, "the @@ context line moves the search past it")

	got, ok = op(hunk{old: []string{"a"}, new: []string{"a", "a2"}}, hunk{old: []string{"d"}, new: nil}).apply(before)
	assert.True(t, ok)
	assert.Equal(t, "a\na2\nb\nc\nb\n", got, "hunks apply in order")

	got, ok = op(hunk{new: []string{"tail"}}).apply(before)
	assert.True(t, ok)
	assert.Equal(t, before+"tail\n", got, "a hunk with nothing to match appends")

	_, ok = op(hunk{old: []string{"zzz"}, new: []string{"y"}}).apply(before)
	assert.False(t, ok)

	got, ok = op(hunk{old: []string{"b  "}, new: []string{"B"}}).apply("a\nb\n")
	assert.True(t, ok, "trailing whitespace is forgiven the way Codex forgives it")
	assert.Equal(t, "a\nB\n", got)
}

func TestPatchEffects_MoveIsADeleteAndACreate(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "old.txt"), []byte("x\n"), 0o644))
	input := []byte(`{"command":"*** Begin Patch\n*** Update File: old.txt\n*** Move to: sub/new.txt\n@@\n-x\n+y\n*** End Patch"}`)
	assert.Equal(t, []harness.FileEffect{
		{Kind: harness.FileDelete, Path: filepath.Join(dir, "old.txt")},
		{Kind: harness.FileCreate, Path: filepath.Join(dir, "sub", "new.txt"), NewContent: "y\n", ResultKnown: true},
	}, patchEffects(input, dir), "relative paths resolve against the hook's cwd")
}

func TestPatchEffects_NotAPatchIsNothing(t *testing.T) {
	assert.Empty(t, patchEffects([]byte(`{"command":"ls"}`), "/"))
	assert.Empty(t, patchEffects([]byte(`not json`), "/"))
}
