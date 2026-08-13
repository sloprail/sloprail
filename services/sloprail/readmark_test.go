package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	s := newSession(t)
	unread := s.turn()

	s.dispatched(false)

	assert.Empty(t, s.mark(), "a cycle that read nothing has no position to claim")
	assert.Equal(t, []string{s.uuid(0), unread}, uuidsOf(s.query()))
}

func TestReadMark_IsNotDraggedBackwardsByALaterNarrowerRead(t *testing.T) {
	// Several rules may query within one cycle. The cycle as a whole saw the
	// furthest of them, and a position only ever moves forward.
	s := newSession(t)
	a := s.turn()
	s.query()
	require.Equal(t, a, s.offered())

	// A second rule queries after another turn lands, then the record is
	// replaced by a shorter one — the position it recorded is no longer there.
	b := s.turn()
	s.query()
	require.Equal(t, b, s.offered())

	// Reading again with nothing new must not move the position backwards.
	s.query()
	assert.Equal(t, b, s.offered())
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
