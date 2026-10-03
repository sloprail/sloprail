package commandmod

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Invocation.Env is the environment the line sets for a program: prefix,
// wrapper assignments and an earlier `export` in the same scope.

func envOf(t *testing.T, line, bin string) map[string]string {
	t.Helper()
	for _, inv := range ExtractCommand(line).Invocations {
		if inv.Bin == bin {
			return inv.Env
		}
	}
	t.Fatalf("no %s invocation in %q", bin, line)
	return nil
}

func TestInvocationEnv(t *testing.T) {
	cases := []struct {
		name, line, bin string
		want            map[string]string
	}{
		{"no assignment", "git commit -m x", "git", nil},
		{"prefix", "GIT_DIR=x git commit", "git", map[string]string{"GIT_DIR": "x"}},
		{"two prefixes", "A=1 B='2 3' git status", "git", map[string]string{"A": "1", "B": "2 3"}},
		{"a quoted message that mentions it sets nothing", `git commit -m "mentions GIT_DIR=x"`, "git", nil},
		{"non-literal value is empty", `GIT_DIR=$X git commit`, "git", map[string]string{"GIT_DIR": ""}},
		{"substitution value is empty", `GIT_DIR=$(pwd) git commit`, "git", map[string]string{"GIT_DIR": ""}},
		{"env wrapper", "env GIT_DIR=x git commit", "git", map[string]string{"GIT_DIR": "x"}},
		{"env wrapper with flags", "env -u A -C /w GIT_DIR=x git commit", "git", map[string]string{"GIT_DIR": "x"}},
		{"sudo wrapper", "sudo GIT_DIR=x git commit", "git", map[string]string{"GIT_DIR": "x"}},
		{"the wrapper's assignment is not an argument of the program", "env GIT_DIR=x git commit", "env", nil},
		{"prefix reaches the wrapped program", "A=1 env B=2 git commit", "git", map[string]string{"A": "1", "B": "2"}},
		{"export earlier on the line", "export GIT_DIR=x; git commit", "git", map[string]string{"GIT_DIR": "x"}},
		{"export in a && chain", "export GIT_DIR=x && git commit", "git", map[string]string{"GIT_DIR": "x"}},
		{"export with a quoted value", `export GIT_DIR="a b" && git commit`, "git", map[string]string{"GIT_DIR": "a b"}},
		{"export of a non-literal value", `export GIT_DIR=$X; git commit`, "git", map[string]string{"GIT_DIR": ""}},
		{"bare export names it", "export GIT_DIR; git commit", "git", map[string]string{"GIT_DIR": ""}},
		{"export survives a cd", "export GIT_DIR=x; cd /w; git commit", "git", map[string]string{"GIT_DIR": "x"}},
		{"export survives a block", "{ export GIT_DIR=x; }; git commit", "git", map[string]string{"GIT_DIR": "x"}},
		{"declare -x", "declare -x GIT_DIR=x; git commit", "git", map[string]string{"GIT_DIR": "x"}},
		{"typeset -x", "typeset -x GIT_DIR=x; git commit", "git", map[string]string{"GIT_DIR": "x"}},
		{"declare -gx", "declare -gx GIT_DIR=x; git commit", "git", map[string]string{"GIT_DIR": "x"}},
		{"declare -rx", "declare -rx GIT_DIR=x; git commit", "git", map[string]string{"GIT_DIR": "x"}},
		{"local -x", "f() { local -x GIT_DIR=x; git commit; }; f", "git", map[string]string{"GIT_DIR": "x"}},
		{"declare without -x exports nothing", "declare GIT_DIR=x; git commit", "git", nil},
		{"declare -g exports nothing", "declare -g GIT_DIR=x; git commit", "git", nil},
		{"an option that cannot be read fails closed", "declare $opt GIT_DIR=x; git commit", "git", map[string]string{"GIT_DIR": "x", "GIT_WORK_TREE": "", "GIT_INDEX_FILE": "", "GIT_COMMON_DIR": "", "GIT_OBJECT_DIRECTORY": "", "GIT_ALTERNATE_OBJECT_DIRECTORIES": "", "GIT_NAMESPACE": ""}},
		{"a name that cannot be read fails closed", `export "$n"=x; git commit`, "git", map[string]string{"GIT_DIR": "", "GIT_WORK_TREE": "", "GIT_INDEX_FILE": "", "GIT_COMMON_DIR": "", "GIT_OBJECT_DIRECTORY": "", "GIT_ALTERNATE_OBJECT_DIRECTORIES": "", "GIT_NAMESPACE": ""}},
		{"a function that exports applies after its definition", "f() { export GIT_DIR=x; }; f; git commit", "git", map[string]string{"GIT_DIR": "x"}},
		{"a function that declares -gx applies after its definition", "f() { declare -gx GIT_DIR=x; }; f; git commit", "git", map[string]string{"GIT_DIR": "x"}},
		{"a function with an unreadable declaration applies", "f() { declare $o GIT_DIR=x; }; f; git commit", "git", map[string]string{"GIT_DIR": "x", "GIT_WORK_TREE": "", "GIT_INDEX_FILE": "", "GIT_COMMON_DIR": "", "GIT_OBJECT_DIRECTORY": "", "GIT_ALTERNATE_OBJECT_DIRECTORIES": "", "GIT_NAMESPACE": ""}},
		{"a function's local -x does not leak", "f() { local -x GIT_DIR=x; }; f; git commit", "git", nil},
		{"a function's declare -x without -g does not leak", "f() { declare -x GIT_DIR=x; }; f; git commit", "git", nil},
		{"a subshell's export stays inside it", "(export GIT_DIR=x); git commit", "git", nil},
		{"inside the subshell it applies", "(export GIT_DIR=x; git commit)", "git", map[string]string{"GIT_DIR": "x"}},
		{"a pipeline side's export does not leak", "export A=1 | cat; git commit", "git", nil},
		{"export on one if branch is possible", "if x; then export GIT_DIR=a; fi; git commit", "git", map[string]string{"GIT_DIR": ""}},
		{"both branches agree", "if x; then export GIT_DIR=a; else export GIT_DIR=a; fi; git commit", "git", map[string]string{"GIT_DIR": "a"}},
		{"later export wins", "export A=1; export A=2; git commit", "git", map[string]string{"A": "2"}},
		{"the prefix overrides the export", "export A=1; A=2 git commit", "git", map[string]string{"A": "2"}},
		{"a payload inherits", "GIT_DIR=x sh -c 'git commit'", "git", map[string]string{"GIT_DIR": "x"}},
		{"an eval payload inherits the export", "export GIT_DIR=x; eval 'git commit'", "git", map[string]string{"GIT_DIR": "x"}},
		{"an export inside a literal eval", "eval 'export GIT_DIR=x'; git commit", "git", map[string]string{"GIT_DIR": "x"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := envOf(t, c.line, c.bin)
			if len(c.want) == 0 {
				assert.Empty(t, got)
				return
			}
			assert.Equal(t, c.want, got)
		})
	}
}

func TestInvocationEnvOnTheWire(t *testing.T) {
	ev := ExtractCommand("GIT_DIR=x git commit").Event()
	invs, ok := ev.Fields[FieldInvocations].([]any)
	require.True(t, ok)
	require.Len(t, invs, 1)
	m := invs[0].(map[string]any)
	assert.Equal(t, map[string]any{"GIT_DIR": "x"}, m[KeyEnv])

	// A program with nothing set carries an empty map, never a missing key.
	m = ExtractCommand("git status").Event().Fields[FieldInvocations].([]any)[0].(map[string]any)
	assert.Equal(t, map[string]any{}, m[KeyEnv])

	back, err := FromEvent(ExtractCommand("GIT_DIR=x git commit").Event())
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"GIT_DIR": "x"}, back.Invocations[0].Env)
}
