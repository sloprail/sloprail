package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T031_03: a Write tool call yields a PreFileCreate for a file not on disk, and a
// PreFileUpdate for one that is — the pre-action file events, derived by the file
// module against the tree the binary runs in.
//
// Run from a git repo so the extractor's stats resolve: the file module reads the
// tree to tell a create from an update and to fill oldContent. A file present on
// disk becomes an update carrying its current bytes; an absent one becomes a
// create carrying the content the write would leave behind.
//
// Driven by the mock, then the tree put back: this derivation stat-s the LIVE tree, and a
// mock run APPLIES its writes (the mock executes a Write against the working directory), so
// a create would read back as an update with oldContent already the written bytes. The test
// restores the tree to its pre-write state after the run, so normalize reads the
// agent's real trajectory (in whichever harness's own record format) against the tree the
// create-vs-update distinction is about.
func TestT031_03_WriteYieldsPreFileEvents(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	// A file that already exists, so a write to it is an update; and none named
	// "brand-new.md", so a write to that is a create.
	e.WriteFile(proj, "existing.md", "old body\n")
	e.CommitAll(proj, "before the session")

	e.Run(proj, "s-031-03", "write some files", Turns("done",
		Write("w1", "brand-new.md", "# Brand New\n"),    // absent -> create
		Write("w2", "existing.md", "a wholly new body"), // present -> update
	))
	path := e.TranscriptPath(proj, "s-031-03")
	// The pre-write tree: the run's writes undone.
	if err := os.Remove(filepath.Join(proj, "brand-new.md")); err != nil {
		t.Fatalf("the run did not write brand-new.md: %v", err)
	}
	e.WriteFile(proj, "existing.md", "old body\n")

	// Run FROM the repo so the file module's stats resolve against it.
	res := normalize(e, proj, path)
	if res.Code != 0 {
		t.Fatalf("normalize exited %d, want 0:\n%s", res.Code, res.Output)
	}
	var entries []normalized
	for _, en := range decodeEntries(t, res.Output) {
		if len(en.Events) > 0 {
			entries = append(entries, en)
		}
	}
	if len(entries) != 2 {
		t.Fatalf("want the two write entries to carry events, got %d:\n%s", len(entries), res.Output)
	}

	create := entries[0]
	if got := eventsOf(create); len(got) != 1 || got[0] != "PreFileCreate" {
		t.Fatalf("a write to an absent path should be PreFileCreate, got %v", got)
	}
	if create.Events[0].Fields["path"] != "brand-new.md" {
		t.Fatalf("PreFileCreate should name brand-new.md, got %v", create.Events[0].Fields["path"])
	}
	if create.Events[0].Fields["newContent"] != "# Brand New\n" {
		t.Fatalf("PreFileCreate should carry the written content, got %v", create.Events[0].Fields["newContent"])
	}

	update := entries[1]
	if got := eventsOf(update); len(got) != 1 || got[0] != "PreFileUpdate" {
		t.Fatalf("a write to a present path should be PreFileUpdate, got %v", got)
	}
	// The update carries the file's CURRENT bytes as oldContent.
	if update.Events[0].Fields["oldContent"] != "old body\n" {
		t.Fatalf("PreFileUpdate should carry the file's current bytes as oldContent, got %v", update.Events[0].Fields["oldContent"])
	}
}

// T031_07: --events narrows which kinds populate each entry's events.
//
// One entry carries both a Bash call and a tag. Asked for PreCommandInvoke alone,
// only the command comes back; asked for PostTagWrite alone, only the tag. The
// other kind is not merely filtered from the output — the module that would
// produce it is not even run.
//
// Driven through the MOCK: the subject is ONE entry holding both a text block and a
// tool_use, which the `SayBash` builder emits as a single `[{text},{tool_use}]`
// assistant message — the block-list shape Claude Code writes for a turn that both
// says something (the #tag) and calls a tool. The mock forwards that entry intact,
// so normalize re-derives both the PreCommandInvoke (from the Bash block) and the
// PostTagWrite (from the #refactor in the text block) on the one entry, which the
// ordinary one-tool-per-turn API could not carry together.
func TestT031_07_EventsFlagNarrows(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	e.Run(proj, "s-031-07", "do it", Turns("done",
		SayBash("a1", "Doing this as #refactor.", "gofmt -w ."),
	))
	path := e.TranscriptPath(proj, "s-031-07")

	// Unfiltered. Where a turn's prose and its call are one entry, that entry carries both
	// the command and the tag; where the record writes them as two (Codex's rollout), the
	// command is on the call's entry and the tag on the message's, one event each. Found by
	// the events rather than a fixed index: a tool_result record sits after them.
	all := decodeEntries(t, mustRun(t, normalize(e, proj, path, "--whole-session")))
	both := theEntryWith(t, all, "PreCommandInvoke")
	bothTag := theEntryWith(t, all, "PostTagWrite")
	if harness.HasCap(t, harness.CapProseWithCallInOneEntry) {
		if got := eventsOf(both); len(got) != 2 {
			t.Fatalf("unfiltered, the entry should carry both a command and a tag, got %v", got)
		}
		if bothTag.UUID != both.UUID {
			t.Fatalf("the command and the tag should be on the SAME entry (%s vs %s)", both.UUID, bothTag.UUID)
		}
	} else {
		if got := eventsOf(both); len(got) != 1 {
			t.Fatalf("the call's entry should carry just the command, got %v", got)
		}
		if got := eventsOf(bothTag); len(got) != 1 {
			t.Fatalf("the message's entry should carry just the tag, got %v", got)
		}
		if bothTag.UUID == both.UUID {
			t.Fatalf("the record writes the prose and the call as two entries, but both events are on entry %s", both.UUID)
		}
	}

	// Only PreCommandInvoke: the tag module is not run, so that entry carries just
	// the command and no PostTagWrite appears anywhere.
	onlyCmd := decodeEntries(t, mustRun(t, normalize(e, proj, path, "--whole-session", "--events", "PreCommandInvoke")))
	cmdEntry := theEntryWith(t, onlyCmd, "PreCommandInvoke")
	if got := eventsOf(cmdEntry); len(got) != 1 || got[0] != "PreCommandInvoke" {
		t.Fatalf("--events PreCommandInvoke should keep only the command, got %v", got)
	}
	if anyKind(onlyCmd, "PostTagWrite") {
		t.Fatalf("--events PreCommandInvoke should not run the tag module at all, but a PostTagWrite appeared:\n%s",
			mustRun(t, normalize(e, proj, path, "--whole-session", "--events", "PreCommandInvoke")))
	}

	// Only PostTagWrite: symmetrically, only the tag.
	onlyTags := decodeEntries(t, mustRun(t, normalize(e, proj, path, "--whole-session", "--events", "PostTagWrite")))
	tagEntry := theEntryWith(t, onlyTags, "PostTagWrite")
	if got := eventsOf(tagEntry); len(got) != 1 || got[0] != "PostTagWrite" {
		t.Fatalf("--events PostTagWrite should keep only the tag, got %v", got)
	}
	if anyKind(onlyTags, "PreCommandInvoke") {
		t.Fatalf("--events PostTagWrite should not run the command module, but a PreCommandInvoke appeared")
	}
}

// T031_08: --events with a kind normalize cannot re-derive is refused when the
// flag is parsed — non-zero exit and a message naming the offending kind — rather
// than silently returning an empty stream.
//
// The Post file events, PreToolUse and Stop are the kinds the TrajectoryEvent
// union excludes; asking for one is a mistake in the rule, and telling the author
// is better than an empty answer that reads as "the agent did none of that".
func TestT031_08_ARefusableKindIsRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	// The kind is refused when the flag is PARSED, before any entry is read, so any
	// resolvable trajectory will do — a mock-produced one here.
	e.Run(proj, "s-031-08", "anything", Turns("done",
		Bash("a1", "true"),
	))
	path := e.TranscriptPath(proj, "s-031-08")

	for _, bad := range []string{"PostFileCreate", "PreToolUse", "Stop", "MadeUpKind"} {
		res := normalize(e, proj, path, "--events", bad)
		if res.Code == 0 {
			t.Fatalf("--events %s exited 0, want non-zero — a kind normalize cannot re-derive must be refused:\n%s", bad, res.Output)
		}
		if !strings.Contains(res.Output, bad) {
			t.Fatalf("the refusal for %s did not name the offending kind:\n%s", bad, res.Output)
		}
	}

	// A valid kind mixed with an invalid one is still refused — the whole flag is
	// typed, not just filtered to the valid part.
	res := normalize(e, proj, path, "--events", "PreCommandInvoke,Stop")
	if res.Code == 0 {
		t.Fatalf("a mix of a valid and an invalid kind should be refused:\n%s", res.Output)
	}
}

// T031_10: normalize refuses when no trajectory can be resolved — no --path, no
// transcript on the payload, and no session id in the environment.
//
// A resolvable trajectory that is simply empty is a different thing (it prints
// []); this is the case where there is nothing to read at all, which is a refusal
// on stderr rather than an empty success.
//
// CLAUDE_CODE_SESSION_ID is cleared because normalize shares cite's environment
// fallback (resolveTrajectory): a session id in the environment would resolve the
// current session, so "no trajectory" means no --path, no payload AND no session id.
// A developer runs this suite inside a real session whose own CLAUDE_CODE_SESSION_ID
// is on os.Environ(), so the empty assignment wins over it and the test holds locally
// and in CI alike.
func TestT031_10_NoTrajectoryIsRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()

	// No path, no payload record, no session id.
	res := e.CLIDirectStdinEnv(proj, `{}`, []string{"CLAUDE_CODE_SESSION_ID="},
		"sr-session", "trajectory", "normalize")
	if res.Code == 0 {
		t.Fatalf("normalize with no trajectory exited 0, want non-zero:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "no trajectory to read") {
		t.Fatalf("the refusal did not say why:\n%s", res.Output)
	}
}

// T031_11: normalize defaults to the payload's transcript when --path is absent,
// reading the hooked-in trajectory.
//
// The default-resolution wiring, proven by a payload carrying a transcript_path:
// the same record resolves and its entries come back, with no --path given. Read
// against a mock-produced transcript — the Bash turn is the entry whose
// PreCommandInvoke confirms the record really resolved.
func TestT031_11_DefaultsToThePayloadTranscript(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)

	e.Run(proj, "s-031-11", "a hooked-in read", Turns("done",
		Bash("a1", "npm test"),
	))
	path := e.TranscriptPath(proj, "s-031-11")

	payload := `{"transcript_path":"` + path + `","cwd":"` + proj + `"}`
	res := e.CLIDirectStdin(proj, payload, "sr-session", "trajectory", "normalize", "--whole-session")
	if res.Code != 0 {
		t.Fatalf("normalize from a payload exited %d, want 0:\n%s", res.Code, res.Output)
	}
	entries := decodeEntries(t, res.Output)
	a := bashEntry(t, entries)
	if got := eventsOf(a); len(got) != 1 || got[0] != "PreCommandInvoke" {
		t.Fatalf("the Bash entry should carry a PreCommandInvoke, got %v", got)
	}
}

// mustRun returns the output of a result that must have exited 0, failing the
// test otherwise — a small helper for the multi-run --events test.
func mustRun(t *testing.T, res harness.Result) string {
	t.Helper()
	if res.Code != 0 {
		t.Fatalf("normalize exited %d, want 0:\n%s", res.Code, res.Output)
	}
	return res.Output
}
