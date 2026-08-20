package e2e

import (
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
// A note on committing the guardrail: a hook's working directory is its
// guardrail's folder, inside the tree the engine compares, so a hook that writes
// a ledger there is itself a change the next cycle would report. The guardrail is
// committed before the session runs, and the assertions name the tags they
// expect rather than counting events, so a ledger file in a later diff cannot
// make a test lie.

// recordTags writes the whole PostTagWrite payload it was handed, one line per
// run, so the test can read the tags off the wire.
const recordTags = `#!/bin/sh
cat >> tags.jsonl
printf '\n' >> tags.jsonl
`

// boundToTags records every PostTagWrite the cycle dispatches.
const boundToTags = `---
hooks:
  PostTagWrite:
    - hooks:
        - type: command
          command: ./record.sh
---

# Records the tags the agent wrote this cycle
`

// T030_01: the tags the agent wrote in its messages reach a rule bound to
// PostTagWrite, as one bulk event.
func TestT030_01_TagsTheAgentWroteReachTheRule(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	e.Guardrail(proj, "tag-watch", boundToTags, map[string]string{"record.sh": recordTags})
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "the project before the session")

	e.Run(proj, "s-030-01", "do some tagged work", Turns("done",
		Say("m1", "Recording this as #update and #decision for later."),
	))

	tags := strings.Join(e.Ledger(proj, "tag-watch", "tags.jsonl"), "\n")
	if tags == "" {
		t.Fatalf("no PostTagWrite reached the hook — the agent wrote tags and nothing scanned them")
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
	e.Guardrail(proj, "tag-watch", boundToTags, map[string]string{"record.sh": recordTags})
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "the project before the session")

	e.Run(proj, "s-030-02", "say something untagged", Turns("done",
		Say("m1", "Nothing tagged in this message at all."),
	))

	tags := strings.Join(e.Ledger(proj, "tag-watch", "tags.jsonl"), "\n")
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
