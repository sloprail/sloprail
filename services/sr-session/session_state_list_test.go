package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/sessionstate"
)

// These tests drive the CLI's own resolution path — openSessionState, which
// reads the engine-set environment for the database's coordinates, and
// listEntries, which turns --owner into either the caller-scoped read or the
// named-owner read. They exist to pin the one property the store layer cannot
// state on its own: that --owner reaches another GUARDRAIL and never another
// SESSION or WORKSPACE, because the database openSessionState resolves is keyed
// by session and workspace alone and --owner touches only the guardrail column.

// plantOwnerEntry opens the session database for the given coordinates directly
// — the same path openSessionState would resolve — and writes one entry under
// the owner's name, so a later --owner read has something to find (or fail to
// find, across a boundary).
func plantOwnerEntry(t *testing.T, workspace, session, owner, key, value string) {
	t.Helper()
	path, err := sessionDBPath(workspace, session)
	require.NoError(t, err)
	store, err := sessionstate.Open(path)
	require.NoError(t, err)
	defer store.Close()
	require.NoError(t, store.SetState(owner, key, value))
}

// readOwnerList runs the CLI's own read for a caller in the given session and
// workspace, asking for owner's entries — exactly what `state list --owner
// <owner>` does once the flag is parsed.
func readOwnerList(t *testing.T, workspace, session, caller, owner, prefix string) []sessionstate.Entry {
	t.Helper()
	t.Setenv(WorkspaceEnv, workspace)
	t.Setenv(SessionEnv, session)
	t.Setenv(GuardrailEnv, caller)

	store, guardrail, err := openSessionState()
	require.NoError(t, err)
	defer store.Close()

	entries, err := listEntries(store, guardrail, owner, prefix)
	require.NoError(t, err)
	return entries
}

func TestListEntries_OwnerReadsAcrossGuardrailsWithinTheSession(t *testing.T) {
	// The capability: a caller in one session reads a DIFFERENT guardrail's
	// entries in that same session. This is the CLI path, not the store method
	// in isolation.
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	ws := t.TempDir()

	plantOwnerEntry(t, ws, "sess-1", "registry-owner", "reg:a", `{"kw":["x"]}`)

	entries := readOwnerList(t, ws, "sess-1", "reader", "registry-owner", "")
	require.Len(t, entries, 1)
	assert.Equal(t, "reg:a", entries[0].Key)
	assert.Equal(t, `{"kw":["x"]}`, entries[0].Value)
}

func TestListEntries_EmptyOwnerIsCallerScoped(t *testing.T) {
	// Without --owner the read is the caller's own, unchanged. The caller reads
	// its own entry and not the owner's, even though both live in the one
	// session database.
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	ws := t.TempDir()

	plantOwnerEntry(t, ws, "sess-1", "registry-owner", "reg:a", "owner-value")
	plantOwnerEntry(t, ws, "sess-1", "reader", "own:x", "reader-value")

	entries := readOwnerList(t, ws, "sess-1", "reader", "" /* no --owner */, "")
	require.Len(t, entries, 1)
	assert.Equal(t, "own:x", entries[0].Key, "the caller's own read must return its own entry, not the owner's")
	assert.Equal(t, "reader-value", entries[0].Value)
}

func TestListEntries_OwnerCannotReadAnotherSession(t *testing.T) {
	// The fail-closed property that matters most. Session 1 holds the owner's
	// entry; a caller in session 2 asks for the SAME owner and gets nothing,
	// because the database is keyed by session and session 2's is a different
	// database. --owner selects a guardrail within the caller's own database and
	// has no way to name another session's.
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	ws := t.TempDir()

	plantOwnerEntry(t, ws, "sess-1", "registry-owner", "secret:x", "SESSION-1-ONLY")

	// Control: within session 1 the entry is readable, so an empty read in
	// session 2 is isolation and not a store that never wrote.
	same := readOwnerList(t, ws, "sess-1", "reader", "registry-owner", "")
	require.Len(t, same, 1)
	assert.Equal(t, "SESSION-1-ONLY", same[0].Value)

	other := readOwnerList(t, ws, "sess-2", "reader", "registry-owner", "")
	assert.Empty(t, other, "--owner read reached across sessions: it must not")
}

func TestListEntries_OwnerCannotReadAnotherWorkspace(t *testing.T) {
	// The other half of the boundary: same session id, different workspace. The
	// database is keyed by workspace too, so naming the owner from workspace B
	// reaches nothing workspace A wrote.
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	wsA := t.TempDir()
	wsB := t.TempDir()

	plantOwnerEntry(t, wsA, "sess-1", "registry-owner", "secret:x", "WORKSPACE-A-ONLY")

	// Control in workspace A.
	same := readOwnerList(t, wsA, "sess-1", "reader", "registry-owner", "")
	require.Len(t, same, 1)
	assert.Equal(t, "WORKSPACE-A-ONLY", same[0].Value)

	other := readOwnerList(t, wsB, "sess-1", "reader", "registry-owner", "")
	assert.Empty(t, other, "--owner read reached across workspaces: it must not")
}

func TestListEntries_OwnerWithNoEntriesIsEmpty(t *testing.T) {
	// A named owner that never wrote in this session is an empty read through the
	// CLI path, matching the store method — the gate gets "nothing declared", not
	// an error.
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	ws := t.TempDir()

	plantOwnerEntry(t, ws, "sess-1", "someone-else", "k", "v")

	entries := readOwnerList(t, ws, "sess-1", "reader", "never-wrote", "")
	assert.Empty(t, entries)
}

func TestOpenSessionState_OutsideAHookErrors(t *testing.T) {
	// --owner does not change the precondition: the CLI still needs a guardrail
	// in scope, because without SR_GUARDRAIL there is no caller identity for the
	// unscoped read and no session to resolve the database from. A bare call
	// errors rather than reading anything.
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv(WorkspaceEnv, t.TempDir())
	t.Setenv(SessionEnv, "sess-1")
	t.Setenv(GuardrailEnv, "") // no guardrail in scope

	_, _, err := openSessionState()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no guardrail in scope")
}
