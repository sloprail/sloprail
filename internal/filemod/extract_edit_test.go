package filemod

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/guardrail"
	"github.com/sloprail/sloprail/internal/module"
)

// editPending is the payload for an Edit-style tool: a replacement, not a body.
//
// Built with the key set Claude Code actually sends, measured across the
// operator's 8,458-transcript corpus: every one of the 5,880 Edit calls carries
// exactly {file_path, old_string, new_string, replace_all}. `content` is not
// among them, which is the whole defect this file is about.
func editPending(path, oldS, newS string) fakePending {
	args, _ := json.Marshal(map[string]any{
		"file_path":   path,
		"old_string":  oldS,
		"new_string":  newS,
		"replace_all": false,
	})
	return fakePending{tool: "Edit", args: args}
}

// --- the measured defect ----------------------------------------------------

// TestExtractPending_EditCreatingAFileCarriesItsRealContent is the bug, as
// measured through `sr-session pre-tool` against a real repository.
//
// An `Edit` whose `old_string` is empty and whose target does not exist is a
// CREATION, and its resulting body is `new_string` outright. The module decoded
// `tool_input` into a struct holding `content`, a key `Edit` does not carry, so
// Content silently decoded to "" and the event announced an empty file.
//
// The three measurements, before the fix:
//
//	Write new.md   {"content":"FULL NEW BODY"}                      PreFileCreate content="FULL NEW BODY"  correct
//	Edit exists.md {"old_string":"line one","new_string":"..."}     PreFileUpdate no content               correct
//	Edit other.md  {"old_string":"","new_string":"CREATED BY EDIT"} PreFileCreate content=""               WRONG
//
// Why it is worse than a missing field. The operator's live guardrail
// `frontmatter-transcript-path` binds PreFileCreate and refuses a file whose
// frontmatter is absent. A file created by Edit was judged empty and refused
// whatever it actually contained — so the rule refused correct work and named
// the author's guardrail for the engine's gap.
//
// And it is the same wrong extractCommand refuses to commit. That comment
// declines to emit `content: ""` for a Bash-created file precisely because it
// "would make `echo x > new.md` indistinguishable from a tool writing a
// genuinely empty file". The identical argument applies here and was violated.
func TestExtractPending_EditCreatingAFileCarriesItsRealContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "other.md")

	events, err := New().Extract(module.Input{
		module.InputPhase:   module.PhasePre,
		module.InputPayload: editPending(path, "", "CREATED BY EDIT"),
	})
	require.NoError(t, err)
	require.Len(t, events, 1)

	e := events[0]
	require.Equal(t, KindPreCreate, e.Kind, "an Edit onto a path that does not exist creates it")
	require.Contains(t, e.Fields, FieldNewContent)
	assert.Equal(t, "CREATED BY EDIT", e.Fields[FieldNewContent],
		"the resulting body is new_string, not the empty string")
}

// TestExtractPending_EditCreateIsDistinguishableFromAGenuinelyEmptyFile is the
// distinction the defect erased, asserted as a difference rather than as two
// separate values.
//
// `newContent == ""` is the exact rule an author writes to catch a genuinely
// empty file. Before the fix it fired on every Edit-created file whatever the
// body, so the rule meant nothing. Both halves are checked here because the
// property is that the two differ — pinning only the non-empty case would let a
// fix that reports every Edit-create as non-empty pass.
func TestExtractPending_EditCreateIsDistinguishableFromAGenuinelyEmptyFile(t *testing.T) {
	dir := t.TempDir()

	m, err := guardrail.CompileMatcherFor(`newContent == ""`, preCreateDecl(t))
	require.NoError(t, err)

	for name, tc := range map[string]struct {
		payload fakePending
		empty   bool
	}{
		"an Edit whose new_string has a body": {
			payload: editPending(filepath.Join(dir, "body.md"), "", "CREATED BY EDIT"),
			empty:   false,
		},
		"an Edit whose new_string is empty": {
			payload: editPending(filepath.Join(dir, "genuinely-empty.md"), "", ""),
			empty:   true,
		},
		"a Write of an empty body": {
			payload: writePending(filepath.Join(dir, "written-empty.md"), ""),
			empty:   true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			events, err := New().Extract(module.Input{
				module.InputPhase:   module.PhasePre,
				module.InputPayload: tc.payload,
			})
			require.NoError(t, err)
			require.Len(t, events, 1)
			require.Equal(t, KindPreCreate, events[0].Kind)

			admitted, err := m.Match(events[0])
			require.NoError(t, err, "a declared field must never evaluate to a nil the cast rejects")
			assert.Equal(t, tc.empty, admitted,
				`newContent == "" must fire for an empty file and only for an empty file`)
		})
	}
}

// TestExtractPending_GenuineEmptyCreateIsResultKnown is the positive side of the
// underivable-create distinction: a create whose empty body was STATED (a Write
// of "", an Edit to "") is derivable, so resultKnown is TRUE — the empty file is
// KNOWN, and a pre-write gate may legitimately judge it. This is what a
// notebook create (resultKnown false, its cell source not the document) must be
// tellable from; the two share `newContent: ""` and differ only in this boolean.
func TestExtractPending_GenuineEmptyCreateIsResultKnown(t *testing.T) {
	dir := t.TempDir()
	for name, payload := range map[string]fakePending{
		"a Write of an empty body": writePending(filepath.Join(dir, "written-empty.md"), ""),
		"an Edit to an empty body": editPending(filepath.Join(dir, "edited-empty.md"), "", ""),
	} {
		t.Run(name, func(t *testing.T) {
			events, err := New().Extract(module.Input{
				module.InputPhase:   module.PhasePre,
				module.InputPayload: payload,
			})
			require.NoError(t, err)
			require.Len(t, events, 1)
			require.Equal(t, KindPreCreate, events[0].Kind)
			assert.Equal(t, "", events[0].Fields[FieldNewContent])
			assert.Equal(t, true, events[0].Fields[FieldResultKnown],
				"a STATED empty body is a known empty file — resultKnown true, unlike an underivable notebook create")
		})
	}
}

// TestExtractPending_EditCreateCarriesMarkersFromTheResultingBody holds that
// the markers follow the content rather than being computed separately. A
// create's markers come from the bytes the write would leave, and for an Edit
// those bytes are new_string.
func TestExtractPending_EditCreateCarriesMarkersFromTheResultingBody(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.go")

	events, err := New().Extract(module.Input{
		module.InputPhase: module.PhasePre,
		module.InputPayload: editPending(path, "",
			"package main\n// sr:blueprint pkg.New\n"),
	})
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, KindPreCreate, events[0].Kind)

	assert.Equal(t, []any{
		map[string]any{KeyMarkerKind: "blueprint", KeyMarkerFQN: "pkg.New", KeyMarkerLine: 2},
	}, events[0].Fields[FieldNewMarkers],
		"a create's markers are read out of the body it would leave behind")
}

// --- Q2: a replacement that cannot be applied -------------------------------

// TestExtractPending_EditWhoseOldStringIsAbsentFromTheFileProducesNoEvent pins
// Q2 in the direction that matters.
//
// The real tool refuses an `old_string` it cannot find, so the write does not
// happen. An event announcing a create or an update for an action that is about
// to fail is a FALSE EVENT: a rule fires, possibly refuses, and blames the agent
// for work the harness was never going to do. Worse, a judging hook costs a
// model call, so a false event spends real money on a decision about nothing.
//
// Silence is the same answer extractCommand gives for a path it cannot classify
// honestly, and the tree diff at session stop still reports whatever does land.
func TestExtractPending_EditWhoseOldStringIsAbsentFromTheFileProducesNoEvent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing.md")
	require.NoError(t, os.WriteFile(path, []byte("the actual body\n"), 0o644))

	events, err := New().Extract(module.Input{
		module.InputPhase:   module.PhasePre,
		module.InputPayload: editPending(path, "TEXT THAT IS NOT THERE", "replacement"),
	})
	require.NoError(t, err)
	assert.Empty(t, events,
		"the tool will refuse this edit, so predicting its effect would be a false event")
}

// TestExtractPending_EditWhoseOldStringIsAmbiguousProducesNoEvent is the other
// half of Q2, and it is a DIFFERENT failure from the one above.
//
// With `replace_all` false the tool refuses an `old_string` occurring more than
// once, because it cannot know which the author meant. The engine cannot know
// either — so it must not pick the first and report bytes the tool will never
// write.
func TestExtractPending_EditWhoseOldStringIsAmbiguousProducesNoEvent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing.md")
	require.NoError(t, os.WriteFile(path, []byte("dup\nmiddle\ndup\n"), 0o644))

	events, err := New().Extract(module.Input{
		module.InputPhase:   module.PhasePre,
		module.InputPayload: editPending(path, "dup", "changed"),
	})
	require.NoError(t, err)
	assert.Empty(t, events,
		"an ambiguous edit is refused by the tool, so its result is not derivable")
}

// TestExtractPending_EditWithReplaceAllAppliesEveryOccurrence is the case the
// ambiguity rule must NOT swallow. With `replace_all` true the tool applies the
// edit everywhere, so the result is fully determined and an event is owed.
//
// Without this the safe answer to ambiguity would be indistinguishable from
// refusing every repeated string, which would silence a legitimate, entirely
// derivable write.
func TestExtractPending_EditWithReplaceAllAppliesEveryOccurrence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing.md")
	require.NoError(t, os.WriteFile(path, []byte("dup\nmiddle\ndup\n"), 0o644))

	args, err := json.Marshal(map[string]any{
		"file_path":   path,
		"old_string":  "dup",
		"new_string":  "changed",
		"replace_all": true,
	})
	require.NoError(t, err)

	events, err := New().Extract(module.Input{
		module.InputPhase:   module.PhasePre,
		module.InputPayload: fakePending{tool: "Edit", args: args},
	})
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, KindPreUpdate, events[0].Kind)
	assert.Equal(t, "changed\nmiddle\nchanged\n", events[0].Fields[FieldNewContent],
		"replace_all makes every occurrence determined, so the result is derivable")
}

// TestExtractPending_EditOntoAnAbsentFileWithANonEmptyOldStringProducesNoEvent
// is the create-side of Q2, and it is a case the disk cannot answer by reading.
//
// `old_string` is non-empty and the file does not exist, so there is nothing for
// it to match. The tool fails. Reporting a create carrying `new_string` would
// announce a file that is not about to appear.
func TestExtractPending_EditOntoAnAbsentFileWithANonEmptyOldStringProducesNoEvent(t *testing.T) {
	events, err := New().Extract(module.Input{
		module.InputPhase: module.PhasePre,
		module.InputPayload: editPending(
			filepath.Join(t.TempDir(), "absent.md"), "not there", "replacement"),
	})
	require.NoError(t, err)
	assert.Empty(t, events, "there is no file for old_string to match, so the tool will fail")
}

// --- Q1: the post-edit result on an update ----------------------------------

// TestExtractPending_EditOnAnExistingFileCarriesTheResultingBytes is Q1's
// answer in force: PreFileUpdate carries `newContent`, the bytes the file will
// hold AFTER the edit, alongside `oldContent`, the bytes it holds now.
//
// The fixture is built so the pre-write and post-write answers differ, which is
// the only way to tell which one is being reported. `oldContent` and
// `oldMarkers` describe the bytes being REPLACED, while `newContent` is the
// resulting file.
func TestExtractPending_EditOnAnExistingFileCarriesTheResultingBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing.md")
	require.NoError(t, os.WriteFile(path, []byte("line one\nline two\n"), 0o644))

	events, err := New().Extract(module.Input{
		module.InputPhase:   module.PhasePre,
		module.InputPayload: editPending(path, "line one", "LINE ONE CHANGED"),
	})
	require.NoError(t, err)
	require.Len(t, events, 1)

	e := events[0]
	require.Equal(t, KindPreUpdate, e.Kind)
	require.Contains(t, e.Fields, FieldNewContent)
	assert.Equal(t, "LINE ONE CHANGED\nline two\n", e.Fields[FieldNewContent],
		"the whole resulting file, not just the replacement")
	assert.Equal(t, "line one\nline two\n", e.Fields[FieldOldContent],
		"oldContent is the file as it stands before the edit, distinct from the result")
}

// TestExtractPending_UpdateSaysWhetherItsResultIsKnown is the honest half of
// Q1, and it pins WHY the answer is a pair of fields rather than one.
//
// A `Write` onto an existing path replaces it wholesale, so its result is
// derivable. A Bash `sed -i` states a transformation the engine will not
// execute, so its result is not.
//
// The tempting design is to omit `newContent` in the second case. It does not
// work, and the reason is the engine's own: Matcher.env fills a DECLARED field
// the event omitted with its type's zero value — deliberately, because an absent
// declared field used to error, matcher errors refuse, and rules failed closed
// on the engine's gaps. So an omitted `newContent` reads as `""` inside a
// matcher, which is indistinguishable from a command that truly empties the
// file. That is the same absent-versus-empty collision as the original defect.
//
// Hence `resultKnown`. Both fields are always present; the boolean is what
// carries the distinction that absence could not.
func TestExtractPending_UpdateSaysWhetherItsResultIsKnown(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "existing.md")
	require.NoError(t, os.WriteFile(path, []byte("original\n"), 0o644))

	t.Run("a Write replaces the file wholesale, so the result is known", func(t *testing.T) {
		events, err := New().Extract(module.Input{
			module.InputPhase:   module.PhasePre,
			module.InputPayload: writePending(path, "brand new body\n"),
		})
		require.NoError(t, err)
		require.Len(t, events, 1)
		require.Equal(t, KindPreUpdate, events[0].Kind)
		assert.Equal(t, "brand new body\n", events[0].Fields[FieldNewContent])
		assert.Equal(t, true, events[0].Fields[FieldResultKnown])
	})

	t.Run("a sed -i states a transformation, so the result is not known", func(t *testing.T) {
		events, err := New().Extract(module.Input{
			module.InputPhase: module.PhasePre,
			module.InputPayload: fakePending{
				tool: "Bash",
				args: json.RawMessage(fmt.Sprintf(`{"command":"sed -i '' 's/a/b/' %s"}`, path)),
			},
		})
		require.NoError(t, err)
		require.Len(t, events, 1)
		require.Equal(t, KindPreUpdate, events[0].Kind)
		assert.Equal(t, false, events[0].Fields[FieldResultKnown],
			"the engine will not run sed, so it does not know the resulting bytes")
		require.Contains(t, events[0].Fields, FieldNewContent,
			"present but meaningless, because absence is not observable to a matcher")
	})
}

// TestExtractPending_ResultKnownIsWhatSeparatesUnknownFromEmptied is the pair
// doing the job it was declared for, through a real compiled matcher rather
// than by reading the map.
//
// Two updates whose `newContent` is the empty string for opposite reasons: one
// where the engine could not work it out, one where the file is genuinely being
// emptied. `newContent == ""` cannot tell them apart — that is the point — and
// `resultKnown` can.
//
// Without this test the pair could be reduced to a single field and the suite
// would still pass, which is exactly the mutation it exists to kill.
func TestExtractPending_ResultKnownIsWhatSeparatesUnknownFromEmptied(t *testing.T) {
	dir := t.TempDir()

	var decl module.KindDecl
	for _, k := range (&Module{}).Kinds() {
		if k.Name == KindPreUpdate {
			decl = k
		}
	}
	require.Equal(t, KindPreUpdate, decl.Name)

	emptied, err := guardrail.CompileMatcherFor(`resultKnown && newContent == ""`, decl)
	require.NoError(t, err)

	t.Run("a Write of an empty body genuinely empties the file", func(t *testing.T) {
		path := filepath.Join(dir, "emptied.md")
		require.NoError(t, os.WriteFile(path, []byte("had content\n"), 0o644))

		events, err := New().Extract(module.Input{
			module.InputPhase:   module.PhasePre,
			module.InputPayload: writePending(path, ""),
		})
		require.NoError(t, err)
		require.Len(t, events, 1)

		admitted, err := emptied.Match(events[0])
		require.NoError(t, err)
		assert.True(t, admitted, "the result is known, and it is empty")
	})

	t.Run("a sed -i is not a file being emptied", func(t *testing.T) {
		path := filepath.Join(dir, "sedded.md")
		require.NoError(t, os.WriteFile(path, []byte("had content\n"), 0o644))

		events, err := New().Extract(module.Input{
			module.InputPhase: module.PhasePre,
			module.InputPayload: fakePending{
				tool: "Bash",
				args: json.RawMessage(fmt.Sprintf(`{"command":"sed -i '' 's/a/b/' %s"}`, path)),
			},
		})
		require.NoError(t, err)
		require.Len(t, events, 1)

		admitted, err := emptied.Match(events[0])
		require.NoError(t, err)
		assert.False(t, admitted,
			"an unknown result must not be readable as an emptied file")
	})
}

// --- tool-name dispatch, not shape ------------------------------------------

// TestExtractPending_TheEditShapeIsReadOnlyForARecognisedWriteTool pins the
// CORRECTED, current contract in place of what this test used to assert
// (see git history: the old version proved the opposite — that any tool
// name carrying an edit shape was read, precisely to survive a rename like
// Claude Code's Task->Agent in v2.1.63).
//
// commandmod/harnesstools.go carries the argument for the reversal: this
// project chose to gate on tool name first, so the edit shape is now read
// ONLY for a tool already on HarnessWriteTools. `Edit` still works; a
// same-shaped call under an unrecognised name — including exactly the kind
// of upstream rename the old test was built to survive — now produces
// nothing until that name is added to the list.
func TestExtractPending_TheEditShapeIsReadOnlyForARecognisedWriteTool(t *testing.T) {
	dir := t.TempDir()

	t.Run("Edit is on the list", func(t *testing.T) {
		p := editPending(filepath.Join(dir, "Edit.md"), "", "BODY")
		p.tool = "Edit"
		events, err := New().Extract(module.Input{
			module.InputPhase:   module.PhasePre,
			module.InputPayload: p,
		})
		require.NoError(t, err)
		require.Len(t, events, 1)
		assert.Equal(t, "BODY", events[0].Fields[FieldNewContent])
	})

	t.Run("an unrecognised tool is not, whatever it carries", func(t *testing.T) {
		for _, tool := range []string{"StrReplace", "Update", "", "SomethingNobodyHasNamedYet"} {
			t.Run(tool, func(t *testing.T) {
				p := editPending(filepath.Join(dir, tool+".md"), "", "BODY")
				p.tool = tool

				events, err := New().Extract(module.Input{
					module.InputPhase:   module.PhasePre,
					module.InputPayload: p,
				})
				require.NoError(t, err)
				assert.Empty(t, events, "tool %q is not on HarnessWriteTools", tool)
			})
		}
	})
}

// TestExtractPending_ContentWinsOverAnEditShapeWhenBothArePresent pins the
// precedence, and it is load-bearing because a real payload carries both.
//
// TestExtract_ExtraArgumentKeysAreIgnored sends {file_path, content,
// old_string, nested} — a Write-shaped payload with a stray old_string. A
// dispatch preferring the edit shape would read that as an edit with no
// new_string and change a passing test's meaning. `content` is the tool stating
// the whole resulting body outright, which is strictly more information than a
// replacement, so it wins.
func TestExtractPending_ContentWinsOverAnEditShapeWhenBothArePresent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.md")
	args, err := json.Marshal(map[string]any{
		"file_path":  path,
		"content":    "THE WHOLE BODY",
		"old_string": "ignored",
		"new_string": "also ignored",
	})
	require.NoError(t, err)

	events, err := New().Extract(module.Input{
		module.InputPhase:   module.PhasePre,
		module.InputPayload: fakePending{tool: "Write", args: args},
	})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "THE WHOLE BODY", events[0].Fields[FieldNewContent],
		"a stated body outranks a replacement that would have to be applied")
}

// TestExtractPending_AReadIsStillNotAWrite pins the CORRECTED behaviour: a
// `Read` carries a file_path and neither a content nor an edit shape, so
// pendingArgs.statesAWrite is false and no event is built at all.
//
// This test used to assert the opposite value on purpose, holding the old
// "Read still produces a PreFileCreate" behaviour in place while a fix was
// pending — see git history on this comment. That trade stopped being
// accepted once a live pre-write gate was observed refusing a plain
// Read as though it were the write the guard exists to catch (the guard sees
// PreFileCreate/PreFileUpdate with resultKnown false and fails closed on it,
// unable to tell a Read's synthesized event apart from a real underivable
// write). The name is kept, because the property it names — a Read is not a
// write — is the same property; only which value pins it changed.
func TestExtractPending_AReadIsStillNotAWrite(t *testing.T) {
	events, err := New().Extract(module.Input{
		module.InputPhase: module.PhasePre,
		module.InputPayload: fakePending{
			tool: "Read",
			args: json.RawMessage(`{"file_path":"does-not-exist.md","offset":10,"limit":50}`),
		},
	})
	require.NoError(t, err)
	assert.Empty(t, events, "Read states no write, so it must reach no event")
}

// --- Q3: several edits to one file in one call ------------------------------

// multiEditPending is the payload for an edits-array tool.
//
// Measured caveat, and it is the reason this is tested at all rather than
// merely handled: `MultiEdit` appears ZERO times in the operator's
// 8,458-transcript corpus, alongside 5,880 `Edit` calls. It is not part of this
// harness's current vocabulary.
//
// It is still implemented and tested, for exactly the reason the module refuses
// name allowlists: a vocabulary this engine does not own can gain a member as
// easily as it renamed one. Reading the SHAPE means an edits array starts
// working the day a harness sends one, with nothing to update here.
func multiEditPending(path string, edits ...[2]string) fakePending {
	list := make([]map[string]any, 0, len(edits))
	for _, e := range edits {
		list = append(list, map[string]any{"old_string": e[0], "new_string": e[1]})
	}
	args, _ := json.Marshal(map[string]any{"file_path": path, "edits": list})
	return fakePending{tool: "MultiEdit", args: args}
}

// TestExtractPending_MultiEditAppliesItsEditsInOrder is Q3.
//
// The edits are sequential and each sees the previous one's result. The fixture
// is built so ORDER IS OBSERVABLE: the second edit's old_string exists only
// after the first has run. Applied against the original bytes it would not
// match, so an implementation that applies each edit to the starting content
// produces nothing and this test fails.
func TestExtractPending_MultiEditAppliesItsEditsInOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing.md")
	require.NoError(t, os.WriteFile(path, []byte("alpha\n"), 0o644))

	events, err := New().Extract(module.Input{
		module.InputPhase: module.PhasePre,
		module.InputPayload: multiEditPending(path,
			[2]string{"alpha", "beta"},
			[2]string{"beta", "gamma"},
		),
	})
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, KindPreUpdate, events[0].Kind)
	assert.Equal(t, "gamma\n", events[0].Fields[FieldNewContent],
		"the second edit matched only because the first had already run")
}

// TestExtractPending_MultiEditCreatingAFileCarriesTheFinalBody is the
// create-side of Q3: an empty first old_string against an absent path builds the
// file up across several edits, and the event carries where it ends.
func TestExtractPending_MultiEditCreatingAFileCarriesTheFinalBody(t *testing.T) {
	path := filepath.Join(t.TempDir(), "built-up.md")

	events, err := New().Extract(module.Input{
		module.InputPhase: module.PhasePre,
		module.InputPayload: multiEditPending(path,
			[2]string{"", "first\n"},
			[2]string{"first\n", "first\nsecond\n"},
		),
	})
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, KindPreCreate, events[0].Kind)
	assert.Equal(t, "first\nsecond\n", events[0].Fields[FieldNewContent])
}

// TestExtractPending_MultiEditWhoseLaterEditCannotApplyProducesNoEvent extends
// Q2 across the sequence: the tool applies the edits atomically, so one that
// cannot apply fails the whole call and no bytes change.
//
// Reporting the partial result of the edits that DID apply would announce a
// state the file never reaches.
func TestExtractPending_MultiEditWhoseLaterEditCannotApplyProducesNoEvent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing.md")
	require.NoError(t, os.WriteFile(path, []byte("alpha\n"), 0o644))

	events, err := New().Extract(module.Input{
		module.InputPhase: module.PhasePre,
		module.InputPayload: multiEditPending(path,
			[2]string{"alpha", "beta"},
			[2]string{"NOT PRESENT AT ANY POINT", "x"},
		),
	})
	require.NoError(t, err)
	assert.Empty(t, events,
		"the call fails as a whole, so the partial result is a state the file never holds")
}

// TestExtractPending_AnEmptyOldStringIsOnlyAnInsertionIntoANewFile pins the
// two conditions on the create spelling, and it exists because a mutation
// relaxing EITHER of them survived the suite.
//
// `old_string: ""` means "match nothing, insert this". It is well-defined only
// as the FIRST edit against a file that is not there. Anywhere else there is no
// defined insertion point — the tool would have to choose between prepending
// and appending — so it refuses, and predicting a result would be inventing one.
//
// The two subtests are the two halves of `i == 0 && created`, each mutated
// separately:
//
//	dropping `i == 0`     an empty old_string on a LATER edit, after an earlier
//	                      one already created the file
//	dropping `created`    an empty old_string against a file already on disk
func TestExtractPending_AnEmptyOldStringIsOnlyAnInsertionIntoANewFile(t *testing.T) {
	t.Run("not as a later edit, once the file has been created", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "built.md")

		events, err := New().Extract(module.Input{
			module.InputPhase: module.PhasePre,
			module.InputPayload: multiEditPending(path,
				[2]string{"", "first\n"},
				// The file exists by now, so this has no insertion point.
				[2]string{"", "second\n"},
			),
		})
		require.NoError(t, err)
		assert.Empty(t, events,
			"an empty old_string past the first edit has no defined insertion point")
	})

	t.Run("not against a file that already exists", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "existing.md")
		require.NoError(t, os.WriteFile(path, []byte("already here\n"), 0o644))

		events, err := New().Extract(module.Input{
			module.InputPhase:   module.PhasePre,
			module.InputPayload: editPending(path, "", "inserted"),
		})
		require.NoError(t, err)
		assert.Empty(t, events,
			"the tool refuses rather than choosing between prepending and appending")
	})
}

// TestExtractPending_APerEditReplaceAllIsNotHonouredAndStaysSilent records a
// KNOWN, deliberate imprecision rather than an oversight.
//
// `Edit` carries `replace_all` per CALL and an edits array would carry it per
// EDIT. The per-edit spelling is not decoded, so an edits entry whose
// old_string repeats is treated as ambiguous and the whole call goes silent —
// even though `replace_all: true` would make it perfectly determined.
//
// That is the conservative direction and it is chosen on purpose. A false
// "cannot apply" costs a missed event, which the Post-phase tree diff still
// reports. A false "applied" would put bytes in an event that the file never
// holds, which nothing downstream can correct. Given MultiEdit appears zero
// times in the 8,458-transcript corpus, paying for precision here would be
// speculative work on a shape no harness currently sends.
//
// If a harness does start sending it, this test is the one to change: decode
// the per-edit flag and thread it through applyEdits, which already takes a
// replaceAll parameter for the single-edit path.
func TestExtractPending_APerEditReplaceAllIsNotHonouredAndStaysSilent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing.md")
	require.NoError(t, os.WriteFile(path, []byte("dup\nmid\ndup\n"), 0o644))

	args, err := json.Marshal(map[string]any{
		"file_path": path,
		"edits": []map[string]any{
			{"old_string": "dup", "new_string": "X", "replace_all": true},
		},
	})
	require.NoError(t, err)

	events, err := New().Extract(module.Input{
		module.InputPhase:   module.PhasePre,
		module.InputPayload: fakePending{tool: "MultiEdit", args: json.RawMessage(args)},
	})
	require.NoError(t, err)
	assert.Empty(t, events,
		"silence is the safe wrong here: a missed event, not an invented result")
}

// TestExtractPending_AnEmptyEditsArrayProducesNoEvent holds the degenerate
// shape. An edits array with nothing in it changes no bytes, so there is no
// modification to be about.
func TestExtractPending_AnEmptyEditsArrayProducesNoEvent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing.md")
	require.NoError(t, os.WriteFile(path, []byte("alpha\n"), 0o644))

	args, err := json.Marshal(map[string]any{"file_path": path, "edits": []any{}})
	require.NoError(t, err)

	events, err := New().Extract(module.Input{
		module.InputPhase:   module.PhasePre,
		module.InputPayload: fakePending{tool: "MultiEdit", args: args},
	})
	require.NoError(t, err)
	assert.Empty(t, events, "no edits means no change")
}

// --- NotebookEdit: the coverage regression the migration introduced ---------

// TestExtractPending_ANotebookWriteIsAFileEventWithNoDerivableResult closes a
// measured hole, and it is the most consequential case in this file because the
// old behaviour was not a wrong value but NO EVENT AT ALL.
//
// Measured before the fix:
//
//	NotebookEdit {"notebook_path":"nb.ipynb","new_source":"print(1)"} -> nothing
//
// `pendingWrite` decoded only `file_path`, so the module saw no path, handed
// off to extractCommand, found no command either, and produced silence. Every
// Pre-kind rule was blind to notebook writes.
//
// It is a REGRESSION rather than a gap that was always there: the shell hook
// this engine replaced, `.claude/hooks/reflect-on-edits.sh`, matched
// NotebookEdit in its jq alongside Write/Edit/MultiEdit. Removing that hook in
// favour of a sloprail guardrail therefore narrowed what the project watched,
// silently, which is precisely the failure mode this engine exists to prevent.
//
// The fix is a second PATH KEY, not a tool-name branch — `notebook_path` names
// a file as surely as `file_path` does, and reading the key keeps working when
// a vendor renames the tool.
func TestExtractPending_ANotebookWriteIsAFileEventWithNoDerivableResult(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nb.ipynb")
	require.NoError(t, os.WriteFile(path, []byte(`{"cells":[]}`), 0o644))

	events, err := New().Extract(module.Input{
		module.InputPhase: module.PhasePre,
		module.InputPayload: fakePending{
			tool: "NotebookEdit",
			args: json.RawMessage(fmt.Sprintf(
				`{"notebook_path":%q,"new_source":"print(1)","cell_id":"c1","edit_mode":"replace"}`, path)),
		},
	})
	require.NoError(t, err)
	require.Len(t, events, 1, "a notebook write is a file modification and must be visible")

	e := events[0]
	assert.Equal(t, KindPreUpdate, e.Kind)
	assert.Equal(t, path, e.Fields[FieldPath])
	assert.Equal(t, false, e.Fields[FieldResultKnown],
		"new_source is one CELL, not the .ipynb document, so the file's bytes are not derived")
}

// TestExtractPending_ANotebookNeverReportsCellSourceAsFileContent is the
// negative that matters, and it guards the tempting wrong fix.
//
// `new_source` is right there and looks like content. Reporting it as the
// file's body would be worse than the silence it replaced: a rule reading
// `content` on a notebook create would receive one cell's Python and judge it
// as though it were the whole JSON document — so a frontmatter or schema rule
// would refuse every notebook, citing bytes that are not in the file.
//
// Re-serialising the real document was considered and rejected: it would make
// the engine decide key order, indentation and unicode escaping, and a result
// that is close but unequal to what Jupyter writes is worse than an honest
// "not known".
func TestExtractPending_ANotebookNeverReportsCellSourceAsFileContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh.ipynb")

	events, err := New().Extract(module.Input{
		module.InputPhase: module.PhasePre,
		module.InputPayload: fakePending{
			tool: "NotebookEdit",
			args: json.RawMessage(fmt.Sprintf(
				`{"notebook_path":%q,"new_source":"print(1)","edit_mode":"insert"}`, path)),
		},
	})
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, KindPreCreate, events[0].Kind)
	assert.Equal(t, "", events[0].Fields[FieldNewContent],
		"one cell's source is not the notebook document, and must never be passed off as it")
	assert.NotEqual(t, "print(1)", events[0].Fields[FieldNewContent],
		"the tempting wrong fix, named so it cannot be introduced quietly")
	// The empty newContent here is UNDERIVABLE, not a genuinely-empty file, and
	// resultKnown is what says so — the signal a pre-write gate reads to
	// fail closed rather than judging "" as if it were the file's bytes.
	assert.Equal(t, false, events[0].Fields[FieldResultKnown],
		"a notebook create's bytes are not derivable, so resultKnown must be false — not the empty-file case")
}

// TestExtractPending_TheNotebookPathKeyIsReadOnlyForARecognisedWriteTool pins
// the CORRECTED, current contract in place of what this test used to assert
// (see git history: the old version proved the key decided regardless of the
// tool's name, for the same drift-immunity reason TheEditShapeIsRead... used
// to prove for content/old_string).
//
// `NotebookEdit` is on HarnessWriteTools, so its `notebook_path` key is still
// read exactly as before. A same-shaped call under an unrecognised name is
// now excluded by the tool-name gate before the key is ever read — see
// commandmod/harnesstools.go.
func TestExtractPending_TheNotebookPathKeyIsReadOnlyForARecognisedWriteTool(t *testing.T) {
	dir := t.TempDir()

	t.Run("NotebookEdit is on the list", func(t *testing.T) {
		p := filepath.Join(dir, "NotebookEdit.ipynb")
		events, err := New().Extract(module.Input{
			module.InputPhase: module.PhasePre,
			module.InputPayload: fakePending{
				tool: "NotebookEdit",
				args: json.RawMessage(fmt.Sprintf(`{"notebook_path":%q,"new_source":"x"}`, p)),
			},
		})
		require.NoError(t, err)
		require.Len(t, events, 1)
		assert.Equal(t, p, events[0].Fields[FieldPath])
	})

	t.Run("an unrecognised tool is not, whatever it carries", func(t *testing.T) {
		for _, tool := range []string{"JupyterEdit", "", "RenamedUpstream"} {
			t.Run(tool, func(t *testing.T) {
				p := filepath.Join(dir, tool+".ipynb")
				events, err := New().Extract(module.Input{
					module.InputPhase: module.PhasePre,
					module.InputPayload: fakePending{
						tool: tool,
						args: json.RawMessage(fmt.Sprintf(`{"notebook_path":%q,"new_source":"x"}`, p)),
					},
				})
				require.NoError(t, err)
				assert.Empty(t, events, "tool %q is not on HarnessWriteTools", tool)
			})
		}
	})
}

// TestExtractPending_FilePathWinsWhenBothPathKeysAreCarried pins the
// precedence between the two path keys, which is orthogonal to the tool-name
// gate — this uses `Write`, a recognised write tool, so the gate admits the
// call and the precedence question is what is actually under test. Nothing
// sends both keys today; the generic spelling is what every other tool means
// by a path, so it is the one that wins if anything ever does.
func TestExtractPending_FilePathWinsWhenBothPathKeysAreCarried(t *testing.T) {
	dir := t.TempDir()
	want := filepath.Join(dir, "chosen.md")

	events, err := New().Extract(module.Input{
		module.InputPhase: module.PhasePre,
		module.InputPayload: fakePending{
			tool: "Write",
			args: json.RawMessage(fmt.Sprintf(
				`{"file_path":%q,"notebook_path":%q,"content":"body"}`,
				want, filepath.Join(dir, "other.ipynb"))),
		},
	})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, want, events[0].Fields[FieldPath])
	assert.Equal(t, "body", events[0].Fields[FieldNewContent],
		"file_path carries a stated body, and a stated body is still read")
}

// preCreateDecl is PreFileCreate's declaration, for compiling a matcher against
// the real thing rather than a hand-built map.
func preCreateDecl(t *testing.T) module.KindDecl {
	t.Helper()
	for _, k := range (&Module{}).Kinds() {
		if k.Name == KindPreCreate {
			return k
		}
	}
	t.Fatal("PreFileCreate must be declared")
	return module.KindDecl{}
}
