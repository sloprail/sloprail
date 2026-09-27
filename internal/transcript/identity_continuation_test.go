package transcript

import (
	"fmt"
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
