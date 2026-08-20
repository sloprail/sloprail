package e2e

import (
	"strings"
	"testing"
)

// T031_01: a Bash command yields a PreCommandInvoke carrying the parsed
// invocations, on the entry that made the call.
//
// The command case the whole vocabulary rests on: the raw line is kept, and the
// program is resolved to its basename with its flags parsed out — so a rule can
// ask "did the agent run gh with --draft" off the event rather than re-parsing
// the shell string.
func TestT031_01_BashYieldsPreCommandInvoke(t *testing.T) {
	e := New(t)
	path := writeTranscript(t,
		userMsg("u1", "open a PR"),                              // line 1
		assistantBash("a1", "u1", "gh pr create --draft --web"), // line 2
	)

	res := normalize(e, dirOf(path), path)
	if res.Code != 0 {
		t.Fatalf("normalize exited %d, want 0:\n%s", res.Code, res.Output)
	}
	entries := decodeEntries(t, res.Output)

	// The user entry carries no event; the assistant entry carries the command.
	if got := eventsOf(entries[0]); len(got) != 0 {
		t.Fatalf("the user entry should carry no event, got %v", got)
	}
	a := entries[1]
	if got := eventsOf(a); len(got) != 1 || got[0] != "PreCommandInvoke" {
		t.Fatalf("the Bash entry should carry one PreCommandInvoke, got %v", got)
	}
	ev := a.Events[0]
	if ev.Fields["raw"] != "gh pr create --draft --web" {
		t.Fatalf("the event did not keep the raw command line: %v", ev.Fields["raw"])
	}
	// The invocation is parsed: bin resolved, flags pulled out.
	invs, ok := ev.Fields["invocations"].([]interface{})
	if !ok || len(invs) != 1 {
		t.Fatalf("want one invocation, got %v", ev.Fields["invocations"])
	}
	inv := invs[0].(map[string]interface{})
	if inv["bin"] != "gh" {
		t.Fatalf("the invocation's bin should be gh, got %v", inv["bin"])
	}
	flags, ok := inv["flags"].(map[string]interface{})
	if !ok {
		t.Fatalf("the invocation carried no flags map: %v", inv["flags"])
	}
	if _, has := flags["draft"]; !has {
		t.Fatalf("the --draft flag was not parsed into the invocation: %v", flags)
	}
}

// T031_02: a tag in an assistant message yields a PostTagWrite carrying every tag.
func TestT031_02_TagsYieldPostTagWrite(t *testing.T) {
	e := New(t)
	path := writeTranscript(t,
		userMsg("u1", "note the decision"),                                              // line 1
		assistantText("a1", "u1", "Recording this as #update and #decision for later."), // line 2
	)

	res := normalize(e, dirOf(path), path)
	if res.Code != 0 {
		t.Fatalf("normalize exited %d, want 0:\n%s", res.Code, res.Output)
	}
	entries := decodeEntries(t, res.Output)
	a := entries[1]
	if got := eventsOf(a); len(got) != 1 || got[0] != "PostTagWrite" {
		t.Fatalf("the tagged entry should carry one PostTagWrite, got %v", got)
	}
	// Both labels, without the leading #.
	tags, ok := a.Events[0].Fields["tags"].([]interface{})
	if !ok || len(tags) != 2 {
		t.Fatalf("PostTagWrite should carry two tags, got %v", a.Events[0].Fields["tags"])
	}
	labels := []string{
		tags[0].(map[string]interface{})["label"].(string),
		tags[1].(map[string]interface{})["label"].(string),
	}
	if labels[0] != "update" || labels[1] != "decision" {
		t.Fatalf("tags should be [update decision] without the #, got %v", labels)
	}
}

// T031_04: an entry that yields none of the re-derivable kinds carries an empty
// array — never null, and never a PostTagWrite with an empty tags list.
//
// The absence is a real shape a consumer reads: an assistant reply with no tag
// and no tool call re-derived nothing, so it carries [], not an empty
// PostTagWrite (which is the cycle-level "no tag" signal, not a per-entry event).
func TestT031_04_AnEntryWithNothingCarriesAnEmptyArray(t *testing.T) {
	e := New(t)
	path := writeTranscript(t,
		userMsg("u1", "hello"),                         // line 1 — user, no events
		assistantReply("a1", "u1", "Sure, on it."),     // line 2 — plain reply, no tag
		assistantText("a2", "a1", "Still no tag here"), // line 3 — text, no tag
	)

	res := normalize(e, dirOf(path), path)
	if res.Code != 0 {
		t.Fatalf("normalize exited %d, want 0:\n%s", res.Code, res.Output)
	}
	entries := decodeEntries(t, res.Output)
	if len(entries) != 3 {
		t.Fatalf("want three entries, got %d:\n%s", len(entries), res.Output)
	}
	for _, ent := range entries {
		if len(ent.Events) != 0 {
			t.Fatalf("entry %s (line %d) should carry no events, got %v", ent.UUID, ent.Line, eventsOf(ent))
		}
	}
	// The empty array must be present on the wire as [], not null — a consumer
	// reading .events[] must not error on it.
	if !strings.Contains(res.Output, `"events":[]`) {
		t.Fatalf("an empty events array must serialise as [], not null:\n%s", res.Output)
	}
}

// T031_05: an assistant turn with three tool calls yields three events, one per
// call — the spread-yield the spec names.
func TestT031_05_ThreeToolCallsYieldThreeEvents(t *testing.T) {
	e := New(t)
	path := writeTranscript(t,
		userMsg("u1", "do a few things"),
		assistantBlocks("a1", "u1", false,
			`{"type":"tool_use","id":"c1","name":"Bash","input":{"command":"ls"}}`,
			`{"type":"tool_use","id":"c2","name":"Bash","input":{"command":"pwd"}}`,
			`{"type":"tool_use","id":"c3","name":"Bash","input":{"command":"whoami"}}`,
		),
	)

	res := normalize(e, dirOf(path), path)
	if res.Code != 0 {
		t.Fatalf("normalize exited %d, want 0:\n%s", res.Code, res.Output)
	}
	entries := decodeEntries(t, res.Output)
	a := entries[1]
	if got := eventsOf(a); len(got) != 3 {
		t.Fatalf("three tool calls should yield three events, got %v", got)
	}
	for _, ev := range a.Events {
		if ev.Kind != "PreCommandInvoke" {
			t.Fatalf("each of the three events should be a PreCommandInvoke, got %s", ev.Kind)
		}
	}
}

// T031_06: line numbers are the physical lines of the file, counting the preamble
// lines transcript reading skips — the one field jq cannot recompute downstream.
func TestT031_06_LineNumbersAreThePhysicalLines(t *testing.T) {
	e := New(t)
	path := writeTranscript(t,
		preambleLine(),                          // line 1 — no uuid, skipped as an entry
		preambleLine(),                          // line 2 — no uuid, skipped as an entry
		userMsg("u1", "start"),                  // line 3
		assistantBash("a1", "u1", "make build"), // line 4
	)

	res := normalize(e, dirOf(path), path)
	if res.Code != 0 {
		t.Fatalf("normalize exited %d, want 0:\n%s", res.Code, res.Output)
	}
	entries := decodeEntries(t, res.Output)
	if len(entries) != 2 {
		t.Fatalf("only the two uuid-carrying lines are entries, got %d:\n%s", len(entries), res.Output)
	}
	if entries[0].UUID != "u1" || entries[0].Line != 3 {
		t.Fatalf("the user entry sits on physical line 3, got line %d", entries[0].Line)
	}
	if entries[1].UUID != "a1" || entries[1].Line != 4 {
		t.Fatalf("the assistant entry sits on physical line 4, got line %d", entries[1].Line)
	}
}
