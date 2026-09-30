package transcript

import (
	"errors"
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	require.NoError(t, err, "StableSessionID")
	assert.Equal(t, "origin-uuid", got, "the identity must be the conversation's origin record")
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
	require.NoError(t, err, "StableSessionID(original)")
	second, err := StableSessionID(p.dir, forked)
	require.NoError(t, err, "StableSessionID(forked)")

	assert.Equal(t, second, first,
		"a fork changed the conversation's identity — everything stored under the first is now unreachable")
	assert.Equal(t, "shared-origin", first)
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
	require.NoError(t, err, "StableSessionID")
	assert.Equal(t, "true-origin", got,
		"the walk stopped at the restart's own root instead of crossing into the older transcript")
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
	require.NoError(t, err, "StableSessionID")
	assert.Equal(t, "true-origin", got, "the walk did not chain through both restarts")
}

// TestStableSessionIDIgnoresSelfMatch is the first trap, and it is an observed
// false positive rather than a hypothetical one: compaction copies earlier
// records verbatim into the new file, uuids included, so the record a boundary
// points at is very often ALSO present in the file doing the pointing. Matching
// it there sends the walk back to the root it just read.
//
// The fixture is that exact shape, and the shape is the common case rather
// than an edge: of the 8,084 real transcripts in one ~/.claude, 34 open with a
// parentless record carrying a logicalParentUuid, and in 33 of them the record
// it names is also in that same file — up to 756 lines further down.
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
	require.NoError(t, err, "StableSessionID")
	assert.Equal(t, "true-origin", got,
		"the walk matched the preserved copy in the file it was leaving and came back to where it started")
}

// TestStableSessionIDSelfMatchWhenItIsTheOnlyMatch pins the exclusion even when
// following it means stopping short: if the ONLY file holding the target is the
// one being left, there is nothing older to reach. Matching the copy would
// produce a confident wrong id; what it produces instead is the continuation's
// own root, flagged as degraded rather than passed off as the origin.
func TestStableSessionIDSelfMatchWhenItIsTheOnlyMatch(t *testing.T) {
	p := newProject(t)
	only := p.write("only-one",
		boundary("restart-root", "the-continuation-point"),
		record("the-continuation-point", "gone-with-the-older-file"),
	)

	got, err := ResolveStableSessionID(p.dir, only)
	require.NoError(t, err, "a continuation whose predecessor is gone still has a root to key on")
	assert.Equal(t, "restart-root", got.ID, "the fallback is the continuation's own root, never the copied record")
	require.ErrorIs(t, got.Degraded, ErrContinuationMissing,
		"a walk that stopped short of the origin must say so rather than pass the fallback off as the origin")
}

// TestStableSessionIDBoundsRunaway is the second trap. A ring of files each
// pointing at the next — malformed, no real harness writes one — ends where it
// closes on itself, flagged as degraded with ErrChainRunaway, rather than
// spinning or leaving the session with no identity.
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

	got, err := ResolveStableSessionID(p.dir, start)
	require.NoError(t, err, "a ring still leaves a root to key on")
	// From link-a the walk goes b, c, d, e, and e points back at link-a: it
	// stops at e, so e's root is the fallback.
	assert.Equal(t, "root-link-e", got.ID)
	require.ErrorIs(t, got.Degraded, ErrChainRunaway,
		"a chain that never reaches an origin must be flagged, not passed off as resolved")

	// In a ring the fallback depends on where the walk starts — every file is
	// somebody's predecessor. Documented on Identity.Degraded; pinned here so a
	// change to it is a decision rather than an accident.
	fromB, err := ResolveStableSessionID(p.dir, p.dir+"/link-b.jsonl")
	require.NoError(t, err)
	assert.Equal(t, "root-link-a", fromB.ID)
	require.ErrorIs(t, fromB.Degraded, ErrChainRunaway)
}

// TestStableSessionIDFailsWhenTranscriptMissing pins failing loudly. A hook
// runs where the harness's transcript must exist, so a missing one is a broken
// environment — and answering with the reported id instead would restore the
// exact silent orphaning this exists to prevent.
func TestStableSessionIDFailsWhenTranscriptMissing(t *testing.T) {
	p := newProject(t)
	_, err := StableSessionID(p.dir, filepath.Join(p.dir, "never-written.jsonl"))
	require.Error(t, err, "a missing transcript must be an error, never a quiet fallback to the reported id")
	assert.True(t, errors.Is(err, fs.ErrNotExist), "the error should say the file is not there, got: %v", err)
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
	require.Error(t, err, "a transcript with no origin record must be an error")
	require.ErrorIs(t, err, ErrNoOriginRecord)
}

// TestStableSessionIDDegradesWhenLogicalParentIsNowhere: a restart pointing at
// a record no transcript holds, with no file holding the restart's own root
// either. The environment lost the predecessor, so the origin cannot be
// reached — and the answer is the restart's own root, flagged, not no answer.
func TestStableSessionIDDegradesWhenLogicalParentIsNowhere(t *testing.T) {
	p := newProject(t)
	path := p.write("orphaned-restart",
		boundary("restart-root", "nowhere-to-be-found"),
	)

	got, err := ResolveStableSessionID(p.dir, path)
	require.NoError(t, err)
	assert.Equal(t, "restart-root", got.ID)
	require.ErrorIs(t, got.Degraded, ErrContinuationMissing)

	id, err := StableSessionID(p.dir, path)
	require.NoError(t, err, "StableSessionID answers with the fallback; only Resolve reports the degradation")
	assert.Equal(t, "restart-root", id)
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
	require.NoError(t, err, "StableSessionID(one)")
	second, err := StableSessionID(p.dir, two)
	require.NoError(t, err, "StableSessionID(two)")
	assert.NotEqual(t, first, second, "two independent conversations resolved to the same identity")
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
	require.NoError(t, err, "StableSessionID")
	assert.Equal(t, "true-origin", got)
}

// TestStableSessionIDNeedsAProjectDirOnlyToCrossARestart: a conversation that
// never restarted resolves with no project directory at all, because nothing
// older has to be found. One that did restart says so rather than failing
// vaguely.
func TestStableSessionIDNeedsAProjectDirOnlyToCrossARestart(t *testing.T) {
	p := newProject(t)
	plain := p.write("plain", root("origin"), record("c", "origin"))
	got, err := StableSessionID("", plain)
	require.NoError(t, err, "a conversation that never restarted needs no project directory")
	assert.Equal(t, "origin", got)

	restarted := p.write("restarted", boundary("restart-root", "somewhere-older"))
	_, err = StableSessionID("", restarted)
	require.Error(t, err, "crossing a restart with no project directory must be an error")
	require.ErrorIs(t, err, ErrNoProjectDir)
}

// TestStableSessionIDNoPath is the guard clause — an error, not an empty
// string, so a caller cannot mistake a guard failure for a resolved identity.
func TestStableSessionIDNoPath(t *testing.T) {
	_, err := StableSessionID("/somewhere", "")
	require.Error(t, err, "an empty transcript path must be an error")
	require.ErrorIs(t, err, ErrNoTranscriptPath)
}

func TestStartCwd_IsTheFirstDirectoryNotTheLast(t *testing.T) {
	p := newProject(t)
	path := p.write("s",
		`{"type":"user","uuid":"u1","parentUuid":null,"cwd":"/repo","message":{"role":"user","content":"hi"}}`,
		`{"type":"assistant","uuid":"a1","parentUuid":"u1","cwd":"/repo/.claude/worktrees/agent-1","message":{"role":"assistant","content":"x"}}`)
	got, err := StartCwd(path)
	require.NoError(t, err)
	assert.Equal(t, "/repo", got)
}

func TestStartCwd_NoDirectoryNamedIsEmptyNotAnError(t *testing.T) {
	p := newProject(t)
	got, err := StartCwd(p.write("s", userMsg("u1", "hi")))
	require.NoError(t, err)
	assert.Equal(t, "", got)
}

func TestStartCwd_UnreadableIsAnError(t *testing.T) {
	_, err := StartCwd("/no/such/record.jsonl")
	assert.Error(t, err)
}
