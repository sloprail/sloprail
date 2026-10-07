package tagmod

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/module"
)

func labels(tags []Tag) []string {
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		out = append(out, t.Label)
	}
	return out
}

func TestModule_Name(t *testing.T) {
	assert.Equal(t, Name, New().Name())
	assert.Equal(t, "tag", Name, "the module name is not a prefix the kinds carry")
}

func TestModule_DeclaresPostTagWriteWithATagsList(t *testing.T) {
	kinds := New().Kinds()
	require.Len(t, kinds, 1)
	assert.Equal(t, KindPostTagWrite, kinds[0].Name)
	require.Len(t, kinds[0].Fields, 1)

	tags := kinds[0].Fields[0]
	assert.Equal(t, FieldTags, tags.Name)
	assert.Equal(t, module.TypeList, tags.Type)
	require.NotNil(t, tags.Elem, "a nil Elem leaves a predicate over tags unchecked")
	assert.Equal(t, module.TypeMap, tags.Elem.Type)
	require.Len(t, tags.Elem.Fields, 2)
	assert.Equal(t, KeyTagLabel, tags.Elem.Fields[0].Name)
	assert.Equal(t, module.TypeString, tags.Elem.Fields[0].Type)
	assert.Equal(t, KeyTagSeen, tags.Elem.Fields[1].Name)
	assert.Equal(t, module.TypeBool, tags.Elem.Fields[1].Type)
}

// --- seen: a tag re-sent from text an earlier Stop already saw ---------------

// A refused reply's text is delivered again with the retry. Its tags are marked
// seen; the retry's own tags are not.
func TestScan_TagsOnlyInSeenTextAreSeen(t *testing.T) {
	got := scan([]string{"Done. #skip"}, []string{"Saved. #preference"})
	assert.Equal(t, []Tag{{Label: "skip", Seen: true}, {Label: "preference"}}, got)
}

// Written again since the previous Stop, a tag is not a re-send — even though
// it was also in the seen text. It keeps its first position.
func TestScan_ATagWrittenAgainIsNotSeen(t *testing.T) {
	got := scan([]string{"#skip #decision"}, []string{"still #skip"})
	assert.Equal(t, []Tag{{Label: "skip"}, {Label: "decision", Seen: true}}, got)
}

// Seen text is stripped of quoted forms like any other: a quoted tag in it is
// no tag at all, seen or not.
func TestScan_SeenTextIsStrippedToo(t *testing.T) {
	assert.Empty(t, scan([]string{"`#skip`"}, nil))
}

// Through Extract and the wire form: seen rides on each tag, and a caller that
// offers no seen text gets every tag unseen — the pre-existing behaviour.
func TestExtract_CarriesSeenOnTheWire(t *testing.T) {
	evs, err := New().Extract(module.Input{
		module.InputPhase:        module.PhasePost,
		module.InputSeenMessages: []string{"#skip"},
		module.InputMessages:     []string{"#preference"},
	})
	require.NoError(t, err)
	require.Len(t, evs, 1)
	assert.Equal(t, []any{
		map[string]any{KeyTagLabel: "skip", KeyTagSeen: true},
		map[string]any{KeyTagLabel: "preference", KeyTagSeen: false},
	}, evs[0].Fields[FieldTags])

	back, err := FromEvent(evs[0])
	require.NoError(t, err)
	assert.Equal(t, []Tag{{Label: "skip", Seen: true}, {Label: "preference"}}, back.Tags)

	evs, err = New().Extract(module.Input{
		module.InputPhase:    module.PhasePost,
		module.InputMessages: []string{"#skip"},
	})
	require.NoError(t, err)
	assert.Equal(t, []any{map[string]any{KeyTagLabel: "skip", KeyTagSeen: false}}, evs[0].Fields[FieldTags])
}

// --- the scanner ------------------------------------------------------------

// sr:proves events/tags-are-said-not-shown
func TestScan_FindsASingleTag(t *testing.T) {
	assert.Equal(t, []string{"update"}, labels(scan(nil, []string{"#update"})))
}

// multiple tags in one message — the bulk case the event exists for.
func TestScan_MultipleTagsInOneMessage(t *testing.T) {
	assert.Equal(t, []string{"update", "decision"},
		labels(scan(nil, []string{"#update #decision"})),
		"a message commonly carries more than one tag")
}

// tags mid-sentence, not only at the start of a line.
func TestScan_TagsMidSentence(t *testing.T) {
	assert.Equal(t, []string{"refactor"},
		labels(scan(nil, []string{"I did a #refactor here"})))
	assert.Equal(t, []string{"a", "b"},
		labels(scan(nil, []string{"start #a middle #b end"})))
}

// no tags at all — the scanner returns nothing, and the module still emits an
// empty event (tested separately).
func TestScan_NoTags(t *testing.T) {
	assert.Empty(t, scan(nil, []string{"nothing tagged here", "still nothing"}))
	assert.Empty(t, scan(nil, nil))
	assert.Empty(t, scan(nil, []string{""}))
}

// dedup: the same label twice yields one tag, first-occurrence order kept.
func TestScan_DedupsByLabelKeepingOrder(t *testing.T) {
	assert.Equal(t, []string{"update", "decision"},
		labels(scan(nil, []string{"#update then #decision then #update again"})),
		"a repeated tag is the same tag; the set is what a context checks membership against")
}

// dedup spans messages: a tag in message 1 and again in message 3 is one tag.
func TestScan_DedupAcrossMessages(t *testing.T) {
	assert.Equal(t, []string{"decision", "update"},
		labels(scan(nil, []string{"#decision", "untagged", "#update and #decision"})),
		"order is first-appearance across the whole cycle")
}

// A markdown heading is `#` followed by a space — punctuation, not a tag.
// sr:proves events/tags-are-said-not-shown
func TestScan_MarkdownHeadingIsNotATag(t *testing.T) {
	assert.Empty(t, scan(nil, []string{"# Heading", "## Subheading", "###"}),
		"# followed by whitespace is a heading, not a tag")
}

// A `#` mid-token — `foo#bar`, a URL fragment — is not a tag: the `#` must sit
// at a word boundary.
func TestScan_HashMidTokenIsNotATag(t *testing.T) {
	assert.Empty(t, scan(nil, []string{"see example.com#section"}),
		"a URL fragment's # is inside a token, not at a word boundary")
	assert.Empty(t, scan(nil, []string{"path/to/thing#anchor"}),
		"a # after a non-space character is not a tag")

	// A `#` that IS at a word boundary and starts with a letter matches, even
	// when it is really a CSS colour — the scanner cannot know one from a tag,
	// and reading it out of a documentation example is visible (the rule fires
	// and someone looks) rather than silent. Asserted so the boundary rule is
	// exact rather than assumed.
	assert.Equal(t, []string{"ffffff"}, labels(scan(nil, []string{"the color is #ffffff"})))
}

// A tag may not start with a digit — that tells it from an issue reference.
// sr:proves events/tags-are-said-not-shown
func TestScan_IssueReferenceIsNotATag(t *testing.T) {
	assert.Empty(t, scan(nil, []string{"fixes #42", "see PR #1234"}),
		"#<digits> is an issue reference, not a tag")
	// but a tag whose FIRST char is a letter and which contains digits is fine.
	assert.Equal(t, []string{"v2"}, labels(scan(nil, []string{"ship #v2"})))
}

// Hyphens and underscores are part of a tag; a trailing period is not.
func TestScan_TagBodyCharacters(t *testing.T) {
	assert.Equal(t, []string{"no-slop"}, labels(scan(nil, []string{"the #no-slop rule"})))
	assert.Equal(t, []string{"no_slop"}, labels(scan(nil, []string{"the #no_slop rule"})))
	assert.Equal(t, []string{"done"}, labels(scan(nil, []string{"we are #done."})),
		"a trailing period ends the tag and stays in the prose")
}

// --- markdown emphasis (issue #89) -------------------------------------------
//
// A real run wrote `**#research summary:**` and the research-run context never
// activated: `#` right after `**` was not at a word boundary. Emphasis around a
// tag is the agent saying it loudly, so it counts.

// Every emphasis delimiter run — `*`, `**`, `***`, `_`, `__`, and mixed — before
// a tag reads as that tag, with any closing delimiter left out of the label.
func TestScan_EmphasisAroundATagIsTheTag(t *testing.T) {
	for _, msg := range []string{
		"**#research summary:** backoff with jitter.", // the measured form
		"**#research**",
		"*#research*",
		"***#research***",
		"_#research_",
		"__#research__",
		"___#research___",
		"**_#research_**",
		"_**#research**_",
		"Done. **#research** — cloned and read.",
		"first line\n**#research** on the next",
		"_#research_.",
	} {
		assert.Equal(t, []string{"research"}, labels(scan(nil, []string{msg})), "in %q", msg)
	}
}

// An underscore opener's closing underscores are trimmed, but only as many as
// opened it: underscores that are part of the tag stay.
func TestScan_UnderscoreEmphasisKeepsTheTagsOwnUnderscores(t *testing.T) {
	assert.Equal(t, []string{"no_slop"}, labels(scan(nil, []string{"_#no_slop_"})))
	assert.Equal(t, []string{"no_slop"}, labels(scan(nil, []string{"__#no_slop__"})))
	assert.Equal(t, []string{"no-slop"}, labels(scan(nil, []string{"**#no-slop**"})))
	assert.Equal(t, []string{"_private"}, labels(scan(nil, []string{"_#_private_"})))
	assert.Empty(t, scan(nil, []string{"_#_"}), "a lone underscore closing an empty tag is no tag")
}

// Strict superset: every form read before is read the same way now — a bare
// tag's trailing underscore is still its own, since no emphasis opened it.
func TestScan_EmphasisDoesNotChangeFormsReadBefore(t *testing.T) {
	assert.Equal(t, []string{"research_"}, labels(scan(nil, []string{"#research_"})))
	assert.Equal(t, []string{"research"}, labels(scan(nil, []string{"#research**"})))
	assert.Equal(t, []string{"a", "b"}, labels(scan(nil, []string{"**#a** and #b"})))
}

// What emphasis does NOT open: the boundary still applies to the emphasis run,
// a digit-first body is still an issue reference, `# ` is still a heading, and
// strikethrough and backticks are not emphasis.
func TestScan_EmphasisNegatives(t *testing.T) {
	for _, msg := range []string{
		"foo**#bar**",                // intraword: no boundary before the run
		"see x_#y_",                  // intraword underscore
		"**#42**",                    // an issue reference, emphasised
		"**# Heading**",              // a heading-shaped `# `
		"~~#research~~",              // struck out: taken back, not said
		"`#research` summary",        // a code span is shown, not said
		"`**#research**` is the tag", // emphasis inside a code span
		"an unclosed `#research",     // a stray backtick is not a boundary
		"****#research****",          // four delimiters are not emphasis
	} {
		assert.Empty(t, scan(nil, []string{msg}), "in %q", msg)
	}
}

// Emphasis inside the shapes the scan strips is still stripped: fenced blocks
// and quoted lines are removed before the pattern runs, emphasis or not.
func TestScan_EmphasisedTagInQuotedFormsIsNotATag(t *testing.T) {
	assert.Empty(t, scan(nil, []string{"```\n**#research** example\n```"}))
	assert.Empty(t, scan(nil, []string{"~~~\n_#research_\n~~~"}))
	assert.Empty(t, scan(nil, []string{"> **#research summary:** quoted"}))
}

// --- said, not shown ---------------------------------------------------------
//
// Two independent consumers of this module (sloprail's own `strategy` repo and
// `nikita-executive-memory`'s predecessor rule) each measured, on real
// sessions, that a `#tag`-shaped token an agent is SHOWING — a fenced example,
// a quoted refusal, an inline span while explaining the mechanism — read as one
// it was DECLARING. All four cases below are the ones actually measured; a
// consumer refusing a turn on the wrong evidence is the failure this section
// exists to prevent from recurring.

// A fenced example showing what to reply is not the agent replying.
// sr:proves events/tags-are-said-not-shown
func TestScan_FencedCodeBlockIsNotATag(t *testing.T) {
	assert.Empty(t, scan(nil, []string{"Here's an example:\n```\nreply with #skip if nothing to record\n```\nThat's the mechanism."}),
		"a #tag inside a fenced block is being SHOWN, not declared")
	assert.Empty(t, scan(nil, []string{"~~~\nsome code #decision in a comment\n~~~"}),
		"the tilde fence strips the same way as the backtick fence")
}

// An inline code span naming a tag while explaining it is not using it.
// sr:proves events/tags-are-said-not-shown
func TestScan_InlineCodeSpanIsNotATag(t *testing.T) {
	assert.Empty(t, scan(nil, []string{"The token `#skip` means nothing here, explaining it."}),
		"an inline span is a mention, not a declaration")
}

// A blockquote of a rule's own refusal — which necessarily CONTAINS the literal
// tag it is telling the agent to use — is not the agent using it.
// sr:proves events/tags-are-said-not-shown
func TestScan_BlockquotedLineIsNotATag(t *testing.T) {
	assert.Empty(t, scan(nil, []string{"> MEMORY GUARDRAIL: ... use #skip if nothing needs recording.\nI read the above."}),
		"a quoted line is not the agent's own words")
}

// A tag genuinely written in the agent's own prose still matches — stripping
// removes only the quoted/shown forms, not the tag pattern's own reach.
func TestScan_UnquotedTagStillMatchesAlongsideStrippedOnes(t *testing.T) {
	assert.Equal(t, []string{"skip"},
		labels(scan(nil, []string{"Nothing to record. #skip"})),
		"genuine, unquoted use is unaffected")
	assert.Equal(t, []string{"real"},
		labels(scan(nil, []string{"`#fake` is just an example; #real is what I mean."})),
		"a quoted mention and a genuine use in the SAME message: only the genuine one counts")
}

// --- Extract ----------------------------------------------------------------

func postWith(messages []string) module.Input {
	return module.Input{
		module.InputPhase:    module.PhasePost,
		module.InputMessages: messages,
	}
}

// sr:proves events/tags-are-said-not-shown
func TestExtract_OneBulkEventCarryingEveryTag(t *testing.T) {
	events, err := New().Extract(postWith([]string{"#update #decision", "and #done"}))
	require.NoError(t, err)
	require.Len(t, events, 1, "one bulk PostTagWrite per cycle, not one per tag")

	got, err := FromEvent(events[0])
	require.NoError(t, err)
	assert.Equal(t, []string{"update", "decision", "done"}, labels(got.Tags))
}

// sr:proves events/tags-are-said-not-shown
func TestExtract_EmptyEventWhenNoTags(t *testing.T) {
	// The event still fires, carrying an empty tags list — a truthful "the agent
	// wrote nothing tagged this cycle", which a context reacting to the ABSENCE
	// of its tag depends on.
	events, err := New().Extract(postWith([]string{"no tags here"}))
	require.NoError(t, err)
	require.Len(t, events, 1)
	got, err := FromEvent(events[0])
	require.NoError(t, err)
	assert.Empty(t, got.Tags)
	// present-and-empty on the wire, so `len(tags) == 0` holds rather than errors.
	assert.Equal(t, []any{}, events[0].Fields[FieldTags])
}

func TestExtract_NoMessagesOfferedProducesNoEvent(t *testing.T) {
	// The absence of a message list is the caller saying "I have no settled text
	// to offer", not "the agent wrote no tags" — distinct from an empty list.
	events, err := New().Extract(module.Input{module.InputPhase: module.PhasePost})
	require.NoError(t, err)
	assert.Empty(t, events, "no InputMessages, no event")
}

func TestExtract_NothingInThePrePhase(t *testing.T) {
	// A tag cannot be known before the agent has written anything.
	events, err := New().Extract(module.Input{
		module.InputPhase:    module.PhasePre,
		module.InputMessages: []string{"#update"},
	})
	require.NoError(t, err)
	assert.Empty(t, events, "PostTagWrite is a Post fact only")
}

// --- Event conversion -------------------------------------------------------

func TestEvent_TagsAreAnEmptyListOnTheWireWhenNone(t *testing.T) {
	e := TagEvent{}.Event()
	assert.Equal(t, []any{}, e.Fields[FieldTags], "empty, not null")
}

func TestFromEvent_RoundTrip(t *testing.T) {
	in := TagEvent{Tags: []Tag{{Label: "update"}, {Label: "decision"}}}
	got, err := FromEvent(in.Event())
	require.NoError(t, err)
	assert.Equal(t, in, got)
}

func TestFromEvent_WrongKindIsAnError(t *testing.T) {
	_, err := FromEvent(event.Event{Kind: "PostFileCreate", Fields: map[string]any{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not a "+KindPostTagWrite)
}

func TestFromEvent_MalformedEntriesAreSkipped(t *testing.T) {
	// A read of events this module produced, so a malformed entry is skipped
	// rather than failing the whole read.
	got, err := FromEvent(event.Event{
		Kind: KindPostTagWrite,
		Fields: map[string]any{
			FieldTags: []any{
				map[string]any{KeyTagLabel: "good"},
				"not a map",
				map[string]any{KeyTagLabel: 42}, // label not a string
				map[string]any{"other": "no label key"},
			},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"good"}, labels(got.Tags))
}
