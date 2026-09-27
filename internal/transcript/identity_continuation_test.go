package transcript

import (
	"fmt"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The continuation shapes below are the ones real Claude Code transcripts on one
// machine have, rebuilt with synthetic content. Every record keeps the fields
// the walk reads (uuid, parentUuid, logicalParentUuid, timestamp) in the order
// the harness writes them; nothing a person typed is reproduced. Before the walk
// learned these shapes, 225 real hook runs (PreToolUse, Stop, SessionStart on
// resume and compact) resolved no session identity and ran with no store.

// compactAt is a compaction boundary as Claude Code writes it: parentless, with
// a logicalParentUuid naming the last record before the compaction, and the
// compaction's own time.
func compactAt(uuid, logicalParent, ts string) string {
	return fmt.Sprintf(`{"parentUuid":null,"logicalParentUuid":%q,"isSidechain":false,"type":"system",`+
		`"subtype":"compact_boundary","content":"Conversation compacted","isMeta":false,"timestamp":%q,`+
		`"uuid":%q,"level":"info","compactMetadata":{"trigger":"auto"}}`, logicalParent, ts, uuid)
}

// originAt is a conversation's first record at a given time.
func originAt(uuid, ts string) string {
	return fmt.Sprintf(`{"parentUuid":null,"isSidechain":false,"type":"user","timestamp":%q,"uuid":%q,`+
		`"message":{"role":"user","content":"start"}}`, ts, uuid)
}

// turnAt is an ordinary chained record at a given time.
func turnAt(uuid, parent, ts string) string {
	return fmt.Sprintf(`{"parentUuid":%q,"isSidechain":false,"type":"assistant","timestamp":%q,"uuid":%q,`+
		`"message":{"role":"assistant","content":"working"}}`, parent, ts, uuid)
}

// queueOp is the uuid-less bookkeeping a resumed file opens with.
func queueOp() string {
	return `{"type":"queue-operation","operation":"enqueue","timestamp":"2026-08-05T10:40:00.000Z","sessionId":"s"}`
}

// TestStableSessionIDForksOfACompactionConverge is the "chain revisits" mode,
// 110 real hook runs across four sessions of one conversation.
//
// What the harness did: the conversation compacted, and the compaction was
// APPENDED to the original transcript, part-way down. Every later resume then
// wrote a new file that opens on a verbatim copy of that same boundary record
// and carries a copy of the record the boundary names. The old walk took "any
// other file holding the logical parent" — which is every fork — and hopped
// fork to fork until it came back to one it had seen. A second compaction in
// one of the forks, resumed into two more files, made the same loop one level
// further out.
func TestStableSessionIDForksOfACompactionConverge(t *testing.T) {
	p := newProject(t)

	// The original file: the origin, the work, the first compaction appended,
	// work after it.
	original := p.write("c-original",
		preamble(),
		originAt("origin", "2026-07-07T16:16:12.911Z"),
		turnAt("before-compact", "origin", "2026-08-05T10:30:00.000Z"),
		compactAt("compact-1", "before-compact", "2026-08-05T10:37:29.006Z"),
		turnAt("after-compact-1", "compact-1", "2026-08-05T10:37:30.000Z"),
	)
	// Resumes of the compacted conversation: each opens on the same boundary
	// and carries the copied logical parent after it.
	var forks []string
	for _, name := range []string{"a-fork", "b-fork", "d-fork", "e-fork"} {
		forks = append(forks, p.write(name,
			queueOp(),
			compactAt("compact-1", "before-compact", "2026-08-05T10:37:29.006Z"),
			turnAt("before-compact", "origin", "2026-08-05T10:30:00.000Z"),
			turnAt("work-in-"+name, "compact-1", "2026-08-06T09:00:00.000Z"),
		))
	}
	// One fork compacted again, appended; two resumes of THAT open on the
	// second boundary.
	twice := p.write("f-fork-compacted-again",
		queueOp(),
		compactAt("compact-1", "before-compact", "2026-08-05T10:37:29.006Z"),
		turnAt("before-compact", "origin", "2026-08-05T10:30:00.000Z"),
		turnAt("before-compact-2", "compact-1", "2026-08-17T17:00:00.000Z"),
		compactAt("compact-2", "before-compact-2", "2026-08-17T17:04:06.710Z"),
	)
	forks = append(forks, twice)
	for _, name := range []string{"0-second-fork", "g-second-fork"} {
		forks = append(forks, p.write(name,
			queueOp(),
			compactAt("compact-2", "before-compact-2", "2026-08-17T17:04:06.710Z"),
			turnAt("before-compact-2", "compact-1", "2026-08-17T17:00:00.000Z"),
			turnAt("work-in-"+name, "compact-2", "2026-08-18T09:00:00.000Z"),
		))
	}

	for _, path := range append([]string{original}, forks...) {
		got, err := ResolveStableSessionID(p.dir, path)
		require.NoError(t, err, "%s", path)
		assert.Equal(t, "origin", got.ID,
			"%s: a fork of a compacted conversation must converge on the conversation's origin", path)
		assert.NoError(t, got.Degraded, "%s: the origin is on disk, so the walk must reach it", path)
	}
}

// TestStableSessionIDBoundaryMidFileWhenLogicalParentWasNeverWritten is the
// "continuation missing" mode that was NOT a lost file: 115 real hook runs
// across five forks of one conversation.
//
// What the harness did: a preserved-segment compaction named, as its logical
// parent, a record that was never written to any transcript — it is in the
// boundary's own list of preserved uuids and nowhere else on the machine. But
// the compaction was appended to the original file, so the boundary record
// itself sits there part-way down, and that is the trace the walk follows.
func TestStableSessionIDBoundaryMidFileWhenLogicalParentWasNeverWritten(t *testing.T) {
	p := newProject(t)
	p.write("the-original",
		originAt("origin", "2026-08-16T06:37:42.433Z"),
		turnAt("preserved-head", "origin", "2026-08-16T14:45:52.150Z"),
		compactAt("compact", "never-written", "2026-08-16T14:49:17.632Z"),
		turnAt("after", "compact", "2026-08-16T14:49:18.000Z"),
	)
	// Other files of the same origin (earlier re-forks) must not confuse it.
	p.write("an-early-refork",
		originAt("origin", "2026-08-16T06:37:42.433Z"),
		turnAt("early-work", "origin", "2026-08-16T07:00:00.000Z"),
	)
	var forks []string
	for _, name := range []string{"fork-1", "fork-2", "fork-3"} {
		forks = append(forks, p.write(name,
			preamble(),
			compactAt("compact", "never-written", "2026-08-16T14:49:17.632Z"),
			turnAt("preserved-head", "origin", "2026-08-16T14:45:52.150Z"),
			turnAt("work-in-"+name, "compact", "2026-08-18T13:14:35.000Z"),
		))
	}

	for _, path := range forks {
		got, err := ResolveStableSessionID(p.dir, path)
		require.NoError(t, err, "%s", path)
		assert.Equal(t, "origin", got.ID,
			"%s: the file the compaction was appended to holds the boundary itself, which reaches the origin", path)
		assert.NoError(t, got.Degraded, "%s", path)
	}
}

// TestStableSessionIDPredecessorDeletedDegradesConsistently is the mode that
// cannot be resolved: the file the continuation came from is gone. Claude Code
// deletes transcripts past its cleanup period, and three real continuations on
// one machine opened on a boundary no other file held in any form.
//
// The fallback must be the same for every fork of that continuation, or the
// fallback reintroduces the very split the identity exists to prevent.
func TestStableSessionIDPredecessorDeletedDegradesConsistently(t *testing.T) {
	p := newProject(t)
	var forks []string
	for _, name := range []string{"fork-1", "fork-2"} {
		forks = append(forks, p.write(name,
			preamble(),
			compactAt("compact", "cleaned-up", "2026-07-18T16:34:36.604Z"),
			turnAt("cleaned-up", "long-gone", "2026-07-18T16:30:00.000Z"),
			turnAt("work-in-"+name, "compact", "2026-07-19T09:00:00.000Z"),
		))
	}
	// An unrelated conversation in the same directory stays unrelated.
	p.write("unrelated", originAt("someone-else", "2026-07-01T00:00:00.000Z"))

	for _, path := range forks {
		got, err := ResolveStableSessionID(p.dir, path)
		require.NoError(t, err, "a deleted predecessor must not leave the session with no identity")
		assert.Equal(t, "compact", got.ID,
			"%s: the fallback is the continuation's own root, which every fork of it shares", path)
		require.ErrorIs(t, got.Degraded, ErrContinuationMissing)
	}
}

// TestStableSessionIDDoesNotWalkIntoALaterContinuation: a later continuation's
// preserved tail can carry a copy of a record an earlier boundary names. The
// file it sits in opens on a LATER boundary, and walking into it would walk
// forwards. Named to sort first, so only the timestamp keeps the walk out.
func TestStableSessionIDDoesNotWalkIntoALaterContinuation(t *testing.T) {
	p := newProject(t)
	p.write("z-original",
		originAt("origin", "2026-08-01T00:00:00.000Z"),
		turnAt("point", "origin", "2026-08-02T00:00:00.000Z"),
	)
	p.write("a-later",
		compactAt("later-compact", "somewhere", "2026-08-20T00:00:00.000Z"),
		turnAt("point", "origin", "2026-08-02T00:00:00.000Z"),
	)
	current := p.write("m-current",
		compactAt("compact", "point", "2026-08-10T00:00:00.000Z"),
		turnAt("point", "origin", "2026-08-02T00:00:00.000Z"),
	)

	got, err := ResolveStableSessionID(p.dir, current)
	require.NoError(t, err)
	assert.Equal(t, "origin", got.ID)
	assert.NoError(t, got.Degraded)
}

// TestStableSessionIDBacktracksPastADeadEnd: a file can hold a copy of the
// record a boundary names without being where the conversation came from.
// When the first candidate in name order dead-ends further back, the walk
// tries the next rather than degrading.
func TestStableSessionIDBacktracksPastADeadEnd(t *testing.T) {
	p := newProject(t)
	// Sorts first; holds a copy of "point", but is itself a continuation of
	// something deleted.
	p.write("a-dead-end",
		compactAt("dead-root", "deleted-long-ago", "2026-08-05T00:00:00.000Z"),
		turnAt("point", "origin", "2026-08-02T00:00:00.000Z"),
	)
	p.write("z-true-predecessor",
		originAt("origin", "2026-08-01T00:00:00.000Z"),
		turnAt("point", "origin", "2026-08-02T00:00:00.000Z"),
	)
	current := p.write("m-current",
		compactAt("compact", "point", "2026-08-10T00:00:00.000Z"),
	)

	got, err := ResolveStableSessionID(p.dir, current)
	require.NoError(t, err)
	assert.Equal(t, "origin", got.ID, "the walk must backtrack past a candidate that dead-ends")
	assert.NoError(t, got.Degraded)
}

// TestStableSessionIDDegradesToTheFirstDeadEndInNameOrder: when every
// candidate dead-ends, the fallback is the first candidate's dead end in name
// order — the same for every fork, which all see the same candidates.
func TestStableSessionIDDegradesToTheFirstDeadEndInNameOrder(t *testing.T) {
	p := newProject(t)
	p.write("a-dead-1",
		compactAt("dead-1", "gone-1", "2026-08-05T00:00:00.000Z"),
		turnAt("point", "x", "2026-08-02T00:00:00.000Z"),
	)
	p.write("b-dead-2",
		compactAt("dead-2", "gone-2", "2026-08-06T00:00:00.000Z"),
		turnAt("point", "x", "2026-08-02T00:00:00.000Z"),
	)
	var forks []string
	for _, name := range []string{"m-fork", "n-fork"} {
		forks = append(forks, p.write(name,
			compactAt("compact", "point", "2026-08-10T00:00:00.000Z"),
			turnAt("point", "x", "2026-08-02T00:00:00.000Z"),
		))
	}
	for _, f := range forks {
		got, err := ResolveStableSessionID(p.dir, f)
		require.NoError(t, err)
		assert.Equal(t, "dead-1", got.ID, "%s", f)
		require.ErrorIs(t, got.Degraded, ErrContinuationMissing)
	}
}

// TestStableSessionIDPastTheHopLimitDegradesLikeARing: a chain longer than
// maxRestartHops is malformed the same way a ring is — no real conversation
// restarts sixty-five times — so it degrades the same way, with
// ErrChainRunaway, rather than leaving the session with no identity.
func TestStableSessionIDPastTheHopLimitDegradesLikeARing(t *testing.T) {
	p := newProject(t)
	const n = maxRestartHops + 2 // an origin and 65 continuations
	name := func(i int) string { return fmt.Sprintf("hop-%03d", i) }
	ts := func(i int) string { return fmt.Sprintf("2026-01-01T00:%02d:%02d.000Z", i/60, i%60) }
	p.write(name(0), originAt("root-0", ts(0)), turnAt("end-0", "root-0", ts(0)))
	var last string
	for i := 1; i < n; i++ {
		last = p.write(name(i),
			compactAt(fmt.Sprintf("root-%d", i), fmt.Sprintf("end-%d", i-1), ts(i)),
			turnAt(fmt.Sprintf("end-%d", i), fmt.Sprintf("root-%d", i), ts(i)),
		)
	}

	got, err := ResolveStableSessionID(p.dir, last)
	require.NoError(t, err, "a chain past the hop limit must degrade, not leave the session with no identity")
	require.ErrorIs(t, got.Degraded, ErrChainRunaway)
	assert.Equal(t, fmt.Sprintf("root-%d", n-1-maxRestartHops), got.ID, "the fallback is where the walk stopped")

	// One hop inside the limit still reaches the origin.
	within, err := ResolveStableSessionID(p.dir, p.dir+"/"+name(maxRestartHops)+".jsonl")
	require.NoError(t, err)
	assert.Equal(t, "root-0", within.ID)
	assert.NoError(t, within.Degraded)
}

// TestStableSessionIDRefusesAnUnreadablePredecessor: a candidate on disk that
// cannot be read is not a predecessor that is gone. Calling it gone would key
// the session on a fallback now and on the origin once the file reads again,
// splitting its state in two — so the walk fails instead.
func TestStableSessionIDRefusesAnUnreadablePredecessor(t *testing.T) {
	t.Run("a record past the size bound", func(t *testing.T) {
		p := newProject(t)
		huge := `{"type":"assistant","uuid":"big","parentUuid":"origin","message":{"content":"` +
			strings.Repeat("x", maxRecordBytes+1) + `"}}`
		p.write("a-predecessor",
			originAt("origin", "2026-08-01T00:00:00.000Z"),
			huge,
			turnAt("point", "origin", "2026-08-02T00:00:00.000Z"),
		)
		current := p.write("m-current", compactAt("compact", "point", "2026-08-10T00:00:00.000Z"))

		_, err := ResolveStableSessionID(p.dir, current)
		require.Error(t, err, "an unreadable predecessor must not be reported as gone")
		assert.Contains(t, err.Error(), "cannot be read")
		assert.NotErrorIs(t, err, ErrContinuationMissing)
	})
	t.Run("a file that cannot be opened", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root reads a mode-000 file")
		}
		p := newProject(t)
		locked := p.write("a-predecessor",
			originAt("origin", "2026-08-01T00:00:00.000Z"),
			turnAt("point", "origin", "2026-08-02T00:00:00.000Z"),
		)
		require.NoError(t, os.Chmod(locked, 0))
		t.Cleanup(func() { _ = os.Chmod(locked, 0o644) })
		current := p.write("m-current", compactAt("compact", "point", "2026-08-10T00:00:00.000Z"))

		_, err := ResolveStableSessionID(p.dir, current)
		require.Error(t, err)
		assert.ErrorIs(t, err, fs.ErrPermission)
	})
}
