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
// the shell string. Driven through the mock: the agent's Bash turn is the entry
// the command is derived from, and command parsing owes nothing to the tree, so
// the mock reads it back exactly.
func TestT031_01_BashYieldsPreCommandInvoke(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	e.Run(proj, "s-031-01", "open a PR", Turns("done",
		Bash("a1", "gh pr create --draft --web"),
	))
	path := e.TranscriptPath(proj, "s-031-01")

	res := normalize(e, proj, path, "--whole-session")
	if res.Code != 0 {
		t.Fatalf("normalize exited %d, want 0:\n%s", res.Code, res.Output)
	}
	entries := decodeEntries(t, res.Output)

	// The user entry carries no event; the assistant entry carries the command.
	if got := eventsOf(entries[0]); len(got) != 0 {
		t.Fatalf("the user entry should carry no event, got %v", got)
	}
	a := bashEntry(t, entries)
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
//
// Driven through the mock: a `Say` turn is the agent's own prose a #tag lives in,
// and tag scanning is text-only, so the mock reads it back exactly.
func TestT031_02_TagsYieldPostTagWrite(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	e.Run(proj, "s-031-02", "note the decision", Turns("done",
		Say("a1", "Recording this as #update and #decision for later."),
	))
	path := e.TranscriptPath(proj, "s-031-02")

	res := normalize(e, proj, path, "--whole-session")
	if res.Code != 0 {
		t.Fatalf("normalize exited %d, want 0:\n%s", res.Code, res.Output)
	}
	entries := decodeEntries(t, res.Output)
	a := taggedEntry(t, entries)
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
// Driven through the mock: a `Say` turn with no tag is the entry that re-derives
// nothing, and its events array must land as [].
func TestT031_04_AnEntryWithNothingCarriesAnEmptyArray(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	e.Run(proj, "s-031-04", "hello", Turns("done",
		Say("a1", "Sure, on it. Still no tag here."),
	))
	path := e.TranscriptPath(proj, "s-031-04")

	res := normalize(e, proj, path, "--whole-session")
	if res.Code != 0 {
		t.Fatalf("normalize exited %d, want 0:\n%s", res.Code, res.Output)
	}
	entries := decodeEntries(t, res.Output)
	// Every entry the mock wrote for this cycle re-derives nothing: the user
	// prompt, the tool results, and the untagged `Say`. None may carry an event.
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
//
// A FIXTURE, not the mock: the spread-yield is about ONE entry carrying three
// tool_use blocks, and the scenario API emits one tool call per assistant turn —
// three `Bash` turns would be three separate entries with one event each, not the
// one entry with three this asserts (see the package note on multi-block turns).
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
//
// A FIXTURE, not the mock: the count rests on records that carry no uuid (Claude
// Code's own preamble), which the mock does not emit — so staging a preamble ahead
// of the real entries is only possible by hand (see the package note).
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

// bashEntry returns the single entry carrying a PreCommandInvoke, failing when
// there is not exactly one. Used by the mock-driven command test, where the entry
// of interest is found by its event rather than by a fixture's fixed index.
func bashEntry(t *testing.T, entries []normalized) normalized {
	t.Helper()
	return theEntryWith(t, entries, "PreCommandInvoke")
}

// taggedEntry returns the single entry carrying a PostTagWrite, failing when there
// is not exactly one.
func taggedEntry(t *testing.T, entries []normalized) normalized {
	t.Helper()
	return theEntryWith(t, entries, "PostTagWrite")
}

// theEntryWith returns the one entry among entries that carries an event of the
// given kind, failing the test when zero or several do.
func theEntryWith(t *testing.T, entries []normalized, kind string) normalized {
	t.Helper()
	var found []normalized
	for _, ent := range entries {
		for _, ev := range ent.Events {
			if ev.Kind == kind {
				found = append(found, ent)
				break
			}
		}
	}
	if len(found) != 1 {
		t.Fatalf("want exactly one entry carrying a %s, found %d", kind, len(found))
	}
	return found[0]
}
