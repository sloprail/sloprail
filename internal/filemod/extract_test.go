package filemod

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/module"
)

// fakePending is a harness's pending action, built inline. Only the two
// methods the module actually reads are needed.
type fakePending struct {
	tool string
	args json.RawMessage
}

func (p fakePending) Tool() string               { return p.tool }
func (p fakePending) Arguments() json.RawMessage { return p.args }

// writePending is the payload for a Write-style tool naming a path.
func writePending(path, content string) fakePending {
	args, _ := json.Marshal(map[string]string{
		"file_path": path,
		"content":   content,
	})
	return fakePending{tool: "Write", args: args}
}

// --- the branches that need no disk -----------------------------------------

func TestExtract_PayloadIsNotPending(t *testing.T) {
	// A module reads what it recognises and ignores the rest: a payload this
	// module cannot read concerns it not at all, which is ordinary rather
	// than an error.
	for name, payload := range map[string]any{
		"absent":      nil,
		"a string":    "not a pending action",
		"a map":       map[string]any{"file_path": "a.md"},
		"raw json":    json.RawMessage(`{"file_path":"a.md"}`),
		"an int":      42,
		"wrong iface": struct{ Tool string }{Tool: "Write"},
	} {
		t.Run(name, func(t *testing.T) {
			in := module.Input{module.InputPhase: module.PhasePre}
			if payload != nil {
				in[module.InputPayload] = payload
			}

			events, err := New().Extract(in)
			require.NoError(t, err, "an unreadable payload is not an error")
			assert.Empty(t, events)
		})
	}
}

func TestExtract_EmptyInput(t *testing.T) {
	events, err := New().Extract(module.Input{})
	require.NoError(t, err)
	assert.Empty(t, events)
}

func TestExtract_MalformedArgumentsProduceNoEvents(t *testing.T) {
	// Arguments that will not decode name no file, so there is nothing for
	// this module to report — and nothing to fail about either.
	for name, args := range map[string]string{
		"not json":        `not json at all`,
		"truncated":       `{"file_path": "a.md"`,
		"json array":      `["a.md"]`,
		"json string":     `"a.md"`,
		"json null":       `null`,
		"empty":           ``,
		"wrong type path": `{"file_path": 42}`,
	} {
		t.Run(name, func(t *testing.T) {
			events, err := New().Extract(module.Input{
				module.InputPhase:   module.PhasePre,
				module.InputPayload: fakePending{tool: "Write", args: json.RawMessage(args)},
			})
			require.NoError(t, err, "malformed arguments are not this module's error")
			assert.Empty(t, events)
		})
	}
}

func TestExtract_NoFilePathProducesNoEvents(t *testing.T) {
	// A tool whose arguments name no file is not a file event.
	for name, args := range map[string]string{
		"absent":          `{"content":"x"}`,
		"empty string":    `{"file_path":"","content":"x"}`,
		"empty object":    `{}`,
		"other tool args": `{"command":"ls -la","description":"list"}`,
	} {
		t.Run(name, func(t *testing.T) {
			events, err := New().Extract(module.Input{
				module.InputPhase:   module.PhasePre,
				module.InputPayload: fakePending{tool: "Bash", args: json.RawMessage(args)},
			})
			require.NoError(t, err)
			assert.Empty(t, events)
		})
	}
}

func TestExtract_ToolNameIsNotConsulted(t *testing.T) {
	// Tool() exists to know how to read the arguments; nothing branches on it
	// today, so any tool naming a file_path produces a file event.
	for _, tool := range []string{"Write", "Edit", "NotebookEdit", "", "SomethingElse"} {
		events, err := New().Extract(module.Input{
			module.InputPhase: module.PhasePre,
			module.InputPayload: fakePending{
				tool: tool,
				args: json.RawMessage(`{"file_path":"does-not-exist.md","content":"x"}`),
			},
		})
		require.NoError(t, err, "tool %q", tool)
		require.Len(t, events, 1, "tool %q", tool)
	}
}

// --- the create/update fork, which needs a real file ------------------------

func TestExtract_NonexistentPathIsACreateCarryingContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.md")

	events, err := New().Extract(module.Input{
		module.InputPhase:   module.PhasePre,
		module.InputPayload: writePending(path, "# Notes\n"),
	})
	require.NoError(t, err)
	require.Len(t, events, 1)

	e := events[0]
	assert.Equal(t, KindPreCreate, e.Kind)
	assert.Equal(t, path, e.Fields[FieldPath])
	assert.Equal(t, "# Notes\n", e.Fields[FieldContent],
		"the file does not exist yet, so a rule has nowhere else to look")
}

func TestExtract_ExistingPathIsAnUpdateWithoutContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing.md")
	require.NoError(t, os.WriteFile(path, []byte("old\n"), 0o644))

	events, err := New().Extract(module.Input{
		module.InputPhase:   module.PhasePre,
		module.InputPayload: writePending(path, "new content\n"),
	})
	require.NoError(t, err)
	require.Len(t, events, 1)

	e := events[0]
	assert.Equal(t, KindPreUpdate, e.Kind)
	assert.Equal(t, path, e.Fields[FieldPath])
	assert.NotContains(t, e.Fields, FieldContent,
		"the file is on disk, so a hook can read it there rather than have it copied through")
}

func TestExtract_ExistingDirectoryCountsAsExisting(t *testing.T) {
	// exists() stats rather than checking for a regular file, so a path that
	// is a directory is treated as an update.
	dir := t.TempDir()

	events, err := New().Extract(module.Input{
		module.InputPhase:   module.PhasePre,
		module.InputPayload: writePending(dir, "x"),
	})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, KindPreUpdate, events[0].Kind)
}

func TestExtract_CreateWithEmptyContent(t *testing.T) {
	// Creating an empty file: the content field is omitted entirely, because
	// FileEvent.Event only sets it when non-empty. So PreFileCreate does not
	// always carry content, despite being the kind that declares it.
	path := filepath.Join(t.TempDir(), "empty.md")

	events, err := New().Extract(module.Input{
		module.InputPhase:   module.PhasePre,
		module.InputPayload: writePending(path, ""),
	})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, KindPreCreate, events[0].Kind)
	assert.NotContains(t, events[0].Fields, FieldContent)
}

func TestExtract_DefaultsToPendingWhenPhaseIsUnset(t *testing.T) {
	// Anything that is not the post phase is read as pending.
	path := filepath.Join(t.TempDir(), "new.md")
	for name, phase := range map[string]any{
		"unset":    nil,
		"pre":      module.PhasePre,
		"garbage":  "not-a-phase",
		"wrong ty": 42,
	} {
		t.Run(name, func(t *testing.T) {
			in := module.Input{module.InputPayload: writePending(path, "x")}
			if phase != nil {
				in[module.InputPhase] = phase
			}

			events, err := New().Extract(in)
			require.NoError(t, err)
			require.Len(t, events, 1)
			assert.Equal(t, KindPreCreate, events[0].Kind)
		})
	}
}

func TestExtract_PostPhaseProducesNothingYet(t *testing.T) {
	// extractObserved is a TODO: comparing the tree against the session's
	// starting point is not implemented, so the post phase reports nothing.
	// This pins the placeholder, and will need updating when it lands.
	events, err := New().Extract(module.Input{
		module.InputPhase:   module.PhasePost,
		module.InputPayload: writePending("anything.md", "x"),
	})
	require.NoError(t, err)
	assert.Empty(t, events)
}

func TestExtract_RoundTripsThroughFromEvent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.md")

	events, err := New().Extract(module.Input{
		module.InputPhase:   module.PhasePre,
		module.InputPayload: writePending(path, "# Notes\n"),
	})
	require.NoError(t, err)
	require.Len(t, events, 1)

	f, err := FromEvent(events[0])
	require.NoError(t, err)
	assert.Equal(t, FileEvent{Path: path, Content: "# Notes\n"}, f)
}

func TestExtract_ExtraArgumentKeysAreIgnored(t *testing.T) {
	// What else a harness puts in a tool's arguments is its business.
	path := filepath.Join(t.TempDir(), "new.md")
	args, err := json.Marshal(map[string]any{
		"file_path":  path,
		"content":    "x",
		"old_string": "irrelevant",
		"nested":     map[string]any{"a": 1},
	})
	require.NoError(t, err)

	events, err := New().Extract(module.Input{
		module.InputPhase:   module.PhasePre,
		module.InputPayload: fakePending{tool: "Edit", args: args},
	})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, path, events[0].Fields[FieldPath])
}
