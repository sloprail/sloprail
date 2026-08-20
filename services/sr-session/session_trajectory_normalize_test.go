package main

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/commandmod"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/module/modules"
	"github.com/sloprail/sloprail/internal/tagmod"
	"github.com/sloprail/sloprail/internal/transcript"
)

// deriveEvents is the per-entry extractor driver: it runs the SAME modules the
// hook points run, one tool call at a time for the command/file kinds and once
// over the entry's prose for tags. These unit tests pin what one entry yields, so
// the CLI e2e can rest on the shape rather than re-proving the derivation.
//
// The registry is the real modules.Registry — the point of the driver is to reuse
// that machinery, so a test borrowing a fake vocabulary would stop proving it.

func derive(t *testing.T, e transcript.Entry, kinds kindSet) []event.Event {
	t.Helper()
	reg, err := modules.Registry()
	require.NoError(t, err)
	// Root "" — the file extractors then report paths as the record spelled them,
	// which is what a bare --path against another trajectory gets. The command
	// extractor is a pure function of the line and unaffected.
	return deriveEvents(e, reg, kinds, "")
}

// assistantWith builds an assistant entry whose content is the given raw blocks.
func assistantWith(blocks string) transcript.Entry {
	return transcript.Entry{
		Type:    transcript.EntryAssistant,
		UUID:    "a1",
		Message: json.RawMessage(`{"role":"assistant","content":[` + blocks + `]}`),
	}
}

func bashBlock(command string) string {
	return `{"type":"tool_use","id":"t1","name":"Bash","input":{"command":` + strconvQuote(command) + `}}`
}

func textBlock(text string) string {
	return `{"type":"text","text":` + strconvQuote(text) + `}`
}

// strconvQuote renders a Go string as a JSON string literal.
func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestDeriveEvents_BashYieldsPreCommandInvokeWithInvocations(t *testing.T) {
	e := assistantWith(bashBlock("gh pr create --draft"))
	events := derive(t, e, allKinds())

	require.Len(t, events, 1, "one Bash call yields one PreCommandInvoke")
	assert.Equal(t, commandmod.KindPreInvoke, events[0].Kind)

	cmd, err := commandmod.FromEvent(events[0])
	require.NoError(t, err)
	assert.Equal(t, "gh pr create --draft", cmd.Raw)
	require.Len(t, cmd.Invocations, 1)
	assert.Equal(t, "gh", cmd.Invocations[0].Bin)
	_, hasDraft := cmd.Invocations[0].Flags["draft"]
	assert.True(t, hasDraft, "the --draft flag is parsed into the invocation")
}

func TestDeriveEvents_APipelineIsOnePreCommandInvokeWithSeveralInvocations(t *testing.T) {
	// commandmod flattens a pipeline into ONE event carrying every invocation, so
	// a single Bash entry with a pipeline yields one PreCommandInvoke with several
	// invocations — not several events. This pins that reading for normalize.
	e := assistantWith(bashBlock("cat notes.md | grep TODO | wc -l"))
	events := derive(t, e, allKinds())

	require.Len(t, events, 1, "a pipeline is still one command entry, one event")
	cmd, err := commandmod.FromEvent(events[0])
	require.NoError(t, err)
	bins := make([]string, 0, len(cmd.Invocations))
	for _, inv := range cmd.Invocations {
		bins = append(bins, inv.Bin)
	}
	assert.ElementsMatch(t, []string{"cat", "grep", "wc"}, bins,
		"every program in the pipeline is flattened into the one event")
}

func TestDeriveEvents_ThreeToolCallsYieldThreeEvents(t *testing.T) {
	// The spread-yield property: one assistant entry with three tool calls yields
	// three events, one per call.
	e := assistantWith(
		bashBlock("ls") + "," + bashBlock("pwd") + "," + bashBlock("whoami"),
	)
	events := derive(t, e, allKinds())
	require.Len(t, events, 3, "three tool calls, three events")
	for _, ev := range events {
		assert.Equal(t, commandmod.KindPreInvoke, ev.Kind)
	}
}

func TestDeriveEvents_TagsYieldPostTagWrite(t *testing.T) {
	e := assistantWith(textBlock("Recording this as #update and #decision for later."))
	events := derive(t, e, allKinds())

	require.Len(t, events, 1, "the prose's tags yield one PostTagWrite")
	assert.Equal(t, tagmod.KindPostTagWrite, events[0].Kind)
	tags, err := tagmod.FromEvent(events[0])
	require.NoError(t, err)
	labels := make([]string, 0, len(tags.Tags))
	for _, tg := range tags.Tags {
		labels = append(labels, tg.Label)
	}
	assert.Equal(t, []string{"update", "decision"}, labels, "both tags, without the #")
}

func TestDeriveEvents_AnUntaggedAssistantEntryYieldsNoPostTagWrite(t *testing.T) {
	// The per-entry reading: an empty PostTagWrite is the cycle-level "no tag"
	// signal, not a per-entry event this entry re-derived. An entry with no tag
	// contributes to the empty array, not a PostTagWrite carrying nothing.
	e := assistantWith(textBlock("Nothing tagged in this message at all."))
	events := derive(t, e, allKinds())
	assert.Empty(t, events, "an untagged entry re-derives no PostTagWrite")
}

func TestDeriveEvents_AnEntryWithNeitherYieldsEmpty(t *testing.T) {
	// A plain assistant text turn with no tags and no tool calls.
	e := assistantWith(textBlock("Just thinking out loud here."))
	events := derive(t, e, allKinds())
	assert.NotNil(t, events, "an entry that yields none still carries a non-nil slice")
	assert.Empty(t, events)

	// A user entry yields nothing at all.
	user := transcript.Entry{
		Type:    transcript.EntryUser,
		UUID:    "u1",
		Message: json.RawMessage(`{"role":"user","content":"do the thing"}`),
	}
	assert.Empty(t, derive(t, user, allKinds()))
}

func TestDeriveEvents_EventsFlagNarrowsWhichKinds(t *testing.T) {
	// One entry carrying both a Bash call and a tag. Narrowing to PreCommandInvoke
	// drops the tag; narrowing to PostTagWrite drops the command.
	e := assistantWith(
		textBlock("Doing this as #refactor.") + "," + bashBlock("gofmt -w ."),
	)

	all := derive(t, e, allKinds())
	require.Len(t, all, 2, "unfiltered, both the command and the tag")

	onlyCmd := derive(t, e, kindSet{commandmod.KindPreInvoke: true})
	require.Len(t, onlyCmd, 1)
	assert.Equal(t, commandmod.KindPreInvoke, onlyCmd[0].Kind)

	onlyTags := derive(t, e, kindSet{tagmod.KindPostTagWrite: true})
	require.Len(t, onlyTags, 1)
	assert.Equal(t, tagmod.KindPostTagWrite, onlyTags[0].Kind)
}

func TestDeriveEvents_AnUnparseableCommandStillYieldsTheEvent(t *testing.T) {
	// commandmod never drops the event for a line it cannot resolve: the raw text
	// is what a rule about unparseable commands matches on. A command with an
	// unbalanced quote resolves no invocations but still carries its raw line.
	e := assistantWith(bashBlock(`echo "unterminated`))
	events := derive(t, e, allKinds())
	require.Len(t, events, 1, "a command that will not parse is still one event")
	cmd, err := commandmod.FromEvent(events[0])
	require.NoError(t, err)
	assert.Equal(t, `echo "unterminated`, cmd.Raw, "the raw line is kept even when nothing resolves")
}

func TestParseEventKinds_AbsentMeansEveryReDerivableKind(t *testing.T) {
	set, err := parseEventKinds(nil)
	require.NoError(t, err)
	for _, k := range trajectoryEventKinds() {
		assert.True(t, set.has(k), "absent --events populates every re-derivable kind, including %s", k)
	}
}

func TestParseEventKinds_NamedKindsAreTheOnlyOnes(t *testing.T) {
	set, err := parseEventKinds([]string{commandmod.KindPreInvoke, tagmod.KindPostTagWrite})
	require.NoError(t, err)
	assert.True(t, set.has(commandmod.KindPreInvoke))
	assert.True(t, set.has(tagmod.KindPostTagWrite))
	assert.False(t, set.has("PreFileCreate"), "a kind not named is not in the set")
}

func TestParseEventKinds_ARefusableKindIsRefusedWhenParsed(t *testing.T) {
	// The spec's typing: a kind normalize cannot re-derive is refused when the flag
	// is parsed, not silently returning nothing. Post file events, PreToolUse and
	// Stop are the ones the TrajectoryEvent union excludes.
	for _, bad := range []string{"PostFileCreate", "PreToolUse", "Stop", "nonsense"} {
		_, err := parseEventKinds([]string{bad})
		require.Error(t, err, "%q must be refused", bad)
		assert.Contains(t, err.Error(), bad, "the refusal names the offending kind")
	}
}

func TestParseEventKinds_EmptyValueIsRefusedNotWidened(t *testing.T) {
	// --events="" is a request that narrowed to nothing, distinct from the flag
	// being absent, so it is refused rather than folded back to everything.
	_, err := parseEventKinds([]string{""})
	require.Error(t, err)
}

func TestSinceLined_KeepsLinesAcrossTheSlice(t *testing.T) {
	lined := []transcript.LinedEntry{
		{Entry: transcript.Entry{UUID: "u1"}, Line: 3},
		{Entry: transcript.Entry{UUID: "u2"}, Line: 5},
		{Entry: transcript.Entry{UUID: "u3"}, Line: 8},
	}
	// Mark at u1 drops it and keeps the rest, with their physical lines intact.
	got := sinceLined(lined, "u1")
	require.Len(t, got, 2)
	assert.Equal(t, 5, got[0].Line, "the line survives the slice — it is the file's, not the slice index")
	assert.Equal(t, 8, got[1].Line)

	// An empty mark yields everything.
	assert.Len(t, sinceLined(lined, ""), 3)
	// An unfindable mark yields everything — the safe over-read of a lost position.
	assert.Len(t, sinceLined(lined, "gone"), 3)
}

func TestNormalizedEntry_SpreadsEntryAndAddsLineAndEvents(t *testing.T) {
	n := normalizedEntry{
		raw: transcript.Entry{
			Type:        transcript.EntryAssistant,
			UUID:        "a1",
			ParentUUID:  "u0",
			IsSidechain: false,
			Message:     json.RawMessage(`{"role":"assistant","content":"hi"}`),
		},
		Line: 7,
		Events: []event.Event{
			{Kind: commandmod.KindPreInvoke, Fields: map[string]any{"raw": "ls", "invocations": []any{}}},
		},
	}
	out, err := json.Marshal(n)
	require.NoError(t, err)

	var got map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(out, &got))

	// The entry's own fields sit at the top level — spread, not nested.
	assert.JSONEq(t, `"assistant"`, string(got["type"]))
	assert.JSONEq(t, `"a1"`, string(got["uuid"]))
	assert.JSONEq(t, `"u0"`, string(got["parentUuid"]))
	assert.JSONEq(t, `false`, string(got["isSidechain"]))
	// The two added fields, beside them.
	assert.JSONEq(t, `7`, string(got["line"]))
	require.Contains(t, string(got["events"]), `"kind":"PreCommandInvoke"`)
	// Each event carries the {kind, fields} wire shape a guardrail hook receives.
	assert.Contains(t, string(got["events"]), `"fields"`)
}

func TestNormalizedEntry_EmptyEventsIsAnArrayNotNull(t *testing.T) {
	n := normalizedEntry{
		raw:    transcript.Entry{Type: transcript.EntryUser, UUID: "u1"},
		Line:   1,
		Events: nil,
	}
	out, err := json.Marshal(n)
	require.NoError(t, err)
	var got map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(out, &got))
	assert.JSONEq(t, `[]`, string(got["events"]), "an entry yielding none carries an empty array, never null")
}

// allKinds is the full re-derivable set, for a derive call that narrows nothing.
func allKinds() kindSet {
	set, _ := parseEventKinds(nil)
	return set
}
