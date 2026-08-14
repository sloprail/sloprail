package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/filemod"
	"github.com/sloprail/sloprail/internal/sessionstate"
)

// A real repository, a real store, and real hook scripts on disk. What is being
// tested is the whole path from a tree to a hook's stdin, and stubbing any of
// it would leave the joins — the part that has actually been wrong — untested.

// guardrailDir writes a declaration and its scripts into a project, and commits
// them.
//
// Committed deliberately. A guardrail's own files are ordinary project files —
// a user writes them, commits them, and edits them — so the engine reports them
// like any others, and it should: an agent that rewrites a rule mid-cycle has
// changed the project. Left uncommitted here they would be untracked files the
// cycle "created", and every assertion about which events fired would be
// counting this test's own scaffolding.
//
// It returns the directory hooks run in, and a SEPARATE directory outside the
// project for them to write to — see ledgerDir.
func guardrailDir(t *testing.T, proj, name, decl string, scripts map[string]string) string {
	t.Helper()
	dir := filepath.Join(proj, ".sloprail", "guardrails", name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "GUARDRAIL.md"), []byte(decl), 0o644))
	for file, body := range scripts {
		require.NoError(t, os.WriteFile(filepath.Join(dir, file), []byte(body), 0o755))
	}
	runGit(t, proj, "add", "-f", ".sloprail")
	runGit(t, proj, "commit", "-m", "guardrail "+name)
	return dir
}

// ledgerDir is where a test's hooks write, and it is OUTSIDE the project.
//
// A hook appending to a file in its own folder is the natural idiom, and it is
// wrong here: that folder is inside the tree being diffed, so the act of
// recording would itself be a change the next comparison reports. The test
// would then be asserting against events its own instrumentation caused — and
// worse, it would pass while doing so, since the extra events look exactly like
// real ones.
//
// One directory per test, passed to the scripts through the environment.
func ledgerDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("SLOPRAIL_TEST_LEDGER", dir)
	return dir
}

// ledger is the lines a hook appended to a file in the ledger directory.
//
// How a test observes what DID happen, and in what order. An absent file is a
// real answer: nothing ran.
func ledger(t *testing.T, dir, file string) []string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(dir, file))
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	var lines []string
	for _, l := range strings.Split(string(body), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// recordEvent is a hook that writes the event it was given, one line per run.
// It reads its payload from stdin, which is where the engine puts it, and
// writes outside the project so that recording is not itself a change.
const recordEvent = `#!/bin/sh
cat >> "$SLOPRAIL_TEST_LEDGER/events.jsonl"
printf '\n' >> "$SLOPRAIL_TEST_LEDGER/events.jsonl"
`

// baselineAt records the repository's CURRENT commit as the point the cycle
// measures from.
//
// Read here rather than passed in, so it is taken at the moment the test says
// the session begins. Captured earlier — before the guardrail's own files were
// committed — those files fall on the far side of the baseline and arrive as
// creates the cycle never made, which is the test instrumenting itself into its
// own assertions.
func baselineAt(t *testing.T, store sessionstate.Store, proj string) {
	t.Helper()
	require.NoError(t, store.SetMeta(sessionstate.MetaBaselineCommit,
		runGit(t, proj, "rev-parse", "HEAD")))
}

// dispatchIn runs the Post dispatch over a project and returns what it wrote to
// stderr, plus whether it reported having run.
//
// # The payload names no record, and that is load-bearing for what this file proves
//
// HookPayload{Cwd: proj} carries no transcript_path, no session_id and no agent
// fields, so record() falls through every branch and stableID reports "no
// transcript path on the hook payload". runPostDispatch prints that to stderr
// and leaves scope.SessionID empty — which means openRevalidation is never
// called and `rev` is NIL for every test in this file.
//
// The consequence is worth stating because it is invisible at the call sites:
// with a nil rev, Subject always answers false, `fingerprinted` is always
// false, and the entire skip-and-record block is never entered. So nothing here
// exercises revalidation, and no test in this file may count hook invocations
// across two cycles and read the result as an exemption — it would be measuring
// the no-store path and would pass whatever the exemption did.
//
// That is deliberate rather than an oversight. What this file tests is
// classification, dispatch order, TurnEnd and refusal collection, and running
// those without a store keeps them independent of it. The revalidation claims
// live in dispatch_post_revalidation_test.go, whose postSession seeds a real
// transcript precisely so that rev is non-nil there.
//
// Note also that every run here writes the identity diagnostic to stderr, so an
// assertion on that stream must name something distinctive rather than merely
// checking it is non-empty.
func dispatchIn(t *testing.T, proj string, store sessionstate.Store) (string, bool) {
	t.Helper()
	var stderr bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetErr(&stderr)
	cmd.SetOut(&bytes.Buffer{})
	ran := runPostDispatch(cmd, store, HookPayload{Cwd: proj})
	return stderr.String(), ran
}

// bindAll is a declaration binding one script to every Post kind and to
// TurnEnd, so one run records everything that was dispatched.
const bindAll = `---
hooks:
  PostFileCreate:
    - hooks:
        - type: command
          command: ./record.sh
  PostFileUpdate:
    - hooks:
        - type: command
          command: ./record.sh
  PostFileDelete:
    - hooks:
        - type: command
          command: ./record.sh
  TurnEnd:
    - hooks:
        - type: command
          command: ./record.sh
---

# Records every event it is given
`

// kindsSeen is the event kinds a recording hook was handed, in order.
func kindsSeen(t *testing.T, ldir string) []string {
	t.Helper()
	var kinds []string
	for _, line := range ledger(t, ldir, "events.jsonl") {
		// The payload is {"event":{"kind":...,...},"guardrailDir":...}. Read for
		// the kind by its spelling rather than by decoding into a type this
		// package owns, so the test reads what a real hook would read.
		i := strings.Index(line, `"kind":"`)
		require.GreaterOrEqual(t, i, 0, "hook payload carried no kind: %s", line)
		rest := line[i+len(`"kind":"`):]
		kinds = append(kinds, rest[:strings.IndexByte(rest, '"')])
	}
	return kinds
}

// TestDispatch_ClassifiesCreateUpdateDeleteFromARealTree.
//
// The three Post kinds, established by comparing a real tree against a real
// baseline. Nothing here tells the engine what happened — no tool call is
// reported, no path is announced — so the classification can only have come
// from looking, which is change_is_observed.
func TestDispatch_ClassifiesCreateUpdateDeleteFromARealTree(t *testing.T) {
	proj := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "keep.md"), []byte("one"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(proj, "gone.md"), []byte("one"), 0o644))
	runGit(t, proj, "add", ".")
	runGit(t, proj, "commit", "-m", "base")

	ldir := ledgerDir(t)
	guardrailDir(t, proj, "records", bindAll, map[string]string{"record.sh": recordEvent})
	store := openStore(t)
	baselineAt(t, store, proj)

	require.NoError(t, os.WriteFile(filepath.Join(proj, "keep.md"), []byte("two"), 0o644))
	require.NoError(t, os.Remove(filepath.Join(proj, "gone.md")))
	require.NoError(t, os.WriteFile(filepath.Join(proj, "fresh.md"), []byte("new"), 0o644))

	_, ran := dispatchIn(t, proj, store)
	require.True(t, ran)

	got := kindsSeen(t, ldir)
	sorted := append([]string(nil), got...)
	sort.Strings(sorted)
	assert.Equal(t, []string{
		filemod.KindPostCreate, filemod.KindPostDelete, filemod.KindPostUpdate, "TurnEnd",
	}, sorted)
}

// TestDispatch_TurnEndFiresOnceAndCarriesNoSubject is turn_end_subjectless.
//
// Two claims, and the second is the one with a wrong answer available. A
// TurnEnd carrying a path would let a matcher narrow it to one file, and a rule
// about the cycle as a whole — that a required artifact was produced — would
// then run per file or not at all.
//
// The count matters as much as the shape: a cycle ends once. Fired per changed
// file, every completeness rule would run as many times as the cycle touched
// files, and a rule that refuses when an artifact is missing would refuse that
// many times over.
func TestDispatch_TurnEndFiresOnceAndCarriesNoSubject(t *testing.T) {
	proj := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "seed.md"), []byte("seed"), 0o644))
	runGit(t, proj, "add", ".")
	runGit(t, proj, "commit", "-m", "base")

	ldir := ledgerDir(t)
	guardrailDir(t, proj, "records", bindAll, map[string]string{"record.sh": recordEvent})
	store := openStore(t)
	baselineAt(t, store, proj)

	// Several changed files, so "fired once" is a real constraint rather than
	// something a single-file tree would satisfy by accident.
	for _, n := range []string{"a.md", "b.md", "c.md"} {
		require.NoError(t, os.WriteFile(filepath.Join(proj, n), []byte("x"), 0o644))
	}

	_, ran := dispatchIn(t, proj, store)
	require.True(t, ran)

	lines := ledger(t, ldir, "events.jsonl")
	var turnEnds []string
	for _, l := range lines {
		if strings.Contains(l, `"kind":"TurnEnd"`) {
			turnEnds = append(turnEnds, l)
		}
	}
	require.Len(t, turnEnds, 1, "a cycle ends once, however many files it touched")

	// No subject. Asserted on the payload the hook actually received: the
	// fields object must be empty, so there is nothing for a matcher to narrow
	// on.
	assert.NotContains(t, turnEnds[0], `"path"`, "TurnEnd must carry no file")
	assert.Regexp(t, `"kind":"TurnEnd"[,}]`, turnEnds[0],
		"TurnEnd must carry the kind and nothing else about a subject")

	// And the file events did fire, so the run above was not vacuous.
	assert.Len(t, kindsSeen(t, ldir), 4, "three files plus one TurnEnd")
}

// TestDispatch_TurnEndFiresWhenTheCycleChangedNothing.
//
// The case whose absence would be a lie. A cycle that touched no files still
// ended, and the rules that fire on completeness — a required artifact never
// produced — are exactly the ones whose violation looks like nothing having
// happened. Firing TurnEnd only when something changed would make those rules
// silent precisely when they should speak.
func TestDispatch_TurnEndFiresWhenTheCycleChangedNothing(t *testing.T) {
	proj := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "seed.md"), []byte("seed"), 0o644))
	runGit(t, proj, "add", ".")
	runGit(t, proj, "commit", "-m", "base")

	ldir := ledgerDir(t)
	guardrailDir(t, proj, "records", bindAll, map[string]string{"record.sh": recordEvent})
	store := openStore(t)
	baselineAt(t, store, proj)

	// Nothing is written. The tree is exactly the baseline.
	_, ran := dispatchIn(t, proj, store)
	require.True(t, ran)

	assert.Equal(t, []string{"TurnEnd"}, kindsSeen(t, ldir),
		"an unchanged tree produces no file events and one TurnEnd")
}

// TestDispatch_UntouchedFileProducesNoEvent is untouched_stays_silent.
//
// The repository holds files the cycle never touched. Reporting them would put
// the whole project in front of every guardrail on the first cycle and bury
// what the agent actually did.
func TestDispatch_UntouchedFileProducesNoEvent(t *testing.T) {
	proj := initRepo(t)
	for _, n := range []string{"old-a.md", "old-b.md", "old-c.md"} {
		require.NoError(t, os.WriteFile(filepath.Join(proj, n), []byte("existing"), 0o644))
	}
	runGit(t, proj, "add", ".")
	runGit(t, proj, "commit", "-m", "base")

	ldir := ledgerDir(t)
	guardrailDir(t, proj, "records", bindAll, map[string]string{"record.sh": recordEvent})
	store := openStore(t)
	baselineAt(t, store, proj)

	require.NoError(t, os.WriteFile(filepath.Join(proj, "touched.md"), []byte("new"), 0o644))

	_, ran := dispatchIn(t, proj, store)
	require.True(t, ran)

	for _, line := range ledger(t, ldir, "events.jsonl") {
		for _, untouched := range []string{"old-a.md", "old-b.md", "old-c.md"} {
			assert.NotContainsf(t, line, untouched, "a file the cycle never touched was reported")
		}
	}
	assert.Equal(t, []string{filemod.KindPostCreate, "TurnEnd"}, kindsSeen(t, ldir))
}

// TestDispatch_APostRefusalDoesNotBlock is before_refusable_only.
//
// A hook exiting non-zero on a Post event has refused, and the refusal must be
// reported — but the work it describes has already landed, so nothing may be
// prevented and nothing may claim to have been.
//
// Three things are asserted together, and each covers a way the other two could
// pass vacuously:
//
//   - the hook RAN (its ledger line exists), or a refusal that never happened
//     would prove nothing about refusals being survivable;
//   - the dispatch still reported having run, so the read mark is not held
//     hostage by a rule objecting to work that already landed;
//   - TurnEnd still fired AFTERWARDS, which is what distinguishes "the refusal
//     was reported and the cycle carried on" from "the refusal stopped the
//     dispatch where it stood".
func TestDispatch_APostRefusalDoesNotBlock(t *testing.T) {
	proj := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "seed.md"), []byte("seed"), 0o644))
	runGit(t, proj, "add", ".")
	runGit(t, proj, "commit", "-m", "base")

	const refuseCreate = `---
hooks:
  PostFileCreate:
    - hooks:
        - type: command
          command: ./refuse.sh
  TurnEnd:
    - hooks:
        - type: command
          command: ./record.sh
---

# Refuses every created file, after the fact
`
	ldir := ledgerDir(t)
	guardrailDir(t, proj, "objects", refuseCreate, map[string]string{
		"refuse.sh": "#!/bin/sh\ncat >/dev/null\necho ran >> \"$SLOPRAIL_TEST_LEDGER/refused.log\"\necho 'this file should not exist' >&2\nexit 1\n",
		"record.sh": recordEvent,
	})
	store := openStore(t)
	baselineAt(t, store, proj)

	landed := filepath.Join(proj, "unwanted.md")
	require.NoError(t, os.WriteFile(landed, []byte("already written"), 0o644))

	stderr, ran := dispatchIn(t, proj, store)

	// The hook ran and objected.
	require.Len(t, ledger(t, ldir, "refused.log"), 1,
		"the refusing hook never ran, so this proves nothing about after-the-fact refusals")
	assert.Contains(t, stderr, "this file should not exist", "the refusal must be reported")
	assert.Contains(t, stderr, "objects", "a refusal names the guardrail that produced it")

	// The dispatch carried on past the refusal rather than returning at it.
	// Asserted on what actually ran, because that is the property: TurnEnd is
	// bound after the refusing rule and still fired.
	assert.Equal(t, []string{"TurnEnd"}, kindsSeen(t, ldir),
		"TurnEnd must still fire after a Post hook refused — the dispatch carried on")

	// The cycle reports that it did NOT complete, and that is deliberate. A
	// Post refusal blocks the turn, so the agent goes round again in this same
	// session over these same turns — and a mark advanced now would put exactly
	// the work it has to fix behind it, never to be offered again. Re-reading a
	// turn costs a second look; skipping one loses a violation for good.
	assert.False(t, ran, "a refused cycle has an objection outstanding, so its mark must not advance")

	// And above all, the work is still there. "Prevented" is a claim about the
	// tree, not about what came back on a stream.
	_, err := os.Stat(landed)
	assert.NoError(t, err, "a refusal after the fact must not remove the file — it demands a correction")
}

// TestDispatch_RefusalDoesNotStopTheOtherFiles.
//
// One file's rule objecting must not cost the other files their judging. The
// refusal is per event, and a dispatcher returning at the first one would
// silently drop every event after it — a rule that fires on file A quietly
// disabling every rule bound to files B and C.
func TestDispatch_RefusalDoesNotStopTheOtherFiles(t *testing.T) {
	proj := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "seed.md"), []byte("seed"), 0o644))
	runGit(t, proj, "add", ".")
	runGit(t, proj, "commit", "-m", "base")

	ldir := ledgerDir(t)

	// One guardrail refuses every create; a second records them. Both are bound
	// to the same kind, so the recorder's ledger says how many creates survived
	// the first one's objections.
	guardrailDir(t, proj, "refuser", `---
hooks:
  PostFileCreate:
    - hooks:
        - type: command
          command: ./refuse.sh
---

# Refuses
`, map[string]string{"refuse.sh": "#!/bin/sh\ncat >/dev/null\necho no >&2\nexit 1\n"})

	guardrailDir(t, proj, "recorder", `---
hooks:
  PostFileCreate:
    - hooks:
        - type: command
          command: ./record.sh
---

# Records
`, map[string]string{"record.sh": recordEvent})

	store := openStore(t)
	baselineAt(t, store, proj)

	for _, n := range []string{"a.md", "b.md", "c.md"} {
		require.NoError(t, os.WriteFile(filepath.Join(proj, n), []byte("x"), 0o644))
	}

	// The return value is not the subject here — one of these rules refuses, so
	// the cycle reports incomplete by design. What matters is that the OTHER
	// files were still judged.
	dispatchIn(t, proj, store)

	assert.Len(t, kindsSeen(t, ldir), 3,
		"every changed file must still be judged after another rule refused one of them")
}

// TestDispatch_RenameIsACreateAndADelete, end to end.
//
// The rename reaches the hooks as the two events it actually is. A rule bound
// to deletion has to hear that the old path is gone — this is the case where a
// single "renamed" event, or a parser that lost one half, would leave it silent.
func TestDispatch_RenameIsACreateAndADelete(t *testing.T) {
	proj := initRepo(t)
	require.NoError(t, os.MkdirAll(filepath.Join(proj, "dir"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(proj, "dir", "old.md"),
		[]byte("content stable enough that git calls this a rename"), 0o644))
	runGit(t, proj, "add", ".")
	runGit(t, proj, "commit", "-m", "base")

	ldir := ledgerDir(t)
	guardrailDir(t, proj, "records", bindAll, map[string]string{"record.sh": recordEvent})
	store := openStore(t)
	baselineAt(t, store, proj)

	runGit(t, proj, "mv", "dir/old.md", "dir/new.md")

	_, ran := dispatchIn(t, proj, store)
	require.True(t, ran)

	// Asserted as PAIRS, one ledger line at a time, rather than by joining every
	// line and asking whether four substrings appear somewhere in the result.
	//
	// The joined form cannot fail on the thing this test is named for. All four
	// strings are present in the blob whichever way the kinds are attached, so a
	// classifier that reported the delete against dir/new.md and the create
	// against dir/old.md — the exact inversion — would pass it. What the test
	// claims is which path arrived as which kind, and only a per-line check can
	// say so.
	assert.Equal(t,
		map[string]string{"dir/old.md": filemod.KindPostDelete, "dir/new.md": filemod.KindPostCreate},
		kindByPath(t, ldir),
		"a rename must arrive as a delete of the source and a create of the destination")
}

// kindByPath is which event kind each path arrived as, read off the ledger one
// line at a time.
//
// The pairing is the whole claim of a classification test, and it is precisely
// what is lost by joining the lines: a path and a kind sitting in the same blob
// say nothing about whether they arrived together.
//
// Paths are read from the event, so a kind carrying none — TurnEnd — is left
// out rather than recorded under "".
func kindByPath(t *testing.T, ldir string) map[string]string {
	t.Helper()
	got := map[string]string{}
	for _, line := range ledger(t, ldir, "events.jsonl") {
		var rec struct {
			Event struct {
				Kind   string `json:"kind"`
				Fields struct {
					Path string `json:"path"`
				} `json:"fields"`
			} `json:"event"`
		}
		require.NoError(t, json.Unmarshal([]byte(line), &rec), "hook payload was not JSON: %s", line)
		if rec.Event.Fields.Path == "" {
			continue
		}
		got[rec.Event.Fields.Path] = rec.Event.Kind
	}
	return got
}

// TestDispatch_UntrackedFileIsReported.
//
// A file the agent wrote and never staged is a change the baseline does not
// have. It appears in no `git diff`, so an implementation asking only that
// question reports nothing at all — and a rule about what the agent writes
// never fires for exactly the files it was written for.
func TestDispatch_UntrackedFileIsReported(t *testing.T) {
	proj := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "seed.md"), []byte("seed"), 0o644))
	runGit(t, proj, "add", ".")
	runGit(t, proj, "commit", "-m", "base")

	ldir := ledgerDir(t)
	guardrailDir(t, proj, "records", bindAll, map[string]string{"record.sh": recordEvent})
	store := openStore(t)
	baselineAt(t, store, proj)

	require.NoError(t, os.WriteFile(filepath.Join(proj, "scratch.md"), []byte("never staged"), 0o644))

	_, ran := dispatchIn(t, proj, store)
	require.True(t, ran)

	lines := strings.Join(ledger(t, ldir, "events.jsonl"), "\n")
	assert.Contains(t, lines, "scratch.md", "an untracked file is the cycle's work")
	assert.Contains(t, lines, filemod.KindPostCreate)
}

// TestDispatch_ChangedAndChangedBackProducesNoFileEvent.
//
// Written to during the cycle and written back. There is no difference from the
// baseline, so there is nothing for a rule to be about — only the cycle's own
// TurnEnd remains.
func TestDispatch_ChangedAndChangedBackProducesNoFileEvent(t *testing.T) {
	proj := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "flip.md"), []byte("original"), 0o644))
	runGit(t, proj, "add", ".")
	runGit(t, proj, "commit", "-m", "base")

	ldir := ledgerDir(t)
	guardrailDir(t, proj, "records", bindAll, map[string]string{"record.sh": recordEvent})
	store := openStore(t)
	baselineAt(t, store, proj)

	require.NoError(t, os.WriteFile(filepath.Join(proj, "flip.md"), []byte("CHANGED"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(proj, "flip.md"), []byte("original"), 0o644))

	_, ran := dispatchIn(t, proj, store)
	require.True(t, ran)

	assert.Equal(t, []string{"TurnEnd"}, kindsSeen(t, ldir),
		"a file reverted to its baseline content differs from nothing")
}

// TestDispatch_WithNoBaselineStillEndsTheCycle.
//
// No point was ever recorded — a project without git, or one with no commit
// yet. There is no difference to report, and that is not a failure: the cycle
// still ended, so TurnEnd still fires and the dispatch still counts as having
// run.
func TestDispatch_WithNoBaselineStillEndsTheCycle(t *testing.T) {
	proj := initRepo(t)
	ldir := ledgerDir(t)
	guardrailDir(t, proj, "records", bindAll, map[string]string{"record.sh": recordEvent})
	store := openStore(t) // no baseline written

	require.NoError(t, os.WriteFile(filepath.Join(proj, "a.md"), []byte("x"), 0o644))

	_, ran := dispatchIn(t, proj, store)
	assert.True(t, ran, "a cycle with no baseline still ended")
	assert.Equal(t, []string{"TurnEnd"}, kindsSeen(t, ldir))
}

// TestDispatch_UnboundExtractorDoesNotRun is extractor_runs_bound.
//
// A project whose only rule is about the cycle as a whole must not pay for the
// tree comparison. The recorder below binds to TurnEnd alone, so no file kind is
// bound and the file module is never asked — even though the tree really did
// change.
func TestDispatch_UnboundExtractorDoesNotRun(t *testing.T) {
	proj := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "seed.md"), []byte("seed"), 0o644))
	runGit(t, proj, "add", ".")
	runGit(t, proj, "commit", "-m", "base")

	ldir := ledgerDir(t)
	guardrailDir(t, proj, "cycle-only", `---
hooks:
  TurnEnd:
    - hooks:
        - type: command
          command: ./record.sh
---

# Binds to the cycle, not to files
`, map[string]string{"record.sh": recordEvent})

	store := openStore(t)
	baselineAt(t, store, proj)

	require.NoError(t, os.WriteFile(filepath.Join(proj, "changed.md"), []byte("real change"), 0o644))

	_, ran := dispatchIn(t, proj, store)
	require.True(t, ran)

	assert.Equal(t, []string{"TurnEnd"}, kindsSeen(t, ldir),
		"no rule binds to a file kind, so no file event may be produced")
}

// TestDispatch_DisabledGuardrailContributesNothing is disabled_guardrail_inert
// at this hook point: turning a rule off is a declaration rather than a
// deletion, and it must cost nothing at the end of a cycle either.
func TestDispatch_DisabledGuardrailContributesNothing(t *testing.T) {
	proj := initRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "seed.md"), []byte("seed"), 0o644))
	runGit(t, proj, "add", ".")
	runGit(t, proj, "commit", "-m", "base")

	ldir := ledgerDir(t)
	guardrailDir(t, proj, "off", `---
enabled: false
hooks:
  PostFileCreate:
    - hooks:
        - type: command
          command: ./record.sh
  TurnEnd:
    - hooks:
        - type: command
          command: ./record.sh
---

# Switched off
`, map[string]string{"record.sh": recordEvent})

	store := openStore(t)
	baselineAt(t, store, proj)
	require.NoError(t, os.WriteFile(filepath.Join(proj, "new.md"), []byte("x"), 0o644))

	_, ran := dispatchIn(t, proj, store)
	assert.True(t, ran)
	assert.Empty(t, kindsSeen(t, ldir), "a disabled guardrail contributes no hook runs")
}
