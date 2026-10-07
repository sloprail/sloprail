// Package tagmod is the module for tags: the `#tag`-shaped tokens the agent
// wrote into its own messages over a cycle.
//
// It exists so a context can subscribe directly to a declared tag — `on:
// [{event: PostTagWrite}]` — instead of every context re-implementing the same
// grep-over-the-trajectory in its own `enter` script. It is deliberately narrow:
// one event kind carrying the tags written this cycle, not the general
// trajectory-query machinery. A context still reads `PostTagWrite.tags` itself
// to decide whether ITS tag was among them. See events/main.tsp,
// EventKind.PostTagWrite.
//
// A tag is a Post fact, not a Pre one, for the same reason every other Post
// event is: a tag cannot be known before the agent writes the sentence that
// carries it. So this module produces nothing at the pre-tool moment and emits
// its one event at the end of a cycle, scanning the settled text — the same
// timing as the file module's diff-established Post events.
package tagmod

import (
	"regexp"
	"strings"

	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/module"
)

// Name identifies this module. It is how the engine reports which module
// produced an event, and how a module is switched off — not a prefix the kinds
// carry.
const Name = "tag"

// KindPostTagWrite is the one kind this module declares.
//
// One kind, and a bulk one: it carries EVERY tag written this cycle, not one
// event per tag. A single message commonly holds more than one (`#update
// #decision`), and a context deciding whether ITS tag showed up should see the
// whole set at once rather than reassembling it from several events. There is no
// Pre counterpart — a tag is established by scanning what the agent actually
// wrote, which is not knowable before it writes it.
const KindPostTagWrite = "PostTagWrite"

// Field names, and the key within one entry of the tags list. They appear here,
// in Kinds below, and in the conversion in event.go — nowhere else, so a rename
// cannot leave a matcher checking against a name the events no longer carry.
const (
	// FieldTags is the list of tags found this cycle, in the order they appeared.
	FieldTags = "tags"

	// KeyTagLabel is the tag's own text, without the leading `#`. Not a field of
	// the kind: a matcher reads it off an element of the list, and the
	// declaration describes the list itself.
	KeyTagLabel = "label"

	// KeyTagSeen is true when the tag was only in text an earlier Stop in this
	// still-open cycle was already shown — the tag is being re-sent, not newly
	// written. A tag the agent wrote since the previous Stop is `seen: false`,
	// even if it had also been written before. See module.InputSeenMessages.
	KeyTagSeen = "seen"
)

// tagPattern matches one `#tag`-shaped token.
//
// The shape, and the reasons for each part:
//
//   - The `#` must sit at a word boundary — start of text, or after whitespace.
//     `(^|\s)` before it is what excludes `foo#bar` and a URL fragment
//     `example.com#section`, where the `#` is part of something else the agent
//     did not mean as a tag. RE2 has no lookbehind, so the leader is CAPTURED and
//     the tag is group 2, not group 1.
//   - The first character after `#` is a letter or underscore, never a digit.
//     That is what tells a tag from an issue reference: `#update` is a tag, `#42`
//     and `PR #1234` are not, and reading a numbered reference as a tag would put
//     noise in front of every context bound to this kind. A project that wants a
//     numeric tag can still write `#v2` — the constraint is only on the FIRST
//     character.
//   - The rest is letters, digits, underscores and hyphens, so `#no-slop` and
//     `#no_slop` and `#decision2` all read whole. It stops at the first character
//     outside that set, so `#update.` yields `update` and the period is left in
//     the prose.
//   - A markdown heading (`# Heading`) does not match: the space after `#` is not
//     a tag character, so there is no tag body at all. That is deliberate — `#`
//     followed by a space is punctuation, not a tag.
//   - Markdown EMPHASIS may open right before the `#`: up to three `*` or `_`
//     between the word boundary and the `#`, so `**#research summary:**`,
//     `*#research*`, `_#research_`, `__#research__` and `***#research***` are all
//     the tag `research`. Emphasis is how an agent makes a declaration stand out
//     — it is the agent SAYING the tag, loudly — and a real run wrote
//     `**#research summary:**` and was never heard (issue #89). The boundary rule
//     still applies to the emphasis run itself: `foo**#bar**` is intraword and
//     stays no tag. A closing `*` ends the tag already (it is not a tag
//     character); a closing `_` is — see emphasisLabel for how it is told apart.
//   - Backticks do NOT count as emphasis. A backtick opens a code span, and a
//     code span is text the agent is SHOWING, not declaring: a closed span is
//     removed before the scan (quotedForms), and a stray unclosed backtick before
//     `#` is not a word boundary, so neither is ever read as a tag. Strikethrough
//     (`~~#tag~~`) does not count either: struck-out text is text the agent took
//     back.
//
// Group 2 is the emphasis run before the `#` (possibly empty), group 3 the
// label, WITHOUT the leading `#`.
var tagPattern = regexp.MustCompile(`(^|\s)([*_]{0,3})#([A-Za-z_][A-Za-z0-9_-]*)`)

// emphasisLabel is a matched tag's label with the underscore emphasis that
// CLOSES it removed.
//
// `_` is both an emphasis delimiter and a tag character (`#no_slop`), so
// `_#research_` scans as `research_`. When the tag was opened by an emphasis run
// holding underscores, as many trailing underscores as that run held are the
// closing delimiter, not part of the label: `_#research_` → `research`,
// `__#no_slop__` → `no_slop`. A tag with no underscore opener keeps every
// character it had — `#research_` is still `research_`, exactly as before — so
// every form read before this is read the same way now.
func emphasisLabel(opener, label string) string {
	for n := strings.Count(opener, "_"); n > 0 && strings.HasSuffix(label, "_"); n-- {
		label = strings.TrimSuffix(label, "_")
	}
	return label
}

// quotedForms are removed from a message before it is scanned — SAID, not
// SHOWN. Each pattern strips one way an agent can put `#tag`-shaped text in
// front of a reader without meaning it as a tag: a fenced code block, an inline
// code span, or a quoted line. Applied in this order because a fenced block may
// itself contain backticks or `>` that would otherwise be read as a span or a
// quote — stripping the largest, most specific shape first is what keeps the
// smaller patterns from tearing a fence in half.
//
// WHY THIS EXISTS, AND WHY IT WAS MISSING. Two independent consumers of this
// module — the sloprail `strategy` repo's tag-required gate, and the
// `nikita-executive-memory` repo's predecessor rule — each discovered, by
// measurement rather than reasoning, that scanning raw text for `#tag` reads a
// tag out of an EXAMPLE of one: a fenced snippet showing what to reply, a
// blockquote of a rule's own refusal message (which necessarily contains the
// literal tag it is telling the agent to use), or an inline span while
// explaining the mechanism. All three shapes were measured, independently, in
// each repo, permitting a turn that recorded and declared nothing. One repo
// worked around it in the consumer's own hand-rolled trajectory re-scan,
// because this module's scan had no such protection and a consumer cannot patch
// a shared primitive from outside it. This is that fix, moved to the one place
// that serves every consumer rather than the one that measured the hole.
//
// APPLIED TO EVERY TAG, not only ones a particular guardrail treats as
// dangerous. A quoted ENTITY tag (`#decision` inside a fenced example) is safe
// on its own in most consumers' logic — the tag is recorded, its matching
// evidence is absent, and a downstream rule refuses, which is the safe
// direction. But safety in ONE consumer's downstream logic is not a property of
// this module, and a future consumer that treats an entity tag's mere presence
// as sufficient (no matching-evidence check) would inherit the same hole `#skip`
// had. Stripping uniformly means no consumer has to reason about which of its
// tags are "the dangerous ones" — a quoted token is not a tag here, full stop,
// and every consumer downstream inherits that for free.
//
// (?s) lets `.` cross newlines inside the fenced and quoted-line patterns, since
// a fence or a quoted block commonly spans several lines. The blockquote
// pattern is per-line ((?m), `^`/`$` match at each line boundary) because a `>`
// prefix is a per-line marker, not a delimited span the way a fence is.
var quotedForms = []*regexp.Regexp{
	regexp.MustCompile("(?s)```.*?```"),   // fenced block, triple backtick
	regexp.MustCompile("(?s)~~~.*?~~~"),   // fenced block, triple tilde
	regexp.MustCompile("`[^`]*`"),         // inline code span
	regexp.MustCompile(`(?m)^[ \t]*>.*$`), // a quoted (blockquote) line
}

// stripQuoted removes every quoted/shown form from msg, replacing each with a
// single space so a tag straddling the boundary of a removed span (rare, but
// possible with adjacent shapes) does not get spliced back together into
// something new. What remains is what the agent SAID in its own prose, which is
// the only text this module's tags are read from.
// sr:invariant events/tags-are-said-not-shown
func stripQuoted(msg string) string {
	for _, p := range quotedForms {
		msg = p.ReplaceAllString(msg, " ")
	}
	return msg
}

// Module produces the tag event.
type Module struct{}

// New returns the tag module.
func New() *Module { return &Module{} }

// Name implements module.Module.
func (*Module) Name() string { return Name }

// Kinds implements module.Module.
//
// `tags` is a list whose element shape is declared — a map with one key,
// `label` — so a matcher reading `any(tags, .label == "decision")` is checked to
// the bottom. A list whose Elem is nil has its predicate body left unchecked, so
// a typo `.labl` would compile, load, and never fire: the silent never-fires this
// declaration exists to prevent, the same shape the file and command modules
// declare their list elements against.
func (*Module) Kinds() []module.KindDecl {
	return []module.KindDecl{
		{
			Name: KindPostTagWrite,
			Fields: []module.FieldDecl{
				{
					Name: FieldTags,
					Type: module.TypeList,
					Elem: &module.FieldDecl{
						Type: module.TypeMap,
						Fields: []module.FieldDecl{
							{Name: KeyTagLabel, Type: module.TypeString},
							{Name: KeyTagSeen, Type: module.TypeBool},
						},
					},
				},
			},
		},
	}
}

// Extract implements module.Module.
//
// It reads the agent's settled messages for this cycle from module.InputMessages
// — a list of the text the agent wrote, gathered by the caller, which owns the
// harness-specific business of telling the agent's turns from everything else in
// the record and finding where this cycle begins. This module does the one thing
// that is the tag vocabulary's own: scan that text for `#tag` tokens.
//
// The split is the same one commandmod draws. commandmod is a pure function of a
// command LINE and never reads the filesystem; this is a pure function of the
// message TEXT and never reads the transcript. What decides which entries are
// the agent's, and which are this cycle's, is a transcript question the service
// answers, exactly as it does for `session query`.
//
// One event, always, when there is a message list to scan — even when it holds
// no tags. A PostTagWrite with an empty `tags` is a real answer ("the agent
// wrote nothing tagged this cycle"), and a context bound to this kind that wants
// to react to the ABSENCE of its tag needs to be told. The event is suppressed
// only when there is nothing to scan at all — no message list on the input —
// which is the caller saying "I have no settled text to offer", not "the agent
// wrote none".
//
// Produces nothing in the Pre phase. A tag is a Post fact; there is nothing to
// scan before the agent has written anything.
func (m *Module) Extract(in module.Input) ([]event.Event, error) {
	if in[module.InputPhase] != module.PhasePost {
		return nil, nil
	}
	messages, ok := in[module.InputMessages].([]string)
	if !ok {
		// No settled text was offered. Distinct from an empty list: the caller
		// either could not read the record or had nothing to gather, which is not
		// this module claiming the agent wrote no tags.
		return nil, nil
	}

	seenMessages, _ := in[module.InputSeenMessages].([]string)
	return []event.Event{TagEvent{Tags: scan(seenMessages, messages)}.Event()}, nil
}

// scan finds every `#tag` in the messages, deduplicated by label with
// first-occurrence order preserved. SAID text only — see stripQuoted and
// quotedForms above: a fenced block, an inline code span, or a quoted line is
// removed from each message before the pattern ever runs against it, so a
// `#tag` an agent is SHOWING (an example, a quoted refusal, an explanation) is
// not read as one it is declaring.
//
// Order is kept because the spec asks for the tags "in the order they appeared",
// and a rule may care which tag came first. Duplicates are dropped because the
// event's purpose is to say WHICH tags were written this cycle — the set — and a
// context checking whether its tag is among them gains nothing from seeing
// `#update` twice, while a bulk event listing the same label repeatedly reads as
// noise. This is a judgment call the spec leaves open: it names order and "every
// tag" but not repetition, and the set-with-order reading is what serves the one
// consumer the spec describes.
//
// Text an earlier Stop was already shown (seenMessages) is scanned first, and
// its tags are marked Seen. A label that turns up again in the new text is not a
// re-send — the agent wrote it again — so it is un-marked, keeping its first
// position. Seen therefore means "only in re-sent text".
// sr:invariant events/tags-are-said-not-shown
func scan(seenMessages, messages []string) []Tag {
	index := map[string]int{}
	var tags []Tag
	add := func(msgs []string, seen bool) {
		for _, msg := range msgs {
			msg = stripQuoted(msg)
			for _, match := range tagPattern.FindAllStringSubmatch(msg, -1) {
				label := emphasisLabel(match[2], match[3])
				if label == "" {
					// `_#_` — the "label" was only the closing underscore.
					continue
				}
				if i, ok := index[label]; ok {
					if !seen {
						tags[i].Seen = false
					}
					continue
				}
				index[label] = len(tags)
				tags = append(tags, Tag{Label: label, Seen: seen})
			}
		}
	}
	add(seenMessages, true)
	add(messages, false)
	return tags
}
