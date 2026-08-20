package e2e

import (
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
func TestT031_03_WriteYieldsPreFileEvents(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	// A file that already exists, so a write to it is an update; and none named
	// "brand-new.md", so a write to that is a create.
	e.WriteFile(proj, "existing.md", "old body\n")
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "before the session")

	path := writeTranscriptIn(t, proj,
		userMsg("u1", "write some files"),
		assistantWrite("a1", "u1", "brand-new.md", "# Brand New\n"),    // absent -> create
		assistantWrite("a2", "a1", "existing.md", "a wholly new body"), // present -> update
	)

	// Run FROM the repo so the file module's stats resolve against it.
	res := normalize(e, proj, path)
	if res.Code != 0 {
		t.Fatalf("normalize exited %d, want 0:\n%s", res.Code, res.Output)
	}
	entries := decodeEntries(t, res.Output)

	create := entries[1]
	if got := eventsOf(create); len(got) != 1 || got[0] != "PreFileCreate" {
		t.Fatalf("a write to an absent path should be PreFileCreate, got %v", got)
	}
	if create.Events[0].Fields["path"] != "brand-new.md" {
		t.Fatalf("PreFileCreate should name brand-new.md, got %v", create.Events[0].Fields["path"])
	}
	if create.Events[0].Fields["newContent"] != "# Brand New\n" {
		t.Fatalf("PreFileCreate should carry the written content, got %v", create.Events[0].Fields["newContent"])
	}

	update := entries[2]
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
func TestT031_07_EventsFlagNarrows(t *testing.T) {
	e := New(t)
	path := writeTranscript(t,
		userMsg("u1", "do it"),
		assistantBlocks("a1", "u1", false,
			`{"type":"text","text":"Doing this as #refactor."}`,
			`{"type":"tool_use","id":"c1","name":"Bash","input":{"command":"gofmt -w ."}}`,
		),
	)

	// Unfiltered: both the command and the tag.
	all := decodeEntries(t, mustRun(t, normalize(e, dirOf(path), path)))
	if got := eventsOf(all[1]); len(got) != 2 {
		t.Fatalf("unfiltered, the entry should carry both a command and a tag, got %v", got)
	}

	// Only PreCommandInvoke.
	onlyCmd := decodeEntries(t, mustRun(t, normalize(e, dirOf(path), path, "--events", "PreCommandInvoke")))
	if got := eventsOf(onlyCmd[1]); len(got) != 1 || got[0] != "PreCommandInvoke" {
		t.Fatalf("--events PreCommandInvoke should keep only the command, got %v", got)
	}

	// Only PostTagWrite.
	onlyTags := decodeEntries(t, mustRun(t, normalize(e, dirOf(path), path, "--events", "PostTagWrite")))
	if got := eventsOf(onlyTags[1]); len(got) != 1 || got[0] != "PostTagWrite" {
		t.Fatalf("--events PostTagWrite should keep only the tag, got %v", got)
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
	path := writeTranscript(t, userMsg("u1", "anything"))

	for _, bad := range []string{"PostFileCreate", "PreToolUse", "Stop", "MadeUpKind"} {
		res := normalize(e, dirOf(path), path, "--events", bad)
		if res.Code == 0 {
			t.Fatalf("--events %s exited 0, want non-zero — a kind normalize cannot re-derive must be refused:\n%s", bad, res.Output)
		}
		if !strings.Contains(res.Output, bad) {
			t.Fatalf("the refusal for %s did not name the offending kind:\n%s", bad, res.Output)
		}
	}

	// A valid kind mixed with an invalid one is still refused — the whole flag is
	// typed, not just filtered to the valid part.
	res := normalize(e, dirOf(path), path, "--events", "PreCommandInvoke,Stop")
	if res.Code == 0 {
		t.Fatalf("a mix of a valid and an invalid kind should be refused:\n%s", res.Output)
	}
}

// T031_10: normalize refuses when no trajectory can be resolved — no --path and no
// transcript on the payload.
//
// A resolvable trajectory that is simply empty is a different thing (it prints
// []); this is the case where there is nothing to read at all, which is a refusal
// on stderr rather than an empty success.
func TestT031_10_NoTrajectoryIsRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()

	// No path, no payload record.
	res := e.CLIDirectStdin(proj, `{}`, "sr-session", "trajectory", "normalize")
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
// the same record resolves and its entries come back, with no --path given.
func TestT031_11_DefaultsToThePayloadTranscript(t *testing.T) {
	e := New(t)
	path := writeTranscript(t,
		userMsg("u1", "a hooked-in read"),
		assistantBash("a1", "u1", "npm test"),
	)

	payload := `{"transcript_path":"` + path + `","cwd":"` + dirOf(path) + `"}`
	res := e.CLIDirectStdin(dirOf(path), payload, "sr-session", "trajectory", "normalize", "--whole-session")
	if res.Code != 0 {
		t.Fatalf("normalize from a payload exited %d, want 0:\n%s", res.Code, res.Output)
	}
	entries := decodeEntries(t, res.Output)
	if len(entries) != 2 {
		t.Fatalf("the hooked-in read should return both entries, got %d:\n%s", len(entries), res.Output)
	}
	if got := eventsOf(entries[1]); len(got) != 1 || got[0] != "PreCommandInvoke" {
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
