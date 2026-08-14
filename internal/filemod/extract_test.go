package filemod

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/guardrail"
	"github.com/sloprail/sloprail/internal/module"
)

// fakePending is a harness's pending action, built inline. Only the methods
// the module actually reads are needed.
type fakePending struct {
	tool string
	args json.RawMessage
	root string
}

func (p fakePending) Tool() string               { return p.tool }
func (p fakePending) Arguments() json.RawMessage { return p.args }
func (p fakePending) Root() string               { return p.root }

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
	// Tool() exists to know how to read the arguments; nothing branches on it,
	// so any tool naming a file_path produces a file event. This is ACCEPTED
	// behaviour, not a pending defect — extractPending's doc comment argues it.
	// The property being locked down is drift-immunity: a tool renamed upstream
	// must go on producing events, which is what a name allowlist would break.
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

// TestExtractPending_ReadOnlyToolStillProducesAnEvent is the accepted cost of
// the choice above, measured rather than asserted. `Read` carries a file_path
// and no content, so it yields a PreFileCreate for a file it only reads.
//
// Named for what is true. If a future change makes this stop happening — by
// asking whether the arguments carry a `content` key, per extractPending's
// closing note — this test is the one to rewrite, deliberately.
func TestExtractPending_ReadOnlyToolStillProducesAnEvent(t *testing.T) {
	events, err := New().Extract(module.Input{
		module.InputPhase: module.PhasePre,
		module.InputPayload: fakePending{
			tool: "Read",
			args: json.RawMessage(`{"file_path":"does-not-exist.md","offset":10,"limit":50}`),
		},
	})
	require.NoError(t, err)
	require.Len(t, events, 1, "Read carries file_path, so it reaches an event")
	assert.Equal(t, KindPreCreate, events[0].Kind)
}

// TestExtractPending_ShapeAlreadyFiltersToolsWithoutAFilePath is the other half
// of the defect-3 argument, and the half the report got wrong: Grep, Glob and
// WebFetch were said to produce bogus file events. They do not. None carries a
// `file_path`, so argument-shape dispatch drops them before any name is
// consulted — which is the evidence that shape does most of the filtering an
// allowlist was proposed to do.
// NotebookEdit was REMOVED from this table, deliberately, and the reason is
// worth stating where the table is.
//
// It sat here asserting that a notebook write produces no event, and that was
// not shape-filtering working — it was a measured coverage hole this test
// pinned. `notebook_path` is a path key like `file_path` is, so a notebook
// write is a file modification the Pre phase simply could not see, and
// `reflect-on-edits.sh` — the shell hook this engine replaced — matched
// NotebookEdit explicitly. The port was strictly narrower than its source until
// the path key was read.
//
// The corrected behaviour is pinned by
// TestExtractPending_ANotebookWriteIsAFileEventWithNoDerivableResult. What
// remains in this table is the genuine article: tools that name no path at all
// under any spelling.
func TestExtractPending_ShapeAlreadyFiltersToolsWithoutAFilePath(t *testing.T) {
	for tool, args := range map[string]string{
		"Grep":     `{"pattern":"foo","path":"/a"}`,
		"Glob":     `{"pattern":"**/*.go"}`,
		"WebFetch": `{"url":"https://example.com","prompt":"p"}`,
	} {
		events, err := New().Extract(module.Input{
			module.InputPhase:   module.PhasePre,
			module.InputPayload: fakePending{tool: tool, args: json.RawMessage(args)},
		})
		require.NoError(t, err, "tool %q", tool)
		assert.Emptyf(t, events, "tool %q names no file_path and must produce no event", tool)
	}
}

// TestExtractPending_AbsoluteFilePathIsReportedRelativeToTheWorkspace is the
// defect that made every path-matched rule inert against a real harness.
//
// A matcher is written `path startsWith "memories/"`, because that is the
// spelling a project uses for its own folders and the only one an author can
// write without knowing where the repo is checked out. The observed phase has
// always reported that spelling — extractObserved takes a root and reports
// paths relative to it. The pending phase did not: it passed `file_path`
// through exactly as the harness gave it.
//
// Claude Code gives it absolute. Measured, not assumed: every `file_path` in
// the operator's own session records is a full path from `/Users/...`. So a
// PreFileCreate arrived carrying
// `/Users/x/repo/memories/topics/t/TOPIC.md`, no matcher beginning
// `startsWith "memories/"` admitted it, and the rule sat in the project looking
// enforced while permitting everything — the exact silent no-op the engine
// exists to prevent, produced by the engine.
//
// It survived because the whole e2e suite drives the harness with RELATIVE
// `file_path` values (tests/e2e/harness/scenario.go), which is the one spelling
// real Claude Code never sends. Every Pre-kind test agreed with the bug.
//
// The two phases must report one spelling. A rule bound to PreFileCreate and
// PostFileCreate with the same matcher is one rule, and it cannot be written at
// all if the two kinds disagree about what a path looks like.
func TestExtractPending_AbsoluteFilePathIsReportedRelativeToTheWorkspace(t *testing.T) {
	root := t.TempDir()
	abs := filepath.Join(root, "memories", "topics", "t", "TOPIC.md")

	events, err := New().Extract(module.Input{
		module.InputPhase: module.PhasePre,
		module.InputPayload: fakePending{
			tool: "Write",
			args: mustArgs(abs, "body"),
			root: root,
		},
	})
	require.NoError(t, err)
	require.Len(t, events, 1)

	assert.Equal(t, filepath.ToSlash(filepath.Join("memories", "topics", "t", "TOPIC.md")),
		events[0].Fields["path"],
		"an absolute file_path must be reported relative to the workspace, as the observed phase already does — otherwise no matcher a project can write will admit it")
}

// TestExtractPending_PathOutsideTheWorkspaceKeepsItsAbsoluteSpelling: a write
// aimed outside the repo has no relative spelling that means anything, and
// inventing one — `../../etc/passwd` — would let it be admitted by a matcher
// written for a folder inside the project. It stays absolute, so a rule about
// `memories/` does not match it, which is the correct outcome.
func TestExtractPending_PathOutsideTheWorkspaceKeepsItsAbsoluteSpelling(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo")
	require.NoError(t, os.MkdirAll(root, 0o755))
	outside := filepath.Join(t.TempDir(), "elsewhere", "notes.md")

	events, err := New().Extract(module.Input{
		module.InputPhase: module.PhasePre,
		module.InputPayload: fakePending{
			tool: "Write",
			args: mustArgs(outside, "body"),
			root: root,
		},
	})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, filepath.ToSlash(outside), events[0].Fields["path"],
		"a path outside the workspace has no meaningful relative spelling and must not be given one")
}

// TestExtractPending_SymlinkedParentDoesNotProduceACleanRelativePath is the
// case a lexical containment test cannot see, and the reason reportable calls
// resolve rather than filepath.Rel.
//
// A repository containing `escape -> /somewhere/else`, written to at
// `<root>/escape/id_rsa`. filepath.Rel answers `escape/id_rsa`, which holds no
// `..` and passes every string check — so the event would carry a CLEAN
// RELATIVE PATH NAMING A FILE OUTSIDE THE REPOSITORY. A hook joins it against
// its own root and reads the outside file, and nothing reports a problem.
// presence.go calls this "the worse of the two" escapes for exactly that
// reason: there is no lexical tell.
//
// Measured before the fix: this returned "escape/id_rsa" for a file in an
// unrelated temp directory.
//
// The absolute spelling is the right answer, and it is not a half-measure. No
// project-relative matcher admits it, so the rule declines to speak about a
// write outside its subject rather than being handed a spelling that resolves
// somewhere it does not mean.
func TestExtractPending_SymlinkedParentDoesNotProduceACleanRelativePath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo")
	require.NoError(t, os.MkdirAll(root, 0o755))
	outside := t.TempDir()
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "escape")))

	through := filepath.Join(root, "escape", "id_rsa")
	events, err := New().Extract(module.Input{
		module.InputPhase: module.PhasePre,
		module.InputPayload: fakePending{
			tool: "Write",
			args: mustArgs(through, "body"),
			root: root,
		},
	})
	require.NoError(t, err)
	require.Len(t, events, 1)

	assert.NotEqual(t, "escape/id_rsa", events[0].Fields["path"],
		"a symlinked parent must not yield a clean relative path to a file outside the repository")
	assert.Equal(t, filepath.ToSlash(through), events[0].Fields["path"],
		"the honest answer is the absolute spelling, which no project-relative matcher admits")
}

// TestExtractPending_RelativeFilePathIsLeftAlone: a harness that already sends
// a workspace-relative path must not have it re-resolved. This is what the e2e
// suite sends, so breaking it would break every existing Pre-kind test.
func TestExtractPending_RelativeFilePathIsLeftAlone(t *testing.T) {
	root := t.TempDir()

	events, err := New().Extract(module.Input{
		module.InputPhase: module.PhasePre,
		module.InputPayload: fakePending{
			tool: "Write",
			args: mustArgs("memories/topics/t/TOPIC.md", "body"),
			root: root,
		},
	})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "memories/topics/t/TOPIC.md", events[0].Fields["path"])
}

// TestExtractPending_NoRootLeavesThePathAsGiven: a producer that names no root
// gets the old behaviour. Relativizing against a guess would be worse than not
// relativizing at all.
func TestExtractPending_NoRootLeavesThePathAsGiven(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "memories", "a.md")

	events, err := New().Extract(module.Input{
		module.InputPhase: module.PhasePre,
		module.InputPayload: fakePending{
			tool: "Write",
			args: mustArgs(abs, "body"),
		},
	})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, filepath.ToSlash(abs), events[0].Fields["path"])
}

// TestExtractPending_SymlinkedWorkspaceStillRelativizes: the root and the path
// may name one directory through different spellings, and on macOS they
// routinely do — `/tmp` is a symlink to `/private/tmp`, so a repo made with
// `mktemp -d` has a git root of `/private/tmp/x` while every path built from
// `$TMPDIR` reads `/tmp/x`.
//
// filepath.Rel is pure string arithmetic. Given those two it answers
// `../../tmp/x/memories/...`, which is outside-the-workspace by the test above,
// so the path keeps its absolute spelling and no matcher admits it — the rule
// goes silently inert again, for a reason that has nothing to do with the rule.
//
// Found by running the migrated guardrails in a scratch repo under /tmp, where
// both rules permitted a write they had just refused when the same repo was
// named by its resolved path.
func TestExtractPending_SymlinkedWorkspaceStillRelativizes(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	// The root as git resolves it (the real directory), the path as the harness
	// spells it (through the link) — the arrangement macOS produces by default.
	events, err := New().Extract(module.Input{
		module.InputPhase: module.PhasePre,
		module.InputPayload: fakePending{
			tool: "Write",
			args: mustArgs(filepath.Join(link, "memories", "a.md"), "body"),
			root: real,
		},
	})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "memories/a.md", events[0].Fields["path"],
		"a workspace reached through a symlink is the same workspace; a rule must not go inert on the spelling")
}

func mustArgs(path, content string) json.RawMessage {
	args, err := json.Marshal(map[string]string{"file_path": path, "content": content})
	if err != nil {
		panic(err)
	}
	return args
}

// TestExtract_DirectoryIsNotAnExistingFile is defect 2. os.Stat succeeding says
// something is at the path, not that a file is — so a write aimed at a
// directory reported PreFileUpdate, an event claiming a file is about to be
// modified when there is no file there and the write cannot land.
func TestExtract_DirectoryIsNotAnExistingFile(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "adir")
	require.NoError(t, os.Mkdir(sub, 0o755))

	events, err := New().Extract(module.Input{
		module.InputPhase:   module.PhasePre,
		module.InputPayload: writePending(sub, "x"),
	})
	require.NoError(t, err, "a directory in the way is not an extraction failure")
	assert.Empty(t, events,
		"a write onto a directory is not a file modification and must produce no event")
}

// TestExtract_RegularFileIsStillAnUpdate guards the tri-state from collapsing
// the other way — the directory fix must not turn ordinary updates into
// silence.
func TestExtract_RegularFileIsStillAnUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "real.md")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0o600))

	events, err := New().Extract(module.Input{
		module.InputPhase:   module.PhasePre,
		module.InputPayload: writePending(path, "new"),
	})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, KindPreUpdate, events[0].Kind)
}

// TestExtract_EmptyFileCreateCarriesContent is defect 1 through the real
// extraction path rather than through FileEvent alone: a Write of empty content
// to a path that does not exist must still carry `content`.
func TestExtract_EmptyFileCreateCarriesContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.txt")

	events, err := New().Extract(module.Input{
		module.InputPhase:   module.PhasePre,
		module.InputPayload: writePending(path, ""),
	})
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, KindPreCreate, events[0].Kind)
	require.Contains(t, events[0].Fields, FieldContent,
		"an empty file is still a file, and its kind declares content")
	assert.Equal(t, "", events[0].Fields[FieldContent])
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

func TestExtract_ExistingDirectoryIsNotAFileAndProducesNoEvent(t *testing.T) {
	// lookAt distinguishes a regular file from anything else at the path, so a
	// directory is neither a create nor an update: no file write can land on
	// it. This test previously asserted PreFileUpdate, pinning that defect.
	dir := t.TempDir()

	events, err := New().Extract(module.Input{
		module.InputPhase:   module.PhasePre,
		module.InputPayload: writePending(dir, "x"),
	})
	require.NoError(t, err)
	assert.Empty(t, events)
}

func TestExtract_CreateWithEmptyContentCarriesTheDeclaredContentField(t *testing.T) {
	// PreFileCreate declares content, so it carries content — including the
	// empty string. This test previously asserted the field was absent,
	// pinning the defect that made `content == ""` error rather than fire.
	path := filepath.Join(t.TempDir(), "empty.md")

	events, err := New().Extract(module.Input{
		module.InputPhase:   module.PhasePre,
		module.InputPayload: writePending(path, ""),
	})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, KindPreCreate, events[0].Kind)
	require.Contains(t, events[0].Fields, FieldContent)
	assert.Equal(t, "", events[0].Fields[FieldContent])
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

func TestExtract_PostPhaseIgnoresAPendingPayload(t *testing.T) {
	// The post phase reads an Observed, not a Pending. Handed the wrong one it
	// reports nothing rather than falling back to predicting from a tool call —
	// which is the whole distinction between the two halves.
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

func TestExtract_CreateWithEmptyContentCarriesBothDeclaredFields(t *testing.T) {
	// Content and markers are now carried by the SAME rule — the declaration —
	// so an empty create carries both, each holding its empty value. This test
	// used to assert content was absent while markers were present, pinning
	// the divergence between the two paths that has since been removed.
	path := filepath.Join(t.TempDir(), "empty.go")

	events, err := New().Extract(module.Input{
		module.InputPhase:   module.PhasePre,
		module.InputPayload: writePending(path, ""),
	})
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Contains(t, events[0].Fields, FieldContent)
	assert.Equal(t, "", events[0].Fields[FieldContent])
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

func TestExtract_UpdateOfAnUnreadableFileStillProducesTheEvent(t *testing.T) {
	// The intent this test has always carried: a file whose CONTENT cannot be
	// read must still produce its event, because a file event that silently
	// never happens is the failure this engine exists to prevent. Only the
	// fixture changed — it used to use a directory, which since the lookAt
	// tri-state is not a file at all and is covered by
	// TestExtract_ExistingDirectoryIsNotAFileAndProducesNoEvent. A real
	// unreadable regular file is the honest way to make markersOnDisk fail.
	if os.Geteuid() == 0 {
		t.Skip("root reads regardless of mode")
	}
	path := filepath.Join(t.TempDir(), "locked.go")
	require.NoError(t, os.WriteFile(path, []byte("// sr:blueprint pkg.Inv\n"), 0o644))
	require.NoError(t, os.Chmod(path, 0o000))
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })

	events, err := New().Extract(module.Input{
		module.InputPhase:   module.PhasePre,
		module.InputPayload: writePending(path, "x"),
	})
	require.NoError(t, err)
	require.Len(t, events, 1, "an unreadable file is still a file being updated")
	assert.Equal(t, KindPreUpdate, events[0].Kind)
	assert.Equal(t, path, events[0].Fields[FieldPath])
	require.Contains(t, events[0].Fields, FieldMarkers)
	assert.Empty(t, events[0].Fields[FieldMarkers],
		"unreadable text yields no markers rather than failing the extraction")
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

// TestExtract_EmptyFileMatcherActuallyFires is defect 1 at the level the user
// meets it, and the reason the defect was worse than it read.
//
// `content == ""` is the rule an author writes to catch an empty file. Against
// the old emitter the field was absent, so evaluation hit a nil where a string
// was declared and returned `interface conversion: nil, not string`. That is
// not a rule quietly failing to fire: since session_pre_tool refuses on a
// matcher error, every write of an empty file became a refusal blaming a
// guardrail that was correct.
//
// This is the assertion that fails loudly if the emitter ever again decides a
// field's presence from its value, and it does so through the real compiler
// against the real declaration rather than through a hand-built map.
func TestExtract_EmptyFileMatcherActuallyFires(t *testing.T) {
	var decl module.KindDecl
	for _, k := range (&Module{}).Kinds() {
		if k.Name == KindPreCreate {
			decl = k
		}
	}
	require.Equal(t, KindPreCreate, decl.Name, "PreFileCreate must be declared")

	m, err := guardrail.CompileMatcherFor(`content == ""`, decl)
	require.NoError(t, err)

	events, err := New().Extract(module.Input{
		module.InputPhase:   module.PhasePre,
		module.InputPayload: writePending(filepath.Join(t.TempDir(), "empty.txt"), ""),
	})
	require.NoError(t, err)
	require.Len(t, events, 1)

	admitted, err := m.Match(events[0])
	require.NoError(t, err, "a declared field must never evaluate to a nil the cast rejects")
	assert.True(t, admitted, `content == "" must fire for a genuinely empty file`)
}

// --- the spelling check runs before the dedupe can hide the breach (F-4) -----

func TestObserved_ABaselineKeyedRawIsCaughtInEitherOrder(t *testing.T) {
	// The guard was defeated by the dedupe three lines above it. seen[clean] was
	// marked before the check ran, so a producer listing both spellings
	// CLEAN-FIRST had the raw one — the only spelling that can produce the
	// disagreement — dropped before the check was ever reached. The breach
	// shipped as a create; reversing the two inputs caught it.
	//
	// Which made the guard order-dependent in exactly the way the defect it was
	// written against is: the same file in the same tree coming out a create or
	// an update by iteration order alone.
	root := tree(t, "dir/a.md")

	for name, paths := range map[string][]string{
		// "./dir/a.md" cleans to "dir/a.md", so both spellings name one file and
		// the dedupe collapses them. Only the RAW one can reach a baseline keyed
		// raw, and the order it arrives in must not decide whether it is asked.
		"clean first": {"dir/a.md", "./dir/a.md"},
		"raw first":   {"./dir/a.md", "dir/a.md"},
	} {
		t.Run(name, func(t *testing.T) {
			events, err := observeErr(fakeObserved{
				root:   root,
				paths:  paths,
				before: map[string]bool{filepath.FromSlash("./dir/a.md"): true}, // keyed raw
			})

			assert.Empty(t, events, "which of the two answers is true is what is in doubt")
			require.ErrorIs(t, err, ErrBaselineKeyedOnRawSpelling)
			assert.Len(t, strings.Split(err.Error(), "\n"), 1,
				"one file, one complaint, however many spellings named it")
		})
	}
}

func TestObserved_TheBaselineIsAskedOncePerFileNotOncePerSpelling(t *testing.T) {
	// Checking every spelling must not turn into interrogating the producer. The
	// canonical question is asked once per file and cached; only the raw probe,
	// which the contract already prices in, repeats per non-canonical spelling.
	root := tree(t, "dir/a.md")

	var asked []string
	events, err := New().Extract(module.Input{
		module.InputPhase: module.PhasePost,
		module.InputPayload: recordingObserved{
			fakeObserved: fakeObserved{
				root:   root,
				paths:  []string{"dir/a.md", "./dir/./a.md", "dir/a.md"},
				before: map[string]bool{filepath.Join("dir", "a.md"): true},
			},
			asked: &asked,
		},
	})

	require.NoError(t, err)
	require.Len(t, events, 1, "three spellings of one file are one event")
	assert.Equal(t, KindPostUpdate, events[0].Kind)

	var canonical int
	for _, a := range asked {
		if a == filepath.Join("dir", "a.md") {
			canonical++
		}
	}
	assert.Equal(t, 1, canonical, "one file, one canonical question")
}

// --- the two passes must agree, or a path vanishes (F5-1, F5-2) --------------

// movingObserved is a producer whose own ExistedAtBaseline call moves the tree,
// which is not a contrivance: the producer runs arbitrary code between the two
// passes by construction, and an agent's cycle is still finishing around it.
//
// The swap runs once, after the nth baseline question, which is what lets a test
// place it precisely between the first pass's decision about a path and the
// second pass's. Everything else is fakeObserved's.
type movingObserved struct {
	fakeObserved
	// after is how many baseline questions to answer before moving the tree.
	after int
	swap  func()
	asked int
}

func (o *movingObserved) ExistedAtBaseline(path string) bool {
	o.asked++
	if o.asked == o.after {
		o.swap()
	}
	return o.fakeObserved.ExistedAtBaseline(path)
}

// escapeRepo lays out a repository beside a directory outside it, and returns
// both. The outside directory is what a swapped-in symlink points at.
func escapeRepo(t *testing.T) (root, outside string) {
	t.Helper()
	parent := t.TempDir()
	root = filepath.Join(parent, "repo")
	outside = filepath.Join(parent, "outside")
	require.NoError(t, os.MkdirAll(root, 0o755))
	require.NoError(t, os.MkdirAll(outside, 0o755))
	return root, outside
}

func TestObserved_APathTheSecondPassRefusesIsStillReported(t *testing.T) {
	// The silent drop the two-pass rework introduced, and the one failure mode
	// this module says everywhere it exists to prevent.
	//
	// Both passes used to call resolve independently on the same path, and the
	// second one's `continue` was justified by "already reported by the first
	// pass, which refused it too" — true only when the two agree. They agree only
	// while the tree holds still, and the producer's own ExistedAtBaseline is
	// enough to move it: pass one resolves repo/dir as a real directory and asks
	// the baseline, the producer swaps the directory for a link outside, pass two
	// refuses the path and continues without a word.
	//
	// A delete that was owed then produced NO event and NO error — indistinguishable
	// from a tree that did not change, which is exactly what ErrNotADifference
	// exists to make impossible, reached by a route it does not cover.
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need a privilege this test will not assume on windows")
	}
	root, outside := escapeRepo(t)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "dir"), 0o755))
	path := filepath.FromSlash("dir/id_rsa")

	events, err := New().Extract(module.Input{
		module.InputPhase: module.PhasePost,
		module.InputPayload: &movingObserved{
			fakeObserved: fakeObserved{
				root:   root,
				paths:  []string{path},
				before: map[string]bool{path: true},
			},
			after: 1,
			swap: func() {
				// The escape appears underneath a path already resolved.
				require.NoError(t, os.RemoveAll(filepath.Join(root, "dir")))
				require.NoError(t, os.Symlink(outside, filepath.Join(root, "dir")))
			},
		},
	})

	// Whichever verdict the module reaches, it must reach ONE of them out loud.
	// Silence is the only answer that is wrong here: it says the cycle changed
	// nothing about a file the producer named as changed.
	if len(events) == 0 {
		require.Error(t, err,
			"a path that produces neither an event nor an error is a change that vanished")
	}
	require.Len(t, events, 1, "the path resolved when it was judged, so it is classified")
	assert.Equal(t, KindPostDelete, events[0].Kind,
		"the file was at the baseline and is not on disk: the delete is owed")
	assert.Equal(t, path, events[0].Fields[FieldPath])
}

func TestObserved_APathTheFirstPassRefusedCarriesNoZeroValueBaseline(t *testing.T) {
	// The mirror of the drop above, and it misclassifies rather than dropping.
	//
	// Pass one refuses the path, so it never asks the baseline and never writes
	// the map. Pass two then resolved it for itself and read baselines[clean] —
	// Go's zero value, false, for a file that WAS at the baseline. A delete or an
	// update ships as PostFileCreate, and every event is plausible on its face.
	//
	// baselinesFor already used the two-value read internally and discarded the
	// distinction at its boundary; telling "asked, answered false" from "never
	// asked" is the whole fix, and carrying the pass's decision forward is what
	// keeps it.
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need a privilege this test will not assume on windows")
	}
	root, outside := escapeRepo(t)
	// The escape is in place while the first pass runs, so "dir/a.md" is refused
	// there and no baseline is recorded for it.
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "dir")))
	require.NoError(t, os.WriteFile(filepath.Join(root, "trigger.md"), []byte("x\n"), 0o644))
	path := filepath.FromSlash("dir/a.md")

	events, err := New().Extract(module.Input{
		module.InputPhase: module.PhasePost,
		module.InputPayload: &movingObserved{
			fakeObserved: fakeObserved{
				// The trigger is last, so its baseline question fires after the
				// first pass has already refused the path above it.
				root:   root,
				paths:  []string{path, "trigger.md"},
				before: map[string]bool{path: true, "trigger.md": true},
			},
			after: 1,
			swap: func() {
				// The escape is replaced by a real directory holding the file, so
				// the path resolves for anyone who asks again.
				require.NoError(t, os.Remove(filepath.Join(root, "dir")))
				require.NoError(t, os.MkdirAll(filepath.Join(root, "dir"), 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(root, "dir", "a.md"), []byte("x\n"), 0o644))
			},
		},
	})

	require.ErrorIs(t, err, ErrPathNotRelativeToRoot, "the refusal is said, once")
	for _, e := range events {
		assert.NotEqual(t, KindPostCreate, e.Kind,
			"a file that was at the baseline is never a create, whatever the second pass sees")
	}
	require.Len(t, events, 1, "only the trigger, whose own path never moved")
	assert.Equal(t, "trigger.md", events[0].Fields[FieldPath])
}

func TestObserved_ARefusedPathIsReportedExactlyOnce(t *testing.T) {
	// The once-and-only-once property, which nothing asserted. Both directions of
	// getting it wrong survived every test: reporting a refusal in neither pass is
	// the silent drop above, and reporting it in both is the same path complaining
	// twice, which teaches a producer that one bad spelling is two problems.
	//
	// Repeated spellings are included because the count is per FILE and not per
	// mention: a producer that listed its one bad path four times has one thing
	// wrong with it.
	root := tree(t, "fine.md")

	events, err := observeErr(fakeObserved{
		root: root,
		paths: []string{
			"../escape.md", "fine.md", "../escape.md", "../escape.md",
		},
		before: map[string]bool{"fine.md": true},
	})

	require.ErrorIs(t, err, ErrPathNotRelativeToRoot)
	assert.Len(t, strings.Split(err.Error(), "\n"), 1,
		"one refused path, one complaint, however many times it was named")
	require.Len(t, events, 1, "and the good path beside it is unaffected")
	assert.Equal(t, "fine.md", events[0].Fields[FieldPath])
}

// --- a repeated path is one problem, not one per spelling (F5-5) -------------

func TestObserved_ARepeatedPathIsOneProblemWhicheverProblemItIs(t *testing.T) {
	// The dedupe's ORDERING, which no test held. Marking seen[clean] before the
	// classification is what makes a file named three times produce one complaint;
	// moved after either of the two problem branches, the same file complains once
	// per spelling and a producer reading the log sees three faults where it has
	// one.
	//
	// The existing repeated-path test only covers the no/no row, whose branch
	// happens to sit last. The lookAt failures sit ABOVE it, so a marking moved
	// just far enough to clear the no/no row still triples these — which is the
	// mutation that survived. Every problem branch is asserted here, so the
	// property is about the loop rather than about one row of it.
	root := tree(t)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "adir"), 0o755))

	for name, tc := range map[string]struct {
		paths    []string
		before   map[string]bool
		sentinel error
	}{
		"a directory where a file was expected": {
			paths:    []string{"adir", "adir", "./adir", filepath.FromSlash("dir/../adir")},
			sentinel: ErrPathIsNotAFile,
		},
		"a path that differs from nothing": {
			paths:    []string{"scratch.tmp", "scratch.tmp", "./scratch.tmp"},
			sentinel: ErrNotADifference,
		},
	} {
		t.Run(name, func(t *testing.T) {
			events, err := observeErr(fakeObserved{root: root, paths: tc.paths, before: tc.before})

			assert.Empty(t, events)
			require.ErrorIs(t, err, tc.sentinel)
			assert.Len(t, strings.Split(err.Error(), "\n"), 1,
				"one file, one complaint, whichever branch reported it")
		})
	}
}

func TestObserved_TheTreeIsLookedAtOncePerFile(t *testing.T) {
	// The other half of the same ordering, and the half an error count cannot see.
	// A file named four ways is stat'd once, because the marking happens before
	// anything can `continue` past it — a property the comment claims and nothing
	// measured.
	root := tree(t, "a.md")

	events := observe(t, fakeObserved{
		root:   root,
		paths:  []string{"a.md", "./a.md", filepath.FromSlash("dir/../a.md"), "a.md"},
		before: map[string]bool{"a.md": true},
	})

	require.Len(t, events, 1, "four spellings of one file are one event")
	assert.Equal(t, KindPostUpdate, events[0].Kind)
	assert.Equal(t, "a.md", events[0].Fields[FieldPath])
}
