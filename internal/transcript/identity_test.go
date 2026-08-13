package transcript

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestStableSessionIDResolvesOrigin is the base case: a transcript whose root
// record has no parent resolves to that record's uuid.
func TestStableSessionIDResolvesOrigin(t *testing.T) {
	p := newProject(t)
	path := p.write("a-session",
		preamble(),
		root("origin-uuid"),
		record("child-uuid", "origin-uuid"),
	)

	got, err := StableSessionID(p.dir, path)
	if err != nil {
		t.Fatalf("StableSessionID: %v", err)
	}
	if got != "origin-uuid" {
		t.Fatalf("StableSessionID = %q, want the conversation's origin %q", got, "origin-uuid")
	}
}

// TestStableSessionIDForkPairConverges is the bug this exists to close. Claude
// Code re-forks its session id mid-conversation and writes a second transcript
// continuing the same message tree. Both must resolve to the same identity, or
// everything stored under the first is abandoned the moment the fork happens.
func TestStableSessionIDForkPairConverges(t *testing.T) {
	p := newProject(t)
	// The shared history: the fork copies it, so both files carry the same
	// origin record, verbatim, uuid included.
	before := root("shared-origin")

	original := p.write("before-the-fork",
		preamble(),
		before,
		record("work-before", "shared-origin"),
	)
	forked := p.write("after-the-fork",
		preamble(),
		before,
		record("work-before", "shared-origin"),
		record("work-after", "work-before"),
	)

	first, err := StableSessionID(p.dir, original)
	if err != nil {
		t.Fatalf("StableSessionID(original): %v", err)
	}
	second, err := StableSessionID(p.dir, forked)
	if err != nil {
		t.Fatalf("StableSessionID(forked): %v", err)
	}

	if first != second {
		t.Fatalf("a fork changed the conversation's identity: %q then %q — everything stored under the first is now unreachable", first, second)
	}
	if first != "shared-origin" {
		t.Fatalf("StableSessionID = %q, want %q", first, "shared-origin")
	}
}

// TestStableSessionIDCrossesRestart is the second walk. A restart opens a file
// whose first record has no parent — nothing precedes it in that file — yet
// which is not a beginning. Following the logical parent into the older
// transcript is what keeps the identity across the one event it most needs to
// survive.
func TestStableSessionIDCrossesRestart(t *testing.T) {
	p := newProject(t)
	p.write("the-original",
		preamble(),
		root("true-origin"),
		record("where-it-got-to", "true-origin"),
	)
	restarted := p.write("after-the-restart",
		boundary("restart-root", "where-it-got-to"),
		record("work-after-restart", "restart-root"),
	)

	got, err := StableSessionID(p.dir, restarted)
	if err != nil {
		t.Fatalf("StableSessionID: %v", err)
	}
	if got != "true-origin" {
		t.Fatalf("StableSessionID = %q, want the conversation's true origin %q — the walk stopped at the restart's own root", got, "true-origin")
	}
}

// TestStableSessionIDCrossesSeveralRestarts walks a chain rather than a single
// hop: a conversation that compacted, then restarted, then restarted again.
func TestStableSessionIDCrossesSeveralRestarts(t *testing.T) {
	p := newProject(t)
	p.write("first",
		root("true-origin"),
		record("end-of-first", "true-origin"),
	)
	p.write("second",
		boundary("second-root", "end-of-first"),
		record("end-of-second", "second-root"),
	)
	third := p.write("third",
		boundary("third-root", "end-of-second"),
	)

	got, err := StableSessionID(p.dir, third)
	if err != nil {
		t.Fatalf("StableSessionID: %v", err)
	}
	if got != "true-origin" {
		t.Fatalf("StableSessionID across two restarts = %q, want %q", got, "true-origin")
	}
}

// TestStableSessionIDIgnoresSelfMatch is the first trap, and it is an observed
// false positive rather than a hypothetical one: compaction copies earlier
// records verbatim into the new file, uuids included, so the record a boundary
// points at is very often ALSO present in the file doing the pointing. Matching
// it there sends the walk back to the root it just read.
//
// The fixture is that exact shape — verified against a real transcript on
// disk, where a compact_boundary's logicalParentUuid named its own preserved
// segment's tail and that uuid appeared again 2500 lines further down the same
// file.
func TestStableSessionIDIgnoresSelfMatch(t *testing.T) {
	p := newProject(t)
	p.write("the-older-one",
		root("true-origin"),
		record("the-continuation-point", "true-origin"),
	)
	// The new file: its boundary points at the-continuation-point, AND carries
	// a verbatim copy of that very record, preserved across the compaction.
	restarted := p.write("the-newer-one",
		boundary("restart-root", "the-continuation-point"),
		record("the-continuation-point", "true-origin"),
		record("work-after", "restart-root"),
	)

	got, err := StableSessionID(p.dir, restarted)
	if err != nil {
		t.Fatalf("StableSessionID: %v", err)
	}
	if got != "true-origin" {
		t.Fatalf("StableSessionID = %q, want %q — the walk matched the preserved copy in the file it was leaving and came back to where it started",
			got, "true-origin")
	}
}

// TestStableSessionIDSelfMatchWhenItIsTheOnlyMatch pins the exclusion even when
// following it means failing: if the ONLY file holding the target is the one
// being left, there is nothing older to reach and the honest answer is an
// error. Matching the copy instead would produce a confident wrong id.
func TestStableSessionIDSelfMatchWhenItIsTheOnlyMatch(t *testing.T) {
	p := newProject(t)
	only := p.write("only-one",
		boundary("restart-root", "the-continuation-point"),
		record("the-continuation-point", "gone-with-the-older-file"),
	)

	_, err := StableSessionID(p.dir, only)
	if err == nil {
		t.Fatal("a logical parent found only in the file being left must be an error, not a match on the copy")
	}
}

// TestStableSessionIDBoundsRunaway is the second trap. A chain of files each
// pointing at the next is bounded, so a malformed record fails with a
// diagnosis rather than spinning.
func TestStableSessionIDBoundsRunaway(t *testing.T) {
	p := newProject(t)
	// A ring: each boundary points at a record in the next file round, and the
	// last points back into the first. No file holds a true origin.
	const n = 5
	names := make([]string, n)
	for i := range names {
		names[i] = "link-" + string(rune('a'+i))
	}
	var start string
	for i := 0; i < n; i++ {
		next := (i + 1) % n
		path := p.write(names[i],
			boundary("root-"+names[i], "point-in-"+names[next]),
			record("point-in-"+names[i], "root-"+names[i]),
		)
		if i == 0 {
			start = path
		}
	}

	_, err := StableSessionID(p.dir, start)
	if err == nil {
		t.Fatal("a chain that never reaches an origin must return an error, not walk forever")
	}
	if !strings.Contains(err.Error(), "revisits") && !strings.Contains(err.Error(), "restarts") {
		t.Fatalf("the error should name the runaway, got: %v", err)
	}
}

// TestStableSessionIDFailsWhenTranscriptMissing pins failing loudly. A hook
// runs where the harness's transcript must exist, so a missing one is a broken
// environment — and answering with the reported id instead would restore the
// exact silent orphaning this exists to prevent.
func TestStableSessionIDFailsWhenTranscriptMissing(t *testing.T) {
	p := newProject(t)
	_, err := StableSessionID(p.dir, filepath.Join(p.dir, "never-written.jsonl"))
	if err == nil {
		t.Fatal("a missing transcript must be an error, never a quiet fallback to the reported id")
	}
}

// TestStableSessionIDFailsWhenNoOriginRecord pins the other loud failure: a
// transcript in which every entry has a parent is not a session we understand,
// and guessing at an origin would key everything on a guess.
func TestStableSessionIDFailsWhenNoOriginRecord(t *testing.T) {
	p := newProject(t)
	path := p.write("no-origin",
		preamble(),
		record("a-child", "a-parent-not-here"),
	)

	_, err := StableSessionID(p.dir, path)
	if err == nil {
		t.Fatal("a transcript with no origin record must be an error")
	}
	if !strings.Contains(err.Error(), "no origin record") {
		t.Fatalf("the error should name the actual problem, got: %v", err)
	}
}

// TestStableSessionIDFailsWhenLogicalParentIsNowhere pins the same for a
// restart pointing at a record no transcript holds — the environment lost a
// file, and answering anyway would answer wrongly.
func TestStableSessionIDFailsWhenLogicalParentIsNowhere(t *testing.T) {
	p := newProject(t)
	path := p.write("orphaned-restart",
		boundary("restart-root", "nowhere-to-be-found"),
	)

	_, err := StableSessionID(p.dir, path)
	if err == nil {
		t.Fatal("an unresolvable logical parent must be an error")
	}
}

// TestStableSessionIDKeepsIndependentConversationsApart proves the other
// direction of the fork case: two genuinely unrelated conversations must never
// merge. True by construction — only one transcript's own chain is ever read —
// but pinned, because a merge would be as damaging as a split.
func TestStableSessionIDKeepsIndependentConversationsApart(t *testing.T) {
	p := newProject(t)
	one := p.write("conversation-one", root("origin-one"), record("c1", "origin-one"))
	two := p.write("conversation-two", root("origin-two"), record("c2", "origin-two"))

	first, err := StableSessionID(p.dir, one)
	if err != nil {
		t.Fatalf("StableSessionID(one): %v", err)
	}
	second, err := StableSessionID(p.dir, two)
	if err != nil {
		t.Fatalf("StableSessionID(two): %v", err)
	}
	if first == second {
		t.Fatalf("two independent conversations resolved to the same identity %q", first)
	}
}

// TestStableSessionIDSkipsUnreadableSibling: another session's half-written
// file in the same project directory is not this conversation's problem, and
// must not stop the walk finding the file it needs.
func TestStableSessionIDSkipsUnreadableSibling(t *testing.T) {
	p := newProject(t)
	p.write("garbage", "this is not json at all", "{unterminated")
	p.write("the-older-one", root("true-origin"), record("continuation", "true-origin"))
	restarted := p.write("the-newer-one", boundary("restart-root", "continuation"))

	got, err := StableSessionID(p.dir, restarted)
	if err != nil {
		t.Fatalf("StableSessionID: %v", err)
	}
	if got != "true-origin" {
		t.Fatalf("StableSessionID = %q, want %q", got, "true-origin")
	}
}

// TestStableSessionIDNeedsAProjectDirOnlyToCrossARestart: a conversation that
// never restarted resolves with no project directory at all, because nothing
// older has to be found. One that did restart says so rather than failing
// vaguely.
func TestStableSessionIDNeedsAProjectDirOnlyToCrossARestart(t *testing.T) {
	p := newProject(t)
	plain := p.write("plain", root("origin"), record("c", "origin"))
	if got, err := StableSessionID("", plain); err != nil || got != "origin" {
		t.Fatalf("StableSessionID with no project dir = %q, %v; want %q and no error", got, err, "origin")
	}

	restarted := p.write("restarted", boundary("restart-root", "somewhere-older"))
	if _, err := StableSessionID("", restarted); err == nil {
		t.Fatal("crossing a restart with no project directory must be an error")
	}
}

// TestStableSessionIDNoPath is the guard clause — an error, not an empty
// string, so a caller cannot mistake a guard failure for a resolved identity.
func TestStableSessionIDNoPath(t *testing.T) {
	if _, err := StableSessionID("/somewhere", ""); err == nil {
		t.Fatal("an empty transcript path must be an error")
	}
}
