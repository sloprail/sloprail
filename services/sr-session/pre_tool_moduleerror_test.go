package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A module returning events ALONGSIDE an error, at the pre-tool point.
//
// module.Module's contract says outright that this happens and that "a caller
// must not discard" the events: one input a module could not make sense of is
// not a reason to drop the events it did produce from the rest. filemod's own
// Extract repeats the requirement.
//
// The new nature pre-tool dispatch takes the events FIRST and reports the error
// after — extractPreEvents appends every module's events before ever consulting
// its error (nature_pre_tool.go), the same discipline postEvents follows at
// Stop. This test drives that dispatch (runSessionPreTool) and pins the contract
// at the point where it can actually be observed: a refusal that does or does not
// arrive.
//
// Why it is a guardrail hole and not a tidiness complaint. `rm a.md b.md` is
// ONE tool call producing TWO file targets. If b.md cannot be stat'ed, the
// module reports a problem for b.md and a perfectly good PreFileDelete for
// a.md — and a caller that threw away the event for a.md would let the deletion
// of a.md through with only a line on stderr that at exit 0 reaches no agent at
// all.
//
// One unreadable path must not silence every rule about every other file the
// same command touches.

// preToolIn runs the pre-tool hook over a project with a pending tool call,
// returning stdout (where a denial is written) and stderr.
func preToolIn(t *testing.T, proj, toolName, toolInput string) (string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetIn(bytes.NewReader([]byte(`{"cwd":` + jsonString(proj) +
		`,"tool_name":` + jsonString(toolName) +
		`,"tool_input":` + toolInput + `}`)))
	require.NoError(t, runSessionPreTool(cmd, nil))
	return stdout.String(), stderr.String()
}

// jsonString quotes a value for embedding in the payload above. A temp path can
// contain characters JSON would object to, and building the payload by
// concatenation without quoting them is how a test starts failing on somebody
// else's machine.
func jsonString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// alwaysRefuse is a check that refuses every invocation, whatever it is handed on
// stdin — the control that turns "a refusal arrived" into proof the event reached
// the check.
const alwaysRefuse = "#!/bin/sh\ncat >/dev/null\necho '{\"reason\":\"nothing is deleted\"}'\nexit 1\n"

// TestPreTool_ModuleErrorDoesNotDiscardItsEvents.
//
// RE-VEHICLED onto the new nature format (was an old GUARDRAIL.md bound to
// PreFileDelete). The invariant is format-agnostic — it is a property of the
// pre-tool EXTRACT loop, not of any rule format — and the new dispatch's loop is
// exactly the one under test.
//
// The reproduction is a single `rm` naming two files: one ordinary, one inside
// a directory the process cannot traverse. lookAt returns `unknown` with an
// error for the second and `presentFile` for the first, so filemod returns one
// event and one error together — exactly the contract shape.
//
// The assertion is on the DENIAL, not on stderr. The error was always printed;
// what a test watching stderr cannot see is whether the event survived, and
// that is precisely why the defect went unnoticed. A PreFileDelete gate whose
// match selects guarded.md refuses the deletion of the bytes about to be lost, so
// a denial on stdout is proof the good PreFileDelete event reached the matching
// stage rather than being dropped alongside the errored one.
func TestPreTool_ModuleErrorDoesNotDiscardItsEvents(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root traverses a 0000 directory, so the unreadable path cannot be staged")
	}
	if runtime.GOOS == "windows" {
		t.Skip("permission bits do not produce an unstattable path here")
	}

	proj := initRepo(t)
	// The file the rule is actually about.
	require.NoError(t, os.WriteFile(filepath.Join(proj, "guarded.md"), []byte("x"), 0o644))

	// A gate on PreFileDelete that refuses the deletion of any markdown file: the
	// `rm` of guarded.md is exactly the event it must receive.
	writeGateYAML(t, proj, "nodelete", `on:
  - event: PreFileDelete
    match: event.path endsWith ".md"
checks:
  - script: ./refuse.sh
`, map[string]string{"refuse.sh": alwaysRefuse})

	// A path whose PARENT cannot be traversed, so lstat fails with EACCES rather
	// than ENOENT — which is what produces `unknown` instead of `absent`.
	locked := filepath.Join(proj, "locked")
	require.NoError(t, os.Mkdir(locked, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(locked, "other.md"), []byte("y"), 0o644))
	require.NoError(t, os.Chmod(locked, 0o000))
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	runGit(t, proj, "add", "-A")
	runGit(t, proj, "commit", "-m", "base")

	// The pre phase resolves a command's paths against the PROCESS's working
	// directory, not against the payload's cwd — extractCommand passes the path
	// the command line named straight to lookAt, with no root applied. So the
	// test has to stand where the harness would. Without this, both paths stat
	// as absent, the module returns no events and no error, and the test would
	// be asserting nothing at all.
	t.Chdir(proj)

	stdout, _ := preToolIn(t, proj, "Bash",
		`{"command":"rm guarded.md locked/other.md"}`)

	assert.Contains(t, stdout, `"permissionDecision":"deny"`,
		"the module produced a PreFileDelete for guarded.md alongside an error about the "+
			"unreadable path; discarding the slice let the guarded deletion through")
	assert.Contains(t, stdout, "nodelete", "the refusal must name the gate")
}
