package e2e

import (
	"encoding/json"
	"strings"
	"testing"
)

// PostTagWrite is established at the end of a cycle by scanning the agent's own
// settled messages for `#tag`-shaped tokens. This suite proves the whole path:
// the agent writes tags in its prose, the Stop dispatch reads the record, the
// tag module scans it, and a rule bound to PostTagWrite is handed one bulk event
// carrying every tag.
//
// # Why these are end-to-end and not unit tests
//
// The unit tests in internal/tagmod prove the scanner finds `#tag` tokens in
// text. They cannot prove the Stop dispatch GATHERS the agent's messages, hands
// them to the module, and dispatches the event a rule can bind — nor that the
// bind point exists at all. That whole wiring is only observable from outside
// the binary.
//
// # RE-VEHICLED onto a NEW-format CONTEXT (was old GUARDRAIL.md PostTagWrite hooks)
//
// PostTagWrite is a ContextEventKind, NOT a GateEventKind (a gate wakes on
// pre-action events plus Stop, never on a Post event; a context may wake on the
// Post file events and PostTagWrite "because it sometimes must recognise itself
// from a file's settled content" — internal/declaration/events.go). So the new
// vehicle for a rule bound to PostTagWrite is a CONTEXT that ENTERS on it, and the
// context's enter is the check that is handed the event. The mechanical
// transformation is otherwise the one in tests/e2e/REVEHICLE-PATTERN.md: the enter
// receives the FLAT event (`.event.kind`, `.event.tags`), never the old nested
// `.event.fields`.
//
// The context enters unconditionally on every PostTagWrite (its trigger carries no
// `match`), which is what lets T030_02 observe the empty-tags cycle too. What the
// enter was handed is read back through the context's own recorded payload
// (ContextState): the enter emits the event it received as its payload, so the
// exact wire shape a rule sees is what the assertions inspect — kind, the bulk tag
// list, and an empty list on a cycle with no tag.

// tagWatch is a NEW-FORMAT context that enters on every PostTagWrite the cycle
// dispatches. No `match`, so it fires whether or not the agent wrote a tag — the
// empty-cycle case T030_02 needs. Its enter records the event it was handed as the
// context's payload; its exit stays active (it never governs anything here).
const tagWatch = `on:
  - event: PostTagWrite
enter: ./enter.sh
exit: ./exit.sh
`

// enterRecordsTags emits the FLAT event it was handed as the context's payload, so
// a test can read the tags off the wire through ContextState. `.event` carries the
// kind alongside the tags (the flat form: fields spread under `event`, `kind`
// beside them), so the emitted payload is `{"tags":[{"label":…},…],"kind":"PostTagWrite"}`
// — exactly the event a rule bound to PostTagWrite receives.
const enterRecordsTags = `#!/bin/sh
payload="$(cat)"
printf '%s' "$payload" | jq -c '.event'
exit 0
`

// exitStayActive never says done, so the context stays active for the rest of the
// session (exit 0 = deactivate, non-zero = stay active). Nothing here reads the
// active flag; only the enter's recorded payload matters.
const exitStayActive = `#!/bin/sh
cat >/dev/null
exit 1
`

// tagsSeen reads the event a PostTagWrite context's enter recorded, as the JSON a
// rule bound to PostTagWrite was handed. The enter emits the flat event as the
// context's payload, so this re-marshals that payload back to JSON and the
// assertions read the same wire shape the old hook read off its stdin.
func tagsSeen(t *testing.T, e *Env, proj, sess, contextName string) string {
	t.Helper()
	active, payload := e.ContextState(proj, sess, contextName)
	if !active && payload == nil {
		return ""
	}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("the context payload is not marshalable JSON: %v", err)
	}
	return string(b)
}

// T030_01: the tags the agent wrote in its messages reach a rule bound to
// PostTagWrite, as one bulk event.
func TestT030_01_TagsTheAgentWroteReachTheRule(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Context(proj, "tag-watch", tagWatch, map[string]string{
		"enter.sh": enterRecordsTags,
		"exit.sh":  exitStayActive,
	})
	e.CommitAll(proj, "the project before the session")

	sess := "s-030-01"
	e.Run(proj, sess, "do some tagged work", Turns("done",
		Say("m1", "Recording this as #update and #decision for later."),
	))

	tags := tagsSeen(t, e, proj, sess, "tag-watch")
	if tags == "" {
		t.Fatalf("no PostTagWrite reached the rule — the agent wrote tags and nothing scanned them")
	}
	if !strings.Contains(tags, `"kind":"PostTagWrite"`) {
		t.Errorf("the event a rule bound to PostTagWrite received was not a PostTagWrite:\n%s", tags)
	}
	// Both tags, in one event — the bulk property. A message commonly carries
	// more than one, and a context deciding whether ITS tag showed up should see
	// the whole set at once.
	if !strings.Contains(tags, `"label":"update"`) {
		t.Errorf("PostTagWrite did not carry #update:\n%s", tags)
	}
	if !strings.Contains(tags, `"label":"decision"`) {
		t.Errorf("PostTagWrite did not carry #decision:\n%s", tags)
	}
	// The `#` is stripped — the label is the tag's text, not the token.
	if strings.Contains(tags, `"label":"#update"`) {
		t.Errorf("the label kept its leading # — it must be the tag's text without it:\n%s", tags)
	}
}

// T030_02: a cycle in which the agent wrote no tag still dispatches a
// PostTagWrite, carrying an empty tags list.
//
// The absence is a real answer a context reacting to a missing tag depends on,
// and folding it away would make "the agent wrote no tag" indistinguishable from
// "the event never fired".
func TestT030_02_AnEmptyCycleStillDispatchesPostTagWrite(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Context(proj, "tag-watch", tagWatch, map[string]string{
		"enter.sh": enterRecordsTags,
		"exit.sh":  exitStayActive,
	})
	e.CommitAll(proj, "the project before the session")

	sess := "s-030-02"
	e.Run(proj, sess, "say something untagged", Turns("done",
		Say("m1", "Nothing tagged in this message at all."),
	))

	tags := tagsSeen(t, e, proj, sess, "tag-watch")
	if tags == "" {
		t.Fatalf("no PostTagWrite fired for a cycle with no tags — a context reacting to a missing tag would never wake")
	}
	if !strings.Contains(tags, `"kind":"PostTagWrite"`) {
		t.Errorf("want a PostTagWrite, got:\n%s", tags)
	}
	// An empty list, present on the wire — never a missing field or a null.
	if !strings.Contains(tags, `"tags":[]`) {
		t.Errorf("an empty cycle's PostTagWrite must carry an empty tags list:\n%s", tags)
	}
}
