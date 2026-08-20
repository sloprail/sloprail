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
//
// Group 2 is the label, WITHOUT the leading `#`.
var tagPattern = regexp.MustCompile(`(^|\s)#([A-Za-z_][A-Za-z0-9_-]*)`)

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

	return []event.Event{TagEvent{Tags: scan(messages)}.Event()}, nil
}

// scan finds every `#tag` in the messages, deduplicated by label with
// first-occurrence order preserved.
//
// Order is kept because the spec asks for the tags "in the order they appeared",
// and a rule may care which tag came first. Duplicates are dropped because the
// event's purpose is to say WHICH tags were written this cycle — the set — and a
// context checking whether its tag is among them gains nothing from seeing
// `#update` twice, while a bulk event listing the same label repeatedly reads as
// noise. This is a judgment call the spec leaves open: it names order and "every
// tag" but not repetition, and the set-with-order reading is what serves the one
// consumer the spec describes.
func scan(messages []string) []Tag {
	seen := map[string]bool{}
	var tags []Tag
	for _, msg := range messages {
		for _, match := range tagPattern.FindAllStringSubmatch(msg, -1) {
			label := match[2]
			if seen[label] {
				continue
			}
			seen[label] = true
			tags = append(tags, Tag{Label: label})
		}
	}
	return tags
}
