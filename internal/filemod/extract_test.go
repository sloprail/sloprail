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
	// Markers is an empty list rather than nil: the content carries none, and
	// the field is present-and-empty on every kind that declares it.
	assert.Equal(t, FileEvent{Path: path, Content: "# Notes\n", Markers: []Marker{}}, f)
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

// --- markers ----------------------------------------------------------------

func TestExtract_CreateCarriesMarkersFromPendingContent(t *testing.T) {
	// The file does not exist, so the pending content is the only text there
	// is — and it is the text the write would leave behind.
	path := filepath.Join(t.TempDir(), "new.go")
	content := "package main\n// sr:blueprint pkg.Alpha\nfunc Alpha() {}\n"

	events, err := New().Extract(module.Input{
		module.InputPhase:   module.PhasePre,
		module.InputPayload: writePending(path, content),
	})
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, KindPreCreate, events[0].Kind)

	assert.Equal(t, []any{
		map[string]any{KeyMarkerKind: "blueprint", KeyMarkerFQN: "pkg.Alpha", KeyMarkerLine: 2},
	}, events[0].Fields[FieldMarkers])
}

func TestExtract_CreateWithNoMarkersCarriesAnEmptyList(t *testing.T) {
	// Present and empty, not absent. `len(markers) == 0` is how an author
	// writes a rule about unmarked code, and a field that vanished when empty
	// would make that expression error instead of holding.
	path := filepath.Join(t.TempDir(), "new.go")

	events, err := New().Extract(module.Input{
		module.InputPhase:   module.PhasePre,
		module.InputPayload: writePending(path, "package main\n"),
	})
	require.NoError(t, err)
	require.Len(t, events, 1)

	require.Contains(t, events[0].Fields, FieldMarkers, "the field is carried even when empty")
	markers := events[0].Fields[FieldMarkers]
	assert.NotNil(t, markers)
	assert.Empty(t, markers)
}

func TestExtract_CreateWithEmptyContentStillCarriesMarkers(t *testing.T) {
	// Content is omitted when empty (see TestExtract_CreateWithEmptyContent),
	// but markers is not — the two are carried by different rules, and markers
	// keys off the declaration rather than off emptiness.
	path := filepath.Join(t.TempDir(), "empty.go")

	events, err := New().Extract(module.Input{
		module.InputPhase:   module.PhasePre,
		module.InputPayload: writePending(path, ""),
	})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.NotContains(t, events[0].Fields, FieldContent)
	require.Contains(t, events[0].Fields, FieldMarkers)
	assert.Empty(t, events[0].Fields[FieldMarkers])
}

func TestExtract_UpdateMarkersDescribeTheBytesBeingREPLACED(t *testing.T) {
	// PreFileUpdate has no content field, so the markers come off disk — which
	// means they describe the PRE-WRITE state, not what the write would leave.
	// This test is built so the two answers differ: the file on disk carries
	// pkg.Old, the pending write carries pkg.New, and only one of them can be
	// reported.
	path := filepath.Join(t.TempDir(), "existing.go")
	onDisk := "package main\n// sr:blueprint pkg.Old\n"
	pending := "package main\n// sr:blueprint pkg.New\n// sr:docs pkg.Extra\n"
	require.NoError(t, os.WriteFile(path, []byte(onDisk), 0o644))

	events, err := New().Extract(module.Input{
		module.InputPhase:   module.PhasePre,
		module.InputPayload: writePending(path, pending),
	})
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, KindPreUpdate, events[0].Kind)

	assert.Equal(t, []any{
		map[string]any{KeyMarkerKind: "blueprint", KeyMarkerFQN: "pkg.Old", KeyMarkerLine: 2},
	}, events[0].Fields[FieldMarkers],
		"the bytes the write would REPLACE — a known limitation until PreFileUpdate carries its pending content")
}

func TestExtract_UpdateOfAnUnreadablePathStillProducesTheEvent(t *testing.T) {
	// A directory exists, so this is an update, and reading it as a file
	// fails. The event must still fire with the path: dropping it would be a
	// file event that silently never happens.
	dir := t.TempDir()

	events, err := New().Extract(module.Input{
		module.InputPhase:   module.PhasePre,
		module.InputPayload: writePending(dir, "x"),
	})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, KindPreUpdate, events[0].Kind)
	assert.Equal(t, dir, events[0].Fields[FieldPath])
	require.Contains(t, events[0].Fields, FieldMarkers)
	assert.Empty(t, events[0].Fields[FieldMarkers])
}

func TestExtract_UpdateKeepsBothOccurrencesOfOneFQN(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing.go")
	require.NoError(t, os.WriteFile(path,
		[]byte("// sr:blueprint pkg.Inv\nif !ok {}\n// sr:blueprint pkg.Inv\n"), 0o644))

	events, err := New().Extract(module.Input{
		module.InputPhase:   module.PhasePre,
		module.InputPayload: writePending(path, "x"),
	})
	require.NoError(t, err)
	require.Len(t, events, 1)

	markers, ok := events[0].Fields[FieldMarkers].([]any)
	require.True(t, ok)
	require.Len(t, markers, 2, "a list, not a mapping keyed by name")
}

func TestFileEvent_DeleteKindsCarryNoMarkersField(t *testing.T) {
	// A deletion has no text to read markers out of. A field that was always
	// empty would be one a rule could match on and never learn anything from.
	f := FileEvent{Path: "a.go", Markers: []Marker{{Kind: "k", FQN: "f", Line: 1}}}
	for _, kind := range []string{KindPreDelete, KindPostCreate, KindPostUpdate, KindPostDelete} {
		assert.NotContains(t, f.Event(kind).Fields, FieldMarkers,
			"%s does not declare markers, so it must not carry them — even when the struct holds some", kind)
	}
}

func TestFileEvent_RoundTripsMarkers(t *testing.T) {
	f := FileEvent{Path: "a.go", Markers: []Marker{
		{Kind: "blueprint", FQN: "pkg.A", Line: 3},
		{Kind: "docs", FQN: "pkg.A", Line: 9},
	}}
	back, err := FromEvent(f.Event(KindPreUpdate))
	require.NoError(t, err)
	assert.Equal(t, f.Markers, back.Markers)
}
