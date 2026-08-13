package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/sessionstate"
	"github.com/sloprail/sloprail/internal/transcript"
)

// The read mark, driven through the commands that own it rather than through a
// helper.
//
// The rule under test is not "a uuid round-trips" — transcript.Since and
// transcript.Mark already have that covered. It is WHEN the mark moves: on a
// cycle that finished, and never on one that was interrupted. That rule lives
// in the stop command's own branching, so it is the command that has to be run.

// session is one isolated session: a transcript the commands read, a data home
// the store lands in, and the identity that ties the two together.
type session struct {
	t              *testing.T
	dir            string // the project the session guards
	transcriptPath string
	entries        int
}

// newSession stands up a project, an isolated data home, and a transcript with
// a root record — the shape anything looking for a conversation's origin scans
// for, and what the session's identity is derived from.
func newSession(t *testing.T) *session {
	t.Helper()

	root := t.TempDir()
	dir := filepath.Join(root, "project")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	// Where the store lands. Isolated so a test never reads or writes the
	// host's own state.
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))

	// Where the harness keeps its transcripts. Isolated for the same reason,
	// and needed because the session's identity walk looks in here.
	configDir := filepath.Join(root, "claude-cfg")
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)

	projectDir := transcript.ProjectDir(configDir, dir)
	require.NoError(t, os.MkdirAll(projectDir, 0o755))

	s := &session{t: t, dir: dir, transcriptPath: filepath.Join(projectDir, "sess.jsonl")}
	s.appendRoot()
	return s
}

// appendRoot writes the conversation's origin: a record with a uuid and an
// explicitly null parent.
func (s *session) appendRoot() {
	s.t.Helper()
	s.append(fmt.Sprintf(
		`{"type":"user","uuid":%q,"parentUuid":null,"message":{"role":"user","content":"go"}}`,
		s.uuid(0)))
}

// turn appends one assistant turn, and returns its uuid.
func (s *session) turn() string {
	s.t.Helper()
	id := s.uuid(s.entries)
	s.append(fmt.Sprintf(
		`{"type":"assistant","uuid":%q,"parentUuid":%q,"message":{"role":"assistant","content":"did a thing"}}`,
		id, s.uuid(s.entries-1)))
	return id
}

func (s *session) uuid(n int) string { return fmt.Sprintf("entry-%04d", n) }

func (s *session) append(line string) {
	s.t.Helper()
	f, err := os.OpenFile(s.transcriptPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	require.NoError(s.t, err)
	defer f.Close()
	_, err = f.WriteString(line + "\n")
	require.NoError(s.t, err)
	s.entries++
}

// payload is what a harness puts on the hook's stdin.
func (s *session) payload(stopHookActive bool) string {
	s.t.Helper()
	b, err := json.Marshal(HookPayload{
		TranscriptPath: s.transcriptPath,
		Cwd:            s.dir,
		StopHookActive: stopHookActive,
	})
	require.NoError(s.t, err)
	return string(b)
}

// run executes one of the engine's own commands against this session, the way
// a harness invokes it: the payload on stdin, no arguments.
func (s *session) run(cmd *cobra.Command, payload string, args ...string) (stdout, stderr string) {
	s.t.Helper()
	var out, errBuf bytes.Buffer
	cmd.SetIn(strings.NewReader(payload))
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	require.NoError(s.t, cmd.Execute())
	return out.String(), errBuf.String()
}

func (s *session) stop(stopHookActive bool) (stdout, stderr string) {
	s.t.Helper()
	return s.run(newSessionStopCmd(), s.payload(stopHookActive))
}

func (s *session) query(args ...string) []transcript.Entry {
	s.t.Helper()
	out, _ := s.run(newSessionQueryCmd(), s.payload(false), args...)
	var entries []transcript.Entry
	require.NoError(s.t, json.Unmarshal([]byte(out), &entries))
	return entries
}

// mark reads the position the store currently holds.
func (s *session) mark() string {
	s.t.Helper()
	store, err := openEngineState(HookPayload{TranscriptPath: s.transcriptPath, Cwd: s.dir})
	require.NoError(s.t, err)
	defer store.Close()
	mark, _, err := store.Meta(sessionstate.MetaTranscriptRead)
	require.NoError(s.t, err)
	return mark
}

func uuidsOf(entries []transcript.Entry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.UUID)
	}
	return out
}

// offered reads the position the record was last read out to, which is what a
// completed cycle carries forward as the mark.
func (s *session) offered() string {
	s.t.Helper()
	store, err := openEngineState(HookPayload{TranscriptPath: s.transcriptPath, Cwd: s.dir})
	require.NoError(s.t, err)
	defer store.Close()
	v, _, err := store.Meta(sessionstate.MetaTranscriptOffered)
	require.NoError(s.t, err)
	return v
}

// dispatched ends a cycle with the judging step standing in as having run.
//
// The mark is held until the Post events are dispatched, and that step is not
// implemented yet. A test about the mark's POSITION must not be a test about
// that step being missing, so it is stood in for here — which also means these
// tests keep working, and keep meaning the same thing, once it lands for real.
func (s *session) dispatched(stopHookActive bool) (stdout, stderr string) {
	s.t.Helper()
	restore := dispatchPostEvents
	dispatchPostEvents = func(*cobra.Command, sessionstate.Store, HookPayload) bool { return true }
	defer func() { dispatchPostEvents = restore }()
	return s.stop(stopHookActive)
}

// cycle is a whole cycle: a rule reads the record, then the cycle ends.
//
// The read is not incidental. The mark is the position a cycle READ, so a cycle
// that never queried has nothing to carry forward — which is the rule F1 turns
// on. A test about where the mark lands has to have something actually read the
// record, the way a session with any guardrail in it does.
func (s *session) cycle() {
	s.t.Helper()
	s.query()
	s.dispatched(false)
}

func TestReadMark_DoesNotMarkTurnsAppendedAfterTheCycleRead(t *testing.T) {
	// F1, and the reason the mark is carried forward rather than re-derived.
	//
	// A turn appended between the cycle's read and the cycle's end was never
	// offered to anything. Marking it judged skips it permanently and silently:
	// the record only grows, the mark only moves forward, and nothing goes back.
	s := newSession(t)
	first := s.turn()

	// The cycle reads. This is everything it was ever shown.
	require.Equal(t, []string{s.uuid(0), first}, uuidsOf(s.query()))

	// The agent appends another turn before the cycle ends. Nothing has judged
	// it, and nothing has been given the chance to.
	racing := s.turn()

	s.dispatched(false)

	assert.Equal(t, first, s.mark(),
		"the mark must be where the cycle READ, not where the record happens to end at write time")
	assert.Equal(t, []string{racing}, uuidsOf(s.query()),
		"a turn appended during the cycle must still be offered — skipping it loses it for good")
}

func TestReadMark_SeveralTurnsAppendedDuringACycleAreAllStillOffered(t *testing.T) {
	// The same race, wider. Everything after the read belongs to the next cycle,
	// however much of it arrived.
	s := newSession(t)
	s.query()
	s.dispatched(false)

	var racing []string
	for range 3 {
		racing = append(racing, s.turn())
	}
	// A second cycle ends without ever having read. It judged nothing, so it
	// may claim nothing.
	s.dispatched(false)

	assert.Equal(t, racing, uuidsOf(s.query()),
		"turns nothing was shown must survive a cycle ending")
}

func TestReadMark_ACycleThatNeverReadAdvancesNothing(t *testing.T) {
	// A cycle that queried nothing has judged nothing. Re-deriving the position
	// at write time would have it claim the whole record regardless.
	//
	// This is the FIRST cycle of the session, so nothing has ever been recorded
	// and the trivial case is all it reaches. The general property its name
	// asserts is the one below, where an earlier cycle DID read.
	s := newSession(t)
	unread := s.turn()

	s.dispatched(false)

	assert.Empty(t, s.mark(), "a cycle that read nothing has no position to claim")
	assert.Equal(t, []string{s.uuid(0), unread}, uuidsOf(s.query()))
}

func TestReadMark_ACycleThatNeverReadInheritsNothingFromAnInterruptedOne(t *testing.T) {
	// F5, and the same defect shape as the one F1 was written to fix: the
	// position was written by `session query` and never cleared, so a later
	// cycle inherited it and marked turns judged that it was never shown.
	//
	// Reachable in an ordinary session, which is why it is worth a test rather
	// than a comment: a cycle reads and is interrupted, turns land, and the next
	// cycle finishes without any rule having queried.
	s := newSession(t)
	first := s.turn()

	// Cycle one reads, then is interrupted. The mark correctly does not move.
	require.NotEmpty(t, uuidsOf(s.query()))
	s.dispatched(true)
	require.Empty(t, s.mark())

	// Turns land that nothing has been shown.
	second := s.turn()
	third := s.turn()

	// Cycle two queries NOTHING and completes.
	s.dispatched(false)

	assert.Empty(t, s.mark(),
		"a cycle that queried nothing must not claim a position an earlier cycle read")
	assert.Equal(t, []string{s.uuid(0), first, second, third}, uuidsOf(s.query()),
		"every turn is still owed to something, including the ones the interrupted cycle saw")
}

func TestReadMark_AnInterruptedCycleDoesNotHandItsPositionToTheNextOne(t *testing.T) {
	// The same rule stated as a fact about the store, so it fails on the cause
	// rather than only on the consequence.
	//
	// The position answers "how far did THIS cycle read". Left standing at the
	// end of a cycle it stops being about that cycle and becomes a claim the
	// next one inherits.
	s := newSession(t)
	s.turn()
	require.NotEmpty(t, uuidsOf(s.query()))
	require.NotEmpty(t, s.offered(), "the read position is recorded while the cycle runs")

	s.dispatched(true)
	assert.Empty(t, s.offered(), "the position is spent when the cycle ends, interrupted or not")
}

func TestReadMark_ACompletedCycleSpendsItsPositionToo(t *testing.T) {
	// The other path a cycle ends on. The mark takes the position, and then the
	// position is gone — otherwise the cycle after this one would inherit it and
	// re-claim the same turns without reading them.
	s := newSession(t)
	last := s.turn()
	s.cycle()
	require.Equal(t, last, s.mark())

	assert.Empty(t, s.offered(), "carried into the mark, and not left behind as well")

	// A following cycle that queries nothing leaves the mark exactly where the
	// cycle that DID read put it.
	s.turn()
	s.dispatched(false)
	assert.Equal(t, last, s.mark())
}

func TestReadMark_HeldCycleDoesNotHandItsPositionOnEither(t *testing.T) {
	// The path where dispatch never ran. The comment on completeCycle used to
	// say the position was kept for "the cycle that finally dispatches" to carry
	// forward, which is the same inheritance F5 is about — a later cycle
	// claiming a read it did not perform.
	//
	// What is lost by clearing is a re-read, not a turn: the mark has not moved,
	// so the next cycle is still offered everything this one saw.
	s := newSession(t)
	a := s.turn()
	require.NotEmpty(t, uuidsOf(s.query()))

	s.stop(false) // dispatch does not run, so the mark is held
	require.Empty(t, s.mark())
	assert.Empty(t, s.offered(), "a held cycle's reading is not the next cycle's to claim")

	// A later cycle that reads nothing claims nothing.
	s.dispatched(false)
	assert.Empty(t, s.mark())
	assert.Equal(t, []string{s.uuid(0), a}, uuidsOf(s.query()),
		"everything is still on offer, because nothing has judged it")
}

func TestReadMark_IsNotDraggedBackwardsByALaterNarrowerRead(t *testing.T) {
	// F6. Several rules may query within one cycle. The cycle as a whole saw the
	// furthest of them, and a position only ever moves forward.
	//
	// The record has to actually SHRINK for the guard to be reached. An earlier
	// version of this test only ever grew the record across its three queries,
	// so the backwards branch never executed and the whole guard could be
	// deleted with the suite staying green.
	s := newSession(t)
	a := s.turn()
	b := s.turn()
	c := s.turn()
	s.query()
	require.Equal(t, c, s.offered(), "the furthest read so far")

	// The record is replaced by a shorter one holding the root and the first two
	// turns — a rule reading it now genuinely reads LESS than the one before did,
	// and the position it would record sits behind the one already there.
	s.truncateTo(3)
	s.query()

	assert.Equal(t, c, s.offered(),
		"a later read that sees less must not drag the position back into re-judging settled work")

	// The entry it would have been dragged back to is a real one that the
	// truncated record still holds, so this is the guard holding rather than the
	// shorter read having nothing to offer.
	assert.Equal(t, []string{s.uuid(0), a, b}, uuidsOf(s.readWhole()))
}

// truncateTo rewrites the record to hold only its first n turns, the way a
// record replaced or truncated underneath a session looks.
func (s *session) truncateTo(n int) {
	s.t.Helper()
	b, err := os.ReadFile(s.transcriptPath)
	require.NoError(s.t, err)
	lines := strings.SplitAfter(string(b), "\n")
	require.GreaterOrEqual(s.t, len(lines), n)
	require.NoError(s.t, os.WriteFile(s.transcriptPath,
		[]byte(strings.Join(lines[:n], "")), 0o644))
	s.entries = n
}

// readWhole is every entry the record currently holds, ignoring the mark.
func (s *session) readWhole() []transcript.Entry {
	s.t.Helper()
	entries, err := transcript.Read(s.transcriptPath)
	require.NoError(s.t, err)
	return entries
}

func TestAdvanceOffered_HoldsWhenTheRecordShrinksPastTheRecordedPosition(t *testing.T) {
	// The guard at the unit, driven through the store rather than the commands,
	// so the branch is reached without depending on how a cycle happens to run.
	store := openStore(t)
	full := entriesNamed("a", "b", "c")

	require.NoError(t, advanceOffered(store, full, "c"))
	require.Equal(t, "c", metaOffered(t, store))

	// The record now holds only a and b. "b" is a real entry in it and sits
	// behind the recorded position, which is exactly the drag the guard stops.
	require.NoError(t, advanceOffered(store, entriesNamed("a", "b"), "b"))
	assert.Equal(t, "c", metaOffered(t, store))
}

func TestAdvanceOffered_MovesForwardWithinTheSameRecord(t *testing.T) {
	// The guard must not be a guard against everything. A later read that really
	// did go further moves the position, which is the whole reason it is written
	// at all.
	store := openStore(t)
	full := entriesNamed("a", "b", "c")

	require.NoError(t, advanceOffered(store, full, "a"))
	require.NoError(t, advanceOffered(store, full, "c"))
	assert.Equal(t, "c", metaOffered(t, store))
}

func TestAdvanceOffered_FirstPositionIsTakenWithNothingToCompareTo(t *testing.T) {
	// Nothing recorded yet, so there is no order to keep and the first read wins.
	store := openStore(t)
	require.NoError(t, advanceOffered(store, entriesNamed("a", "b"), "b"))
	assert.Equal(t, "b", metaOffered(t, store))
}

func TestIsBefore_OrdersTwoNamesTheRecordHolds(t *testing.T) {
	// The ordinary case: both present, and the record decides.
	e := entriesNamed("a", "b", "c")
	assert.True(t, isBefore(e, "a", "c"), "forward")
	assert.False(t, isBefore(e, "c", "a"), "backward")
	assert.False(t, isBefore(e, "b", "b"), "the same place is not before itself")
}

func TestIsBefore_HoldsWhenTheNewNameIsNotInTheRecord(t *testing.T) {
	// Moving to a name this record cannot locate would leave the position
	// somewhere no later read can resume from. The one it has works.
	e := entriesNamed("a", "b", "c")
	assert.False(t, isBefore(e, "b", "not-here"))
}

func TestIsBefore_TakesTheNewNameWhenTheRecordHoldsNeither(t *testing.T) {
	// The doc's "treated as behind the new one", and the case naive index
	// arithmetic gets wrong: -1 < -1 is false, which holds a stale position
	// nothing can locate over this cycle's own reading. Neither is locatable, so
	// there is nothing to recommend keeping the older one.
	e := entriesNamed("a", "b", "c")
	assert.True(t, isBefore(e, "gone", "also-gone"))
}

func TestIsBefore_HoldsWhenTheOldNameIsGoneFromTheRecord(t *testing.T) {
	// A record that no longer holds the recorded position is a record that lost
	// turns, and nothing left in it says where that position sat — so a new name
	// anywhere in it may be behind the old one. This is the truncation
	// recordOffered promises not to be dragged back by, and it cannot be told
	// apart from a legitimate replacement by looking at the record.
	//
	// Held either way. The tie goes to the direction it is safe to be wrong in:
	// an unlocatable position costs the next read a re-read from the start,
	// while a position dragged backwards costs re-judging settled work.
	e := entriesNamed("a", "b", "c")
	assert.False(t, isBefore(e, "gone", "b"), "a name mid-record may sit behind where the old one was")
	assert.False(t, isBefore(e, "gone", "c"), "and the end of a record that lost turns is not necessarily further on")
}

func TestIndexOf_FindsAPositionAndReportsAbsenceAsMinusOne(t *testing.T) {
	// The sentinel every branch above is written around, pinned so that changing
	// it breaks here rather than silently changing what isBefore decides.
	e := entriesNamed("a", "b", "c")
	assert.Equal(t, 0, indexOf(e, "a"))
	assert.Equal(t, 2, indexOf(e, "c"))
	assert.Equal(t, -1, indexOf(e, "not-here"))
	assert.Equal(t, -1, indexOf(nil, "a"), "an empty record holds no position")
}

// entriesNamed is a record of nothing but uuids, which is all order is read from.
func entriesNamed(uuids ...string) []transcript.Entry {
	out := make([]transcript.Entry, 0, len(uuids))
	for _, u := range uuids {
		out = append(out, transcript.Entry{UUID: u})
	}
	return out
}

// metaOffered reads the recorded read position straight off a store.
func metaOffered(t *testing.T, store sessionstate.Store) string {
	t.Helper()
	v, _, err := store.Meta(sessionstate.MetaTranscriptOffered)
	require.NoError(t, err)
	return v
}

func TestReadMark_HeldUntilTheCycleActuallyDispatches(t *testing.T) {
	// The ordering F1 asked to be made explicit rather than vacuous.
	//
	// The mark asserts a position has been judged. Until the Post events are
	// dispatched, no cycle has judged anything, so the mark must not move — and
	// nothing is lost by waiting, because the position read is remembered and
	// carried forward by whichever cycle finally dispatches.
	s := newSession(t)
	a := s.turn()
	require.NotEmpty(t, uuidsOf(s.query()))
	require.Equal(t, a, s.offered(), "the read position is recorded either way")

	// Dispatch does not happen, so the mark does not move.
	s.stop(false)
	assert.Empty(t, s.mark(),
		"the mark must not claim a position judged before any judging is dispatched")

	// Everything read is still on offer, because nothing judged it.
	assert.Equal(t, []string{s.uuid(0), a}, uuidsOf(s.query()))

	// Once a cycle dispatches, the position it read is carried forward.
	s.dispatched(false)
	assert.Equal(t, a, s.mark())
}

func TestReadMark_FirstCycleReadsTheWholeSession(t *testing.T) {
	// Nothing has been read yet, so everything is this cycle's work. Correct
	// rather than a special case.
	s := newSession(t)
	a := s.turn()
	b := s.turn()

	assert.Equal(t, []string{s.uuid(0), a, b}, uuidsOf(s.query()))
}

func TestReadMark_AdvancesWhenACycleCompletes(t *testing.T) {
	// The mark moves at the end of a cycle that finished, and the next cycle
	// sees only what happened after it.
	s := newSession(t)
	s.turn()
	last := s.turn()

	s.cycle()
	assert.Equal(t, last, s.mark())

	// Nothing new since, so there is nothing to judge.
	assert.Empty(t, s.query())

	// The next cycle's work, and only it.
	next := s.turn()
	assert.Equal(t, []string{next}, uuidsOf(s.query()))
}

func TestReadMark_DoesNotAdvanceOnAnInterruptedCycle(t *testing.T) {
	// The rule the whole mark depends on.
	//
	// A cycle already refused once has not finished, and may have judged
	// nothing. Moving the mark anyway would skip whatever it never looked at,
	// and nothing afterwards would know to go back. Re-reading a turn costs a
	// second look; skipping one loses a violation for good.
	s := newSession(t)
	first := s.turn()

	s.cycle()
	require.Equal(t, first, s.mark())

	// A second cycle reads its work, and is then interrupted.
	unjudged := s.turn()
	s.query()
	s.dispatched(true)

	assert.Equal(t, first, s.mark(), "an interrupted cycle must not move the mark")
	assert.Equal(t, []string{unjudged}, uuidsOf(s.query()),
		"the turns an interrupted cycle may never have judged must still be offered")
}

func TestReadMark_InterruptedCycleDoesNotLoseTurnsAtAll(t *testing.T) {
	// Several turns across an interruption. Every one of them is still on
	// offer, because none of them is known to have been judged.
	s := newSession(t)
	s.cycle()

	var written []string
	for range 3 {
		written = append(written, s.turn())
	}
	s.query()
	s.dispatched(true)

	assert.Equal(t, written, uuidsOf(s.query()))
}

func TestReadMark_WholeSessionIgnoresThePosition(t *testing.T) {
	// The flag means something now: a rule asking about the session as a whole
	// rather than about this cycle's work.
	s := newSession(t)
	a := s.turn()
	s.cycle()
	b := s.turn()

	assert.Equal(t, []string{b}, uuidsOf(s.query()), "the default is the unjudged part")
	assert.Equal(t, []string{s.uuid(0), a, b}, uuidsOf(s.query("--whole-session")),
		"--whole-session reads the entire record")
}

func TestReadMark_EmptyRecordLeavesThePositionStanding(t *testing.T) {
	// Writing an empty mark would mean "nothing has been read", and every
	// subsequent cycle would re-judge everything before it. A position that was
	// correct is kept rather than discarded.
	s := newSession(t)
	last := s.turn()
	s.cycle()
	require.Equal(t, last, s.mark())

	// Nothing happened since; the cycle still completes.
	s.cycle()
	assert.Equal(t, last, s.mark())
}

func TestReadMark_AdvancesAcrossSeveralCompletedCycles(t *testing.T) {
	// The mark keeps up over a long session: each cycle is offered its own turn
	// and nothing before it. The first cycle also sees the root record, since
	// nothing has been read yet when it runs.
	s := newSession(t)
	s.cycle()

	for range 4 {
		want := s.turn()
		assert.Equal(t, []string{want}, uuidsOf(s.query()))
		s.dispatched(false)
		assert.Equal(t, want, s.mark())
	}
}

func TestReadMark_UnreadablePositionReadsTheWholeRecord(t *testing.T) {
	// A position that cannot be read is not a position saying nothing needs
	// judging. Defaulting the other way would have a rule report no violations
	// because the engine could not open its own database.
	s := newSession(t)
	a := s.turn()
	s.cycle()
	require.Equal(t, a, s.mark())

	// A mark pointing at an entry that is not in this record — the record it
	// pointed into is gone, or is another conversation's.
	store, err := openEngineState(HookPayload{TranscriptPath: s.transcriptPath, Cwd: s.dir})
	require.NoError(t, err)
	require.NoError(t, store.SetMeta(sessionstate.MetaTranscriptRead, "no-such-entry"))
	require.NoError(t, store.Close())

	assert.Equal(t, []string{s.uuid(0), a}, uuidsOf(s.query()),
		"a lost position means look again, not skip")
}

func TestStop_InterruptedCycleDoesNotTakeANewBaselineEither(t *testing.T) {
	// The other half of what an interrupted cycle leaves alone. A refusal
	// outstanding is exactly the work that must stay inside the next cycle's
	// difference, so the point does not move while one is unresolved.
	s := newSession(t)
	initRepoAt(t, s.dir)
	commitFile(t, s.dir, "a.txt", "one")
	commitFile(t, s.dir, "b.txt", "two")
	// Another line, so the switch below is one a completed cycle WOULD move the
	// point for. Otherwise this passes without the interrupted path mattering.
	divergentBranch(t, s.dir, "feature")

	s.dispatched(false)
	store, err := openEngineState(HookPayload{TranscriptPath: s.transcriptPath, Cwd: s.dir})
	require.NoError(t, err)
	before, _, err := store.Meta(sessionstate.MetaBaselineBranch)
	require.NoError(t, err)
	require.NoError(t, store.Close())
	require.Equal(t, "main", before)

	// The agent switches branches, and the cycle is interrupted.
	runGit(t, s.dir, "checkout", "feature")
	s.dispatched(true)

	store, err = openEngineState(HookPayload{TranscriptPath: s.transcriptPath, Cwd: s.dir})
	require.NoError(t, err)
	defer store.Close()
	after, _, err := store.Meta(sessionstate.MetaBaselineBranch)
	require.NoError(t, err)
	assert.Equal(t, "main", after, "an interrupted cycle leaves the point where it was")
}

func TestStop_CompletedCycleRebaselinesAndSaysSo(t *testing.T) {
	// The person reading why a file stopped appearing in the difference should
	// not have to infer that the point moved.
	s := newSession(t)
	initRepoAt(t, s.dir)
	commitFile(t, s.dir, "a.txt", "one")
	commitFile(t, s.dir, "b.txt", "two")
	// A line that does not contain the point, so switching to it really is
	// leaving the history rather than renaming where the tree already is.
	divergentBranch(t, s.dir, "feature")
	s.dispatched(false)

	// A guardrail refused something, and nobody fixed it.
	store, err := openEngineState(HookPayload{TranscriptPath: s.transcriptPath, Cwd: s.dir})
	require.NoError(t, err)
	require.NoError(t, store.RecordFileCheck("broken.txt", "no-slop",
		sessionstate.Verdict{Fingerprint: "bad", Passed: false}))
	require.NoError(t, store.Close())

	runGit(t, s.dir, "checkout", "feature")
	_, stderr := s.dispatched(false)

	assert.Contains(t, stderr, "another branch")

	store, err = openEngineState(HookPayload{TranscriptPath: s.transcriptPath, Cwd: s.dir})
	require.NoError(t, err)
	defer store.Close()

	branch, _, err := store.Meta(sessionstate.MetaBaselineBranch)
	require.NoError(t, err)
	assert.Equal(t, "feature", branch, "the point followed the tree")

	// And the refusal came through it untouched.
	skippable, err := store.Skippable("broken.txt", "no-slop", "bad")
	require.NoError(t, err)
	assert.False(t, skippable, "an unfixed refusal outlives the point moving")
}

func TestStart_RecordsTheBaselineAndNeverBlocks(t *testing.T) {
	s := newSession(t)
	initRepoAt(t, s.dir)
	want := commitFile(t, s.dir, "a.txt", "one")

	s.run(newSessionStartCmd(), s.payload(false))

	store, err := openEngineState(HookPayload{TranscriptPath: s.transcriptPath, Cwd: s.dir})
	require.NoError(t, err)
	defer store.Close()

	commit, _, err := store.Meta(sessionstate.MetaBaselineCommit)
	require.NoError(t, err)
	assert.Equal(t, want, commit)
	branch, _, err := store.Meta(sessionstate.MetaBaselineBranch)
	require.NoError(t, err)
	assert.Equal(t, "main", branch)
}

func TestStart_WithoutARepositoryStillSucceeds(t *testing.T) {
	// A session that cannot start because of the engine's own bookkeeping is
	// worse than a session with no baseline.
	s := newSession(t)
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(s.dir))

	var out, errBuf bytes.Buffer
	cmd := newSessionStartCmd()
	cmd.SetIn(strings.NewReader(s.payload(false)))
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(nil)
	assert.NoError(t, cmd.Execute())
}

// initRepoAt makes an existing directory a repository, for a session whose
// project directory already exists.
func initRepoAt(t *testing.T, dir string) {
	t.Helper()
	runGit(t, dir, "init", "--initial-branch=main")
	runGit(t, dir, "config", "user.email", "test@example.invalid")
	runGit(t, dir, "config", "user.name", "Test")
}

func TestReadMark_ClearingAnInterruptedPositionCostsARereadAndNotATurn(t *testing.T) {
	// The claim discardOffered rests on, checked rather than asserted.
	//
	// Discarding the position when a cycle is interrupted could plausibly lose
	// the turns that cycle read. It does not, and the reason is that the MARK is
	// what the next read resumes from and the mark did not move — so every turn
	// the interrupted cycle saw comes round again, and the only cost is looking
	// at it twice.
	s := newSession(t)
	a := s.turn()
	b := s.turn()

	// A cycle reads both turns and is then interrupted.
	require.Equal(t, []string{s.uuid(0), a, b}, uuidsOf(s.query()))
	s.dispatched(true)
	require.Empty(t, s.offered(), "the position is discarded")

	// Every turn it saw is offered again, plus whatever arrived since.
	c := s.turn()
	assert.Equal(t, []string{s.uuid(0), a, b, c}, uuidsOf(s.query()),
		"a discarded position costs a re-read; it must not cost a turn")

	// And the cycle that does complete marks exactly what it read.
	s.dispatched(false)
	assert.Equal(t, c, s.mark())
}

// racingStore is a Store that lets another writer in through the exact window a
// read-modify-write leaves open: between the read of the position and the write
// that acts on it.
//
// This is what makes F11 a test rather than a coin flip. Two hook processes
// really do lose one another's updates, but only when they interleave — measured
// at 1 run in 200 — and a test that spawns goroutines and hopes would pass on a
// branch that fixed nothing. Injecting the interleaving makes the window
// deterministic, so the assertion is about the code and not about the scheduler.
type racingStore struct {
	sessionstate.Store
	// interlopers write on the next N reads of the offered position, one each,
	// standing in for another query getting there first.
	interlopers []string
}

func (r *racingStore) Meta(key string) (string, bool, error) {
	v, ok, err := r.Store.Meta(key)
	if key == sessionstate.MetaTranscriptOffered && len(r.interlopers) > 0 {
		// Another query wins the position after this read and before the write
		// that the read is about to inform.
		next := r.interlopers[0]
		r.interlopers = r.interlopers[1:]
		if err := r.Store.SetMeta(key, next); err != nil {
			return "", false, err
		}
	}
	return v, ok, err
}

func TestAdvanceOffered_DoesNotLoseAnotherQuerysUpdate(t *testing.T) {
	// F11. Two queries in one cycle advance the position at the same time: one
	// reaches "e", the other "b". The cycle as a whole read as far as "e", so
	// that is what must be recorded.
	//
	// Read-then-write loses it. Both read the same starting value, both decide
	// theirs is further, and whichever writes last wins regardless of which
	// actually read further — leaving "b", a position BEHIND one a real read
	// reached. That is only a re-read rather than a skipped turn, but
	// recordOffered claims the position only ever moves forward, and the claim
	// has to be true for the reasoning built on it to hold.
	// Each query is given the record as IT saw it, ending at the position it
	// reports — which is the only shape recordOffered ever produces, and the shape
	// the end-of-record guard requires. The slower query saw the record only as
	// far as "b"; the other had already read through to "e".
	shorter := entriesNamed("a", "b")
	store := &racingStore{
		Store: openStore(t),
		// The winner reaches "e" inside this call's window; this call is the one
		// carrying "b", which is behind it.
		interlopers: []string{"e"},
	}

	require.NoError(t, advanceOffered(store, shorter, "b"))

	assert.Equal(t, "e", metaOffered(t, store.Store),
		"a position another query already reached must not be overwritten by a nearer one")
}

func TestAdvanceOffered_StillAdvancesAfterLosingARace(t *testing.T) {
	// The retry must not turn into a refusal to record. Losing the swap means
	// deciding again against what the winner left — and when this read really is
	// further on, it still lands.
	full := entriesNamed("a", "b", "c", "d", "e")
	store := &racingStore{
		Store:       openStore(t),
		interlopers: []string{"b"},
	}

	require.NoError(t, advanceOffered(store, full, "e"))

	assert.Equal(t, "e", metaOffered(t, store.Store),
		"losing a swap costs a retry, not the position")
}

func TestAdvanceOffered_ConcurrentQueriesDoNotLoseTheFurthestPosition(t *testing.T) {
	// F11 against the real thing rather than an injected window. Two hook
	// processes advancing at once are two separate store handles on one database
	// file, which is the shape the injected test above abstracts — and the shape
	// the defect actually needed: a single handle serialises its own writers
	// (SetMaxOpenConns(1)), so a goroutine pair sharing one store never
	// reproduced this at all and would have "passed" against the broken code.
	//
	// Read-then-write loses 14 of 200 here. The swap loses none, and cannot: the
	// comparison happens inside the write, so a loser is told and decides again.
	full := entriesNamed("a", "b", "c", "d", "e")
	const runs = 200

	for range runs {
		dbPath := filepath.Join(t.TempDir(), "state.db")
		one, err := sessionstate.Open(dbPath)
		require.NoError(t, err)
		two, err := sessionstate.Open(dbPath)
		require.NoError(t, err)

		// Released together so the two calls overlap rather than queue.
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); <-start; _ = advanceOffered(one, full, "e") }()
		go func() { defer wg.Done(); <-start; _ = advanceOffered(two, full, "b") }()
		close(start)
		wg.Wait()

		position, _, err := one.Meta(sessionstate.MetaTranscriptOffered)
		require.NoError(t, err)
		require.Equal(t, "e", position,
			"the cycle read as far as e, so the position must not settle on the nearer b")

		require.NoError(t, one.Close())
		require.NoError(t, two.Close())
	}
}

func TestAdvanceOffered_HoldsWhenThePositionIsNotTheEndOfTheRecord(t *testing.T) {
	// F12. A record whose earlier entries were REORDERED breaks the one thing
	// index order is trusted for.
	//
	// Recorded c. The record is then rewritten to a,c,b,d and a later read names
	// b. isBefore sees c at index 1 and b at index 2 and calls that forward, so
	// the position moves to b — and the next cycle's Since returns [d], with b
	// marked judged by nothing that ever looked at it. Forwards onto an unjudged
	// turn is the one direction this bookkeeping must never err in.
	//
	// The guard is that a position is only taken when it is the END of the record
	// it came from, which is the only circumstance under which "lower index" means
	// "already read". b is mid-record here, so it is refused.
	store := openStore(t)
	require.NoError(t, advanceOffered(store, entriesNamed("a", "b", "c"), "c"))
	require.Equal(t, "c", metaOffered(t, store))

	// The record comes back reordered, and this read reports a position inside it.
	require.NoError(t, advanceOffered(store, entriesNamed("a", "c", "b", "d"), "b"))

	assert.Equal(t, "c", metaOffered(t, store),
		"a mid-record position must not drag the mark forward past an entry nothing judged")
}

func TestAdvanceOffered_TakesThePositionAtTheEndOfAReorderedRecord(t *testing.T) {
	// The guard must not become a refusal to record at all. The end of the record
	// is still a position a read genuinely reached, so it is taken — which is what
	// the caller always supplies, since it records transcript.Mark of the record
	// it just read.
	store := openStore(t)
	require.NoError(t, advanceOffered(store, entriesNamed("a", "b", "c"), "c"))

	require.NoError(t, advanceOffered(store, entriesNamed("a", "c", "b", "d"), "d"))

	assert.Equal(t, "d", metaOffered(t, store))
}
