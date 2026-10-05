package main

import (
	"os"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/sessionstate"
)

// The Stop reads only what the dispatching record gained since the last one: the cursor is kept in
// the store, and a record that grew is read from where it stood.
func TestSettle_TheRecordIsReadOnlyFromWhereTheLastStopStopped(t *testing.T) {
	f := newRegistryFixture(t)
	path := f.transcript("p", launchRecord("bg1")...)
	f.settle(path)
	st, err := os.Stat(path)
	require.NoError(t, err)
	raw, ok, err := f.store.Meta(sessionstate.MetaAgentSignalCursor)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Contains(t, raw, `"offset":`+strconv.FormatInt(st.Size(), 10))

	// Appended, the notification is heard from the cursor on.
	f1, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f1.WriteString(notificationRecord("bg1", "completed") + "\n")
	require.NoError(t, err)
	require.NoError(t, f1.Close())
	plan := f.settle(path)
	assert.False(t, plan.Waiting["bg1"])
	assert.Equal(t, sessionstate.AgentCompleted, f.agent("bg1").Status)
	assert.Empty(t, f.errs.String())
}
