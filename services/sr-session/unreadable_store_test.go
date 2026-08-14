package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The guardrails DIRECTORY itself unreadable.
//
// One level above every fault the loader already reports. Invalid is about a
// declaration that could not be read; this is the folder holding all of them
// refusing to be listed, so LoadWith returns an error and NO declarations and
// NO invalids — there is nothing to enumerate.
//
// Both hook points then behaved as though the project had adopted no rules at
// all. The pre-tool point printed the error and fell into its `len(decls) == 0
// && len(invalid) == 0` branch, whose comment reads "nothing declared, or
// nothing readable. Either way there is no rule to enforce" — and those are not
// the same thing, which is exactly the conflation refuseForUnreadable was
// written to reject one level down. The Post side returned false, which held
// the mark but blocked nothing and told the agent nothing.
//
// The argument is refuseForUnreadable's, unchanged. "No evidence of what was
// guarded" is not "evidence nothing was guarded". A project whose .sloprail
// directory has bad permissions, or sits on a mount that has gone away, keeps
// every file it believes is a guardrail — and the engine decided on its own
// that an unlistable folder guards nothing, silently, because no channel at
// PreToolUse delivers text beside a permitted action.
//
// Distinguishable from the ordinary case in the one way that matters: a project
// with no .sloprail directory at all yields os.IsNotExist, which LoadWith
// already answers with (nil, nil, nil). That is a project that has adopted no
// rules, and it is not affected by any of this.

// unreadableGuardrailsDir stages a project that has a real guardrail and then
// makes the folder holding it unlistable.
func unreadableGuardrailsDir(t *testing.T, proj string) {
	t.Helper()
	const refuseEverything = `---
hooks:
  PreFileCreate:
    - hooks:
        - type: command
          command: ./refuse.sh
  TurnEnd:
    - hooks:
        - type: command
          command: ./refuse.sh
---

# Refuses
`
	guardrailDir(t, proj, "real", refuseEverything, map[string]string{"refuse.sh": alwaysRefuse})

	dir := filepath.Join(proj, ".sloprail", "guardrails")
	require.NoError(t, os.Chmod(dir, 0o000))
	// Restored so the test's own cleanup can remove the tree.
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
}

// TestPreTool_UnreadableGuardrailStoreRefuses.
//
// The project holds a rule bound to PreFileCreate. The folder cannot be listed,
// so the engine cannot know that — and permitted the write.
func TestPreTool_UnreadableGuardrailStoreRefuses(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root lists a 0000 directory, so the store cannot be made unreadable")
	}

	proj := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "seed.md"), []byte("s"), 0o644))
	unreadableGuardrailsDir(t, proj)
	t.Chdir(proj)

	var stdout, stderr bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetIn(bytes.NewReader([]byte(`{"cwd":` + jsonString(proj) +
		`,"tool_name":"Write","tool_input":{"file_path":` + jsonString(filepath.Join(proj, "new.md")) +
		`,"content":"hi"}}`)))
	require.NoError(t, runSessionPreTool(cmd, nil))

	assert.Contains(t, stdout.String(), `"permissionDecision":"deny"`,
		"the guardrails folder could not be listed, so the engine cannot know what the "+
			"project declared; permitting reads an unreadable store as approval")
}

// TestDispatch_UnreadableGuardrailStoreBlocksTheTurn.
//
// The same at the other hook point. Returning false held the read mark, which
// is bookkeeping — it blocked nothing and said nothing to the agent, so the
// turn ended with the cycle unjudged.
func TestDispatch_UnreadableGuardrailStoreBlocksTheTurn(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root lists a 0000 directory, so the store cannot be made unreadable")
	}

	proj := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "seed.md"), []byte("s"), 0o644))
	runGit(t, proj, "add", ".")
	runGit(t, proj, "commit", "-m", "base")
	unreadableGuardrailsDir(t, proj)

	store := openStore(t)
	baselineAt(t, store, proj)

	stdout, ran := dispatchOut(t, proj, store)

	assert.False(t, ran)
	assert.Contains(t, stdout, `"decision":"block"`,
		"a cycle whose guardrails could not be listed has judged nothing, and the agent must be told")
}

// TestPreTool_NoGuardrailDirectoryStillPermits.
//
// The control, and the boundary a fix must not cross. A project that has
// adopted no guardrails has no .sloprail directory, LoadWith answers
// (nil, nil, nil), and every action proceeds. If this ever fails, the fix above
// has turned "no rules" into "unreadable rules" and broken every project that
// does not use sloprail.
func TestPreTool_NoGuardrailDirectoryStillPermits(t *testing.T) {
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
		"a project with no guardrails at all is not a project whose guardrails could not be read")
}
