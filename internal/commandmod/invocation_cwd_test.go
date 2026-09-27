package commandmod

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Invocation.Cwd is the directory each program runs in, as far as the line
// says. It reuses cwd.go's traversal, so these cases pin the invocation-side
// wiring (statement tracking, payload composition, the wire spelling) rather
// than re-testing every `cd` shape cwd_test.go already covers.

// cwdOf returns the Cwd of the first invocation of bin in the line, failing the
// test when the line has none.
func cwdOf(t *testing.T, line, bin string) string {
	t.Helper()
	for _, inv := range ExtractCommand(line).Invocations {
		if inv.Bin == bin {
			return inv.Cwd
		}
	}
	t.Fatalf("no %s invocation in %q", bin, line)
	return ""
}

func TestInvocationCwd(t *testing.T) {
	cases := []struct {
		name, line, bin, want string
	}{
		{"no cd is where the line started", "git clone https://x/y.git", "git", "."},
		{"absolute cd", "cd /work && git clone https://x/y.git", "git", "/work"},
		{"relative cd stays relative to the start", "cd sub && cat lib/a.js", "cat", "sub"},
		{"relative cds compose", "cd a && cd b && cat f", "cat", "a/b"},
		{"the cd itself runs before it moves", "cd /work && ls", "cd", "."},
		{"a pipeline's later program keeps the chain's cwd", "cd /w && cat f | head -n 5", "head", "/w"},
		{"a subshell's cd does not leak out", "(cd /inner && ls) && cat f", "cat", "."},
		{"inside the subshell it applies", "(cd /inner && cat f)", "cat", "/inner"},
		{"an unresolvable cd is unknown", `cd "$DIR" && cat f`, "cat", ""},
		{"cd - is unknown", "cd - && cat f", "cat", ""},
		{"pushd is unknown", "pushd /x && cat f", "cat", ""},
		{"a payload's cd composes onto the outer cwd", `cd /x && sh -c 'cd y && cat z'`, "cat", "/x/y"},
		{"a payload with no cd runs in the outer cwd", `cd /x && bash -c 'cat z'`, "cat", "/x"},
		{"an absolute cd in a payload wins", `cd /x && sh -c 'cd /abs && cat z'`, "cat", "/abs"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, cwdOf(t, tc.line, tc.bin))
		})
	}
}

// The wire form carries cwd beside bin/argv/flags, and FromEvent reads it back,
// so a script reading `.invocations[].cwd` sees what the module resolved.
func TestInvocationCwd_RoundTripsThroughTheWireForm(t *testing.T) {
	ev := ExtractCommand("cd /work && git clone https://x/y.git dest").Event()

	list, ok := ev.Fields[FieldInvocations].([]any)
	require.True(t, ok)
	require.Len(t, list, 2)
	git, ok := list[1].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "/work", git[KeyCwd])

	back, err := FromEvent(ev)
	require.NoError(t, err)
	require.Len(t, back.Invocations, 2)
	assert.Equal(t, "/work", back.Invocations[1].Cwd)
	assert.Equal(t, ".", back.Invocations[0].Cwd)
}
