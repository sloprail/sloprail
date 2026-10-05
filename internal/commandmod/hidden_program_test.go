package commandmod

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A wrapper whose own bare word (timeout's duration) was lost does not eat the
// program in its place: the program is still reported, with the gap before it.
func TestHidden_ALostWrapperWordDoesNotHideTheProgram(t *testing.T) {
	inv := firstInv(t, `timeout $T git push`, "git")
	assert.Equal(t, []string{"git", "push"}, inv.Argv)
	assert.Equal(t, []int{0}, inv.Gaps)
	assert.Equal(t, "", inv.Cwd)

	inv = firstInv(t, `timeout 5 git push`, "git")
	assert.Empty(t, inv.Gaps)
}

// `builtin cd` and `command cd` move the shell like `cd`: a resolved target is
// the directory of what follows, an unresolved one makes it unknown.
func TestHidden_BuiltinAndCommandCdMoveTheShell(t *testing.T) {
	cases := []struct{ line, cwd string }{
		{`builtin cd /x; git push`, "/x"},
		{`command cd /x; git push`, "/x"},
		{`command -p cd /x; git push`, "/x"},
		{`command -- cd /x; git push`, "/x"},
		{`builtin command cd /x; git push`, "/x"},
		{`builtin cd "$X"; git push`, ""},
		{`command cd "$X"; git push`, ""},
		{`command -v cd; git push`, "."},
	}
	for _, tc := range cases {
		t.Run(tc.line, func(t *testing.T) {
			assert.Equal(t, tc.cwd, firstInv(t, tc.line, "git").Cwd)
		})
	}
}

// `env -S 'cmd'` runs a command string: its literal payload is read, where and
// with the environment env gives it.
func TestHidden_EnvSplitStringPayloadIsRead(t *testing.T) {
	for _, line := range []string{
		`env -S 'git push'`, `env -i -S'git push'`, `env --split-string="git push"`, `env -u X -S "git push"`,
	} {
		t.Run(line, func(t *testing.T) {
			assert.Equal(t, []string{"git", "push"}, firstInv(t, line, "git").Argv)
		})
	}
	assert.Equal(t, "/x", firstInv(t, `env -C /x -S 'git push'`, "git").Cwd)
	for _, line := range []string{`env -S "$A"`, `env -S`, `env -- -S 'git push'`} {
		for _, inv := range ExtractCommand(line).Invocations {
			assert.NotEqual(t, "git", inv.Bin, line)
		}
	}
}
