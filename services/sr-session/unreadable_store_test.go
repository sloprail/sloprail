package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/module/modules"
)

// The new-format declaration directory itself unreadable.
//
// One level above every fault the loader already reports. An Invalid is about a
// declaration that could not be read; this is the `.sloprail` folder holding all
// of them refusing to be listed, so the store's Resolve returns an error and NO
// declarations and NO invalids — there is nothing to enumerate.
//
// The engine must not read that as "the project adopted no rules". "No evidence
// of what was guarded" is not "evidence nothing was guarded": a project whose
// `.sloprail` directory has bad permissions, or sits on a mount that has gone
// away, keeps every file it believes is a guard. So newNatureDeclarations reports
// the failure ("the new-format declarations in this project could not be read")
// and treats the store as empty rather than refusing every action the agent
// cannot repair.
//
// Distinguishable from the ordinary case in the one way that matters: a project
// with no `.sloprail` directory at all yields os.IsNotExist, which the store
// answers with an empty resolution and no error. That is a project that has
// adopted no rules, and it is not affected by any of this.

// unreadableDeclarationDir stages a project that has a real new-format file-guard
// and then makes the `.sloprail` directory holding it unlistable.
func unreadableDeclarationDir(t *testing.T, proj string) {
	t.Helper()
	writeFileGuardYAML(t, proj, "real", `match: "**/*.md"
checks:
  - script: ./refuse.sh
`, map[string]string{"refuse.sh": alwaysRefuse})

	dir := filepath.Join(proj, DotDirName)
	require.NoError(t, os.Chmod(dir, 0o000))
	// Restored so the test's own cleanup can remove the tree.
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
}

// TestPreTool_UnreadableDeclarationStoreDoesNotRefuse.
//
// RE-VEHICLED onto the new nature format (was an old `.sloprail/guardrails`
// store). The project holds a rule and the folder cannot be listed, so the engine
// cannot know what was declared. It reports and permits: an invalid guardrail
// blocks nothing, and a store that will not list is the same failure as a
// declaration that will not parse, one level up.
//
// A directory whose permissions are wrong is not something the agent can put
// right, so refusing its every action stalls the session without protecting
// anything.
func TestPreTool_UnreadableDeclarationStoreDoesNotRefuse(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root lists a 0000 directory, so the store cannot be made unreadable")
	}

	proj := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "seed.md"), []byte("s"), 0o644))
	unreadableDeclarationDir(t, proj)
	t.Chdir(proj)

	var stdout, stderr bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetIn(bytes.NewReader([]byte(`{"cwd":` + jsonString(proj) +
		`,"tool_name":"Write","tool_input":{"file_path":` + jsonString(filepath.Join(proj, "new.md")) +
		`,"content":"hi"}}`)))
	require.NoError(t, runSessionPreTool(cmd, nil))

	assert.NotContains(t, stdout.String(), `"permissionDecision":"deny"`,
		"an unlistable declaration folder must not refuse the action")
	assert.Contains(t, stderr.String(), "could not be read",
		"the failure must be reported even though it is not enforced")
}

// TestDispatch_UnreadableDeclarationStoreDoesNotBlockTheTurn.
//
// The same at the Stop hook point, and the same answer. The cycle is unjudged and
// the turn ends anyway, because holding it asks the agent to fix a directory's
// permissions — which it cannot do, and which no number of retries will change.
func TestDispatch_UnreadableDeclarationStoreDoesNotBlockTheTurn(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root lists a 0000 directory, so the store cannot be made unreadable")
	}

	proj := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "seed.md"), []byte("s"), 0o644))
	runGit(t, proj, "add", ".")
	runGit(t, proj, "commit", "-m", "base")
	unreadableDeclarationDir(t, proj)
	t.Chdir(proj)

	reg, err := modules.Registry()
	require.NoError(t, err)

	var stdout bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetOut(&stdout)

	reason := natureDispatchStop(cmd, HookPayload{Cwd: proj}, reg)

	assert.Empty(t, reason,
		"an unlistable declaration folder must not produce a block reason")
	assert.NotContains(t, stdout.String(), `"decision":"block"`,
		"an unlistable declaration folder must not hold the turn")
}

// TestPreTool_NoDeclarationDirectoryStillPermits.
//
// The control, and the boundary a fix must not cross. A project that has adopted
// no guards has no `.sloprail` directory, the store resolves to nothing, and every
// action proceeds. If this ever fails, the fix above has turned "no rules" into
// "unreadable rules" and broken every project that does not use sloprail.
func TestPreTool_NoDeclarationDirectoryStillPermits(t *testing.T) {
	proj := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "seed.md"), []byte("s"), 0o644))
	t.Chdir(proj)

	var stdout, stderr bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetIn(bytes.NewReader([]byte(`{"cwd":` + jsonString(proj) +
		`,"tool_name":"Write","tool_input":{"file_path":` + jsonString(filepath.Join(proj, "new.md")) +
		`,"content":"hi"}}`)))
	require.NoError(t, runSessionPreTool(cmd, nil))

	assert.Empty(t, stdout.String(),
		"a project with no guards at all is not a project whose guards could not be read")
}
