package dispatch

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/natures"
)

// A declared script that lost its shebang or execute bit stays LOADED (the declaration package
// reports it as an environment fault), so the exec path is what stops the action: through every
// way a script is run (a gate or file-guard check, prepare, subjects, a prerequisite's `when`, a
// context's enter and exit) it must refuse, never permit, and name the file and the fix.

type brokenScript struct {
	name, body string
	mode       os.FileMode
	fix        string
}

var brokenScripts = []brokenScript{
	{"noexec", "#!/bin/sh\nexit 0\n", 0o644, "chmod +x"},
	{"noshebang", "exit 0\n", 0o755, "#!/usr/bin/env bash"},
}

func brokenIn(t *testing.T, b brokenScript) (dir string) {
	t.Helper()
	dir = t.TempDir()
	p := filepath.Join(dir, "x.sh")
	require.NoError(t, os.WriteFile(p, []byte(b.body), b.mode))
	require.NoError(t, os.Chmod(p, b.mode))
	return dir
}

func TestUnrunnableScript_RefusesInEveryRole(t *testing.T) {
	for _, b := range brokenScripts {
		t.Run(b.name, func(t *testing.T) {
			dir := brokenIn(t, b)
			r := Runner{}.withDefaults()
			req := gateReq(nil, nil)
			req.Dir = dir
			named := func(reason string) {
				t.Helper()
				assert.Contains(t, reason, "x.sh", "names the file")
				assert.Contains(t, reason, b.fix, "names the fix")
			}

			v, err := r.RunScript(req, declaration.Check{Script: "./x.sh"}, Prepared{})
			require.NoError(t, err)
			require.True(t, v.Refused, "a check script that cannot run permitted")
			named(v.Reason)

			_, v, err = r.RunSubjects(req, "./x.sh")
			require.NoError(t, err)
			require.True(t, v.Refused, "a subjects script that cannot run permitted")
			named(v.Reason)

			_, v, err = r.runPrepare(req, "./x.sh")
			require.NoError(t, err)
			require.True(t, v.Refused, "a prepare script that cannot run permitted")
			named(v.Reason)

			applies, _, err := r.prerequisiteApplies(req, "./x.sh")
			require.NoError(t, err)
			assert.True(t, applies, "a `when` that cannot run must not waive the prerequisite")

			enter := enterReq(natures.ContextState{})
			enter.Dir, enter.Enter = dir, "./x.sh"
			_, active, v, err := r.EnterContext(enter)
			require.NoError(t, err)
			require.True(t, v.Refused, "a context enter that cannot run was read as a decline")
			assert.False(t, active)
			named(v.Reason)
			assert.Contains(t, v.Reason, `context "goal-tracking": enter script ./x.sh cannot run`, "names the context")
			assert.Contains(t, v.Reason, "cannot be judged", "says why it is a refusal and not a decline")
			assert.NotContains(t, v.Reason, "check", "an enter is not a check")

			exit := exitReq()
			exit.Dir, exit.Exit = dir, "./x.sh"
			done, reason, err := r.ExitContext(exit)
			require.NoError(t, err)
			assert.False(t, done, "a context exit that cannot run must keep the context active")
			named(reason)
		})
	}
}

// A declared script is exec'd directly (a path plus plain arguments), never through `sh -c`: a
// chain, a pipe or a quoted path is refused before anything runs, and plain arguments arrive.
func TestRunScriptExec_NoShellSyntax(t *testing.T) {
	dir := t.TempDir()
	mark := filepath.Join(dir, "ran")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.sh"), []byte("#!/bin/sh\ncat >/dev/null\necho \"$1 $2\" > "+mark+"\n"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.sh"), []byte("#!/bin/sh\ntouch "+mark+".b\n"), 0o755))
	for _, s := range []string{"./a.sh && ./b.sh", "./a.sh | cat", `"./a.sh"`, "./a.sh; ./b.sh", "./a.sh $(./b.sh)"} {
		res, err := runScriptExec(scriptCall{Dir: dir, Script: s})
		require.NoError(t, err)
		assert.False(t, res.Passed, s)
		assert.True(t, res.Unrunnable, s)
		assert.Contains(t, res.Reason, "plain arguments", s)
	}
	assert.NoFileExists(t, mark, "a refused script string must not run any part of itself")
	assert.NoFileExists(t, mark+".b")

	res, err := runScriptExec(scriptCall{Dir: dir, Script: "./a.sh one two"})
	require.NoError(t, err)
	assert.True(t, res.Passed, res.Reason)
	got, _ := os.ReadFile(mark)
	assert.Equal(t, "one two\n", string(got))
}

// The control: an enter that RUNS and exits non-zero is a decline, never a refusal; and one
// whose declared file is gone is unrunnable (refused), not a decline.
func TestEnterContext_DeclineIsNotARefusalButMissingIs(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "no.sh"), []byte("#!/bin/sh\nexit 1\n"), 0o755))
	r := Runner{}.withDefaults()
	req := enterReq(natures.ContextState{})
	req.Dir, req.Enter = dir, "./no.sh"
	_, active, v, err := r.EnterContext(req)
	require.NoError(t, err)
	assert.False(t, v.Refused, "a script that ran and declined refused")
	assert.False(t, active)

	req.Enter = "./gone.sh"
	_, _, v, err = r.EnterContext(req)
	require.NoError(t, err)
	require.True(t, v.Refused, "a missing enter read as a decline")
	assert.Contains(t, v.Reason, "gone.sh")
}

func TestContextScriptFault(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ok.sh"), []byte("#!/bin/sh\n"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "noexec.sh"), []byte("#!/bin/sh\n"), 0o644))
	assert.NoError(t, ContextScriptFault(dir, "./ok.sh"))
	assert.NoError(t, ContextScriptFault(dir, "./ok.sh arg"))
	assert.ErrorContains(t, ContextScriptFault(dir, "./noexec.sh"), "chmod +x")
	assert.ErrorContains(t, ContextScriptFault(dir, "./missing.sh"), "not found")
	assert.NoError(t, ContextScriptFault(dir, ""))
}
