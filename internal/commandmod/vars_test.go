package commandmod

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A shell variable the line assigned earlier is read as its literal value; any
// other parameter (unset, from the real environment, a default, a command
// substitution) is unresolvable, and its place in the vector is recorded as a
// gap rather than the word being read as empty or shifted away.

func firstInv(t *testing.T, line, bin string) Invocation {
	t.Helper()
	for _, inv := range ExtractCommand(line).Invocations {
		if inv.Bin == bin {
			return inv
		}
	}
	t.Fatalf("no %s invocation in %q", bin, line)
	return Invocation{}
}

func TestVars_LiteralAssignmentsAreExpanded(t *testing.T) {
	cases := []struct {
		name, line string
		argv       []string
	}{
		{"semicolon", `D=/x; git -C $D push`, []string{"git", "-C", "/x", "push"}},
		{"and", `D=/x && git -C "$D" push`, []string{"git", "-C", "/x", "push"}},
		{"export", `export D=/x; git -C $D push`, []string{"git", "-C", "/x", "push"}},
		{"composed from a known variable", `A=/x; D=$A/y; git -C $D push`, []string{"git", "-C", "/x/y", "push"}},
		{"braced", `D=/x; git -C ${D} push`, []string{"git", "-C", "/x", "push"}},
		{"reassigned", `D=/a; D=/b; git -C $D push`, []string{"git", "-C", "/b", "push"}},
		{"a backgrounded block runs in a subshell", `D=/x; { D=/y; } & git -C $D push`, []string{"git", "-C", "/x", "push"}},
		{"both branches agree", `if t; then D=/x; else D=/x; fi; git -C $D push`, []string{"git", "-C", "/x", "push"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inv := firstInv(t, tc.line, "git")
			assert.Equal(t, tc.argv, inv.Argv)
			assert.Empty(t, inv.Gaps)
		})
	}
}

// sr:proves events/command-undecidable-not-guessed
func TestVars_UnresolvableWordsLeaveAGap(t *testing.T) {
	cases := []struct {
		name, line string
		argv       []string
		gaps       []int
	}{
		{"unset", `git -C $D push`, []string{"git", "-C", "push"}, []int{2}},
		{"quoted unset", `git -C "$D" push`, []string{"git", "-C", "push"}, []int{2}},
		{"command substitution", `git -C $(pwd)/x push`, []string{"git", "-C", "push"}, []int{2}},
		{"a default is not the line's own value", `git -C ${D:-/x} push`, []string{"git", "-C", "push"}, []int{2}},
		{"a prefix of an unset variable", `git -C $HOME/x push`, []string{"git", "-C", "push"}, []int{2}},
		{"trailing", `git push $REMOTE`, []string{"git", "push"}, []int{2}},
		{"assigned from a substitution", `D=$(pwd); git -C $D push`, []string{"git", "-C", "push"}, []int{2}},
		{"assigned from an unknown variable", `D=$X/y; git -C $D push`, []string{"git", "-C", "push"}, []int{2}},
		{"the two branches disagree", `if t; then D=/a; else D=/b; fi; git -C $D push`, []string{"git", "-C", "push"}, []int{2}},
		{"a pipeline side does not leak", `D=/x | cat; git -C $D push`, []string{"git", "-C", "push"}, []int{2}},
		{"a subshell does not leak", `(D=/x); git -C $D push`, []string{"git", "-C", "push"}, []int{2}},
		{"read forgets", `D=/x; read D; git -C $D push`, []string{"git", "-C", "push"}, []int{2}},
		{"unset forgets", `D=/x; unset D; git -C $D push`, []string{"git", "-C", "push"}, []int{2}},
		{"a loop variable", `for D in /a /b; do git -C $D push; done`, []string{"git", "-C", "push"}, []int{2}},
		{"a function in the line", `f() { D=/evil; }; D=/x; f; git -C $D push`, []string{"git", "-C", "push"}, []int{2}},
		{"an assignment inside an expansion", `D=/x; echo ${D:=/y}; git -C $D push`, []string{"git", "-C", "push"}, []int{2}},
		{"the prefix form does not apply to its own words", `D=/x git -C $D push`, []string{"git", "-C", "push"}, []int{2}},
		{"a conditional && assignment", `D=/x; false && D=/y; git -C $D push`, []string{"git", "-C", "push"}, []int{2}},
		{"a conditional || assignment", `D=/x; c || D=/y; git -C $D push`, []string{"git", "-C", "push"}, []int{2}},
		{"a conditional export", `D=/x; c && export D=/y; git -C $D push`, []string{"git", "-C", "push"}, []int{2}},
		{"a nameref", `declare -n D=Q; Q=/y; git -C $D push`, []string{"git", "-C", "push"}, []int{2}},
		{"a lowercasing declare", `declare -l D=/Y; git -C $D push`, []string{"git", "-C", "push"}, []int{2}},
		{"an integer declare", `declare -i D=3+4; git -C $D push`, []string{"git", "-C", "push"}, []int{2}},
		{"arithmetic inside an assignment", `D=/x; A=$((D=1)); git -C $D push`, []string{"git", "-C", "push"}, []int{2}},
		{"a function defined by eval", `D=/x; eval 'f() { D=/y; }'; f; git -C $D push`, []string{"git", "-C", "push"}, []int{2}},
		{"a sourced file may define one", `D=/x; source ./env.sh; git -C $D push`, []string{"git", "-C", "push"}, []int{2}},
		{"a later iteration reads what an earlier one assigned", `D=/x; for i in 1 2; do git -C $D push; D=/y; done`, []string{"git", "-C", "push"}, []int{2}},
		{"the same in a while loop", `D=/x; while c; do git -C $D push; read D; done`, []string{"git", "-C", "push"}, []int{2}},
		{"the last pipeline element runs in this shell under zsh", `D=/x; true | read D; git -C $D push`, []string{"git", "-C", "push"}, []int{2}},
		{"a trap assigns at any point", `D=/x; trap 'D=/y' DEBUG; git -C $D push`, []string{"git", "-C", "push"}, []int{2}},
		{"a backslash-quoted builtin", `D=/x; \read D <<< /y; git -C $D push`, []string{"git", "-C", "push"}, []int{2}},
		{"arithmetic operand of a test", `D=/x; [[ 1 -eq D=1 ]]; git -C $D push`, []string{"git", "-C", "push"}, []int{2}},
		{"a subscripted assignment", `D=/x; a[D=1]=q; git -C $D push`, []string{"git", "-C", "push"}, []int{2}},
		{"a := in a case word", `D=; case ${D:=/y} in *) ;; esac; git -C "$D" push`, []string{"git", "-C", "push"}, []int{2}},
		{"a := in a redirect", `D=; : > ${D:=/y}; git -C "$D" push`, []string{"git", "-C", "push"}, []int{2}},
		{"a quoted export assignment", `D=/x; export "D=/y"; git -C $D push`, []string{"git", "-C", "push"}, []int{2}},
		{"a quoted declare assignment", `D=/x; declare 'D=/y'; git -C $D push`, []string{"git", "-C", "push"}, []int{2}},
		{"a split quoted export", `D=/x; export D"=/y"; git -C $D push`, []string{"git", "-C", "push"}, []int{2}},
		{"find's placeholder", `find /y -name .git -exec git -C {} push \;`, []string{"git", "-C", "push"}, []int{2}},
		{"a backgrounded assignment sets nothing here", `D=/x & git -C $D push`, []string{"git", "-C", "push"}, []int{2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inv := firstInv(t, tc.line, "git")
			assert.Equal(t, tc.argv, inv.Argv)
			assert.Equal(t, tc.gaps, inv.Gaps)
		})
	}
}

func TestVars_CdToAKnownVariableResolves(t *testing.T) {
	assert.Equal(t, "/x", cwdOf(t, `D=/x; cd $D && git push`, "git"))
	assert.Equal(t, "/x", cwdOf(t, `D=/x && cd "$D" && git push`, "git"))
	assert.Equal(t, "", cwdOf(t, `cd $D && git push`, "git"))
	assert.Equal(t, "", cwdOf(t, `D=$(pwd); cd $D && git push`, "git"))
	assert.Equal(t, "", cwdOf(t, `cd ${D:-/x} && git push`, "git"))
}

func TestVars_GapsRoundTripThroughTheWireForm(t *testing.T) {
	ev := ExtractCommand(`git -C $D push`).Event()
	list, ok := ev.Fields[FieldInvocations].([]any)
	require.True(t, ok)
	entry, ok := list[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, []any{2}, entry[KeyGaps])

	back, err := FromEvent(ev)
	require.NoError(t, err)
	assert.Equal(t, []int{2}, back.Invocations[0].Gaps)
}

// A payload built from a variable the line assigned is as readable as a literal one.
func TestVars_AnInterpreterPayloadFromAKnownVariableIsUnwrapped(t *testing.T) {
	inv := firstInv(t, `D=/x; sh -c "git -C $D push"`, "git")
	assert.Equal(t, []string{"git", "-C", "/x", "push"}, inv.Argv)
	assert.Empty(t, inv.Gaps)
}

// A word lost between a wrapper and its program may be another wrapper (`$X` = `env -C /y`), or a
// later -C: the program's folder is unknown, whatever a literal option said before it.
func TestVars_ALostWordAfterAWrapperMakesTheFolderUnknown(t *testing.T) {
	for _, line := range []string{
		`env -C /x $A git push`,
		`sudo -D /x $A git push`,
		`nohup $X git push`,
		`command $X git push`,
	} {
		assert.Equal(t, "", cwdOf(t, line, "git"), line)
	}
	assert.Equal(t, "/x", cwdOf(t, `env $A -C /x git push`, "git"))
}

func TestVars_AQuotedDeclarationBuiltinStillAssigns(t *testing.T) {
	for _, line := range []string{`D=/x; \export D=/y; git -C $D push`, `D=/x; 'export' D=/y; git -C $D push`, `D=/x; "declare" D=/y; git -C $D push`} {
		inv := firstInv(t, line, "git")
		assert.Equal(t, []string{"git", "-C", "push"}, inv.Argv, line)
	}
}

func TestVars_AWrapperKeepsItsProgramWhenItsValueIsLost(t *testing.T) {
	inv := firstInv(t, `sudo -u $U git -C /x push`, "git")
	assert.Equal(t, []string{"git", "-C", "/x", "push"}, inv.Argv)
	assert.Equal(t, "", cwdOf(t, `env -C $D git push`, "git"))
}
