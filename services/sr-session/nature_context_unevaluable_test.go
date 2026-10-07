package main

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/module/modules"
)

// sr:proves matching/context-unevaluable-does-not-enter
func TestContextMatchingEvents_UnevaluableMatchDoesNotEnterAndIsReported(t *testing.T) {
	reg, err := modules.Registry()
	require.NoError(t, err)

	var stderr bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetErr(&stderr)

	// `int("a.md")` compiles and then cannot be evaluated.
	broken := declaration.Context{
		Name: "broken",
		On:   []declaration.ContextTrigger{{Event: declaration.KindPostFileCreate, Match: `int(event.path) > 0`}},
	}
	events := []event.Event{{Kind: declaration.KindPostFileCreate, Fields: map[string]any{"path": "a.md"}}}

	matched := contextMatchingEvents(cmd, reg, broken, events, nil)
	assert.Empty(t, matched, "a match that cannot be evaluated does not enter the context")
	assert.Contains(t, stderr.String(), "could not be evaluated")
	assert.Contains(t, stderr.String(), "broken")

	// The nearest permitted neighbour: a sound match on the same event enters.
	stderr.Reset()
	sound := declaration.Context{
		Name: "sound",
		On:   []declaration.ContextTrigger{{Event: declaration.KindPostFileCreate, Match: `event.path == "a.md"`}},
	}
	assert.Len(t, contextMatchingEvents(cmd, reg, sound, events, nil), 1)
	assert.Empty(t, stderr.String())
}
