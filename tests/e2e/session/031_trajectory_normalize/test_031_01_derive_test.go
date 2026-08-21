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
// Driven through the MOCK: a10n-claude-mock forwards a multi-tool_use assistant
// entry VERBATIM — it breaks the turn loop at the FIRST tool_use to execute it, but
// the entry it persisted still carries all three blocks (measured), so normalize
// re-derives three PreCommandInvoke from the one entry. The `BashBatch` builder
// emits exactly that: one assistant entry carrying several Bash tool_use blocks,
// which the ordinary one-tool-per-turn scenario API could not (three `Bash` turns
// would be three separate entries with one event each, not the one entry with three
// this asserts).
func TestT031_05_ThreeToolCallsYieldThreeEvents(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	e.Run(proj, "s-031-05", "do a few things", Turns("done",
		BashBatch("c1", "ls", "pwd", "whoami"),
	))
	path := e.TranscriptPath(proj, "s-031-05")

	res := normalize(e, proj, path, "--whole-session")
	if res.Code != 0 {
		t.Fatalf("normalize exited %d, want 0:\n%s", res.Code, res.Output)
	}
	entries := decodeEntries(t, res.Output)
	// The one assistant entry carrying the batch — found by its PreCommandInvoke
	// events rather than a fixed index, since the mock's own tool_result records sit
	// around it. It is the only entry carrying any (theEntryWith fails if not exactly
	// one), and it must carry all three.
	a := bashEntry(t, entries)
	if got := eventsOf(a); len(got) != 3 {
		t.Fatalf("three tool calls should yield three events, got %v", got)
	}
	for _, ev := range a.Events {
		if ev.Kind != "PreCommandInvoke" {
			t.Fatalf("each of the three events should be a PreCommandInvoke, got %s", ev.Kind)
		}
	}
	// The three raw command lines are the three the batch carried, in order — proof
	// it is the multi-block entry and not three separate ones collapsed.
	var raws []string
	for _, ev := range a.Events {
		raws = append(raws, ev.Fields["raw"].(string))
	}
	if len(raws) != 3 || raws[0] != "ls" || raws[1] != "pwd" || raws[2] != "whoami" {
		t.Fatalf("the three events should carry ls, pwd, whoami in order, got %v", raws)
	}
}

// T031_06: line numbers are the physical lines of the file, counting the preamble
// lines transcript reading skips — the one field jq cannot recompute downstream.
//
// Driven through the MOCK: the count rests on Claude Code's own no-uuid preamble
// records — custom-title / ai-title / mode / queue-operation / last-prompt — which a
// transcript reader counts-but-skips. The harness seeds two of them at the transcript
// HEAD via SeedPreamble (the mock's own --preamble-file writes only into a still-empty
// transcript, and the harness pre-seeds the parentless root record itself, so the
// preamble is written ahead of that same record by the harness), then the session's
// own records follow: the seeded prompt on line 3, an assistant turn on line 4. So the
// physical-line count is checked against a mock-produced transcript whose opening lines
// are real preamble records, not a fully hand-authored fixture.
//
// A `Say` turn is the assistant entry because it produces a single assistant record
// with NO synthesised tool_result after it (a tool turn would add a tool_result entry,
// making a third entry on a later line); the two entries are then exactly the prompt
// and the assistant turn, on lines 3 and 4.
func TestT031_06_LineNumbersAreThePhysicalLines(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	// Two no-uuid preamble records occupy physical lines 1 and 2, ahead of the prompt.
	e.SeedPreamble(
		PreambleTitle("a conversation"),
		PreambleTitle("still a conversation"),
	)
	e.Run(proj, "s-031-06", "start", Turns("done",
		Say("m1", "on it"),
	))
	path := e.TranscriptPath(proj, "s-031-06")

	res := normalize(e, proj, path)
	if res.Code != 0 {
		t.Fatalf("normalize exited %d, want 0:\n%s", res.Code, res.Output)
	}
	entries := decodeEntries(t, res.Output)
	if len(entries) != 2 {
		t.Fatalf("only the two uuid-carrying lines are entries, got %d:\n%s", len(entries), res.Output)
	}
	// The seeded prompt is the user entry on physical line 3 (after the two preamble
	// lines); the assistant Say turn is the entry on line 4.
	if entries[0].Type != "user" || entries[0].Line != 3 {
		t.Fatalf("the user entry sits on physical line 3, got type %q line %d:\n%s",
			entries[0].Type, entries[0].Line, res.Output)
	}
	if entries[1].Type != "assistant" || entries[1].Line != 4 {
		t.Fatalf("the assistant entry sits on physical line 4, got type %q line %d:\n%s",
			entries[1].Type, entries[1].Line, res.Output)
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

// anyKind reports whether any entry among entries carries an event of the given
// kind — for asserting a kind is ABSENT everywhere (the module that would produce it
// was not run), which is stronger than checking one entry's events.
func anyKind(entries []normalized, kind string) bool {
	for _, ent := range entries {
		for _, ev := range ent.Events {
			if ev.Kind == kind {
				return true
			}
		}
	}
	return false
}
