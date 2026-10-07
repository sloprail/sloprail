package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func emit(t *testing.T, harnessName, text string) string {
	t.Helper()
	t.Setenv("SLOPRAIL_HARNESS", harnessName)
	cmd := newEmitContextCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetIn(strings.NewReader(text))
	require.NoError(t, cmd.Execute())
	return out.String()
}

// The start-up text is written in the running harness's own form: recorded runs show
// Codex injecting the hookSpecificOutput additionalContext document as a developer
// message, and Cursor reading {"additional_context"} (plain stdout is not its context).
func TestEmitContext_IsTheRunningHarnessesForm(t *testing.T) {
	const text = "sloprail is active.\nSecond line \"quoted\"."
	assert.JSONEq(t,
		`{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"sloprail is active.\nSecond line \"quoted\"."}}`,
		emit(t, "claude", text))
	assert.JSONEq(t,
		`{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"sloprail is active.\nSecond line \"quoted\"."}}`,
		emit(t, "codex", text))
	assert.JSONEq(t,
		`{"additional_context":"sloprail is active.\nSecond line \"quoted\"."}`,
		emit(t, "cursor", text))
}

func TestEmitContext_NothingToSayWritesNothing(t *testing.T) {
	for _, h := range []string{"claude", "codex", "cursor"} {
		assert.Empty(t, emit(t, h, "\n  \n"), h)
	}
}
