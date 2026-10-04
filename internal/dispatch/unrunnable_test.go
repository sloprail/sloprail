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

			exit := exitReq()
			exit.Dir, exit.Exit = dir, "./x.sh"
			done, reason, err := r.ExitContext(exit)
			require.NoError(t, err)
			assert.False(t, done, "a context exit that cannot run must keep the context active")
			named(reason)
		})
	}
}
