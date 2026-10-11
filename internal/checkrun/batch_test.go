package checkrun

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/declaration"
)

// A rule whose `subjects:` script puts subjects in batches: each batch is run as one unit
// (prepare, scripts, judges), each subject still gets its own verdict, key and steps.

// batchFixture is a rule over docs/{a,b,c}.md whose subjects script puts all three in batch
// "b1" (or, with batch "", hands each alone), with the given checks. Every check and judge
// mock appends what it was handed (the batch's ids, or the lone subject's id) to the ledger.
func batchFixture(t *testing.T, batch string, checks func(dir, ledger string) []declaration.Check) *evalFixture {
	t.Helper()
	f := newEvalFixture(t, func(g *declaration.FileGuard) { g.Subjects = "./subjects.sh" }).withSession(t)
	batchKey := ""
	if batch != "" {
		batchKey = `,"batch":"` + batch + `"`
	}
	var entries []string
	for _, id := range []string{"a", "b", "c"} {
		entries = append(entries, `{"id":"`+id+`","files":["docs/`+id+`.md"]`+batchKey+`}`)
	}
	require.NoError(t, os.WriteFile(filepath.Join(f.guard.Dir, "subjects.sh"),
		[]byte("#!/bin/sh\ncat >/dev/null\necho '["+strings.Join(entries, ",")+"]'\n"), 0o755))
	f.guard.Checks = checks(f.guard.Dir, f.ledger)
	f.commitDoc(t, "docs/a.md", "a")
	f.commitDoc(t, "docs/b.md", "b")
	f.commitDoc(t, "docs/c.md", "c")
	return f
}

// handedIDs is the shell that appends, to the ledger, what the payload on stdin (or in
// $input) hands: "batch:<ids>" or "subject:<id>".
func handedIDs(ledger string) string {
	return `printf '%s' "$input" | jq -r 'if .batch then "batch:" + ([.batch.subjects[].id] | join(",")) else "subject:" + .subject.id end' >> '` + ledger + "'\n"
}

// writeScript writes an executable into the rule's folder.
func writeScript(t *testing.T, dir, name, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body), 0o755))
}

// judgeMock installs the judge "j" behind SR_CHECKS_JUDGE_MOCKS: it records what it was
// handed and answers the jq program run on its input.
func judgeMock(t *testing.T, dir, ledger, answer string) declaration.Check {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "j.md.j2"), []byte("Judge {{ batch.id }}\n"), 0o644))
	writeScript(t, dir, "mock.sh", "input=\"$(tail -n 1)\"\n"+handedIDs(ledger)+"printf '%s' \"$input\" | jq -c '"+answer+"'\n")
	t.Setenv("SR_CHECKS_JUDGE_MOCKS", `{"file-guard/docs/j":"`+filepath.Join(dir, "mock.sh")+`"}`)
	return declaration.Check{Judge: "j.md.j2", Model: "m"}
}

// failB answers every subject of the batch, failing "b".
const failB = `{verdicts: (.batch.subjects | map({key: .id, value: {pass: (.id != "b"), reasoning: (if .id == "b" then "b is wrong" else "fine" end)}}) | from_entries)}`

// passAll answers every subject of the batch with a pass.
const passAll = `{verdicts: (.batch.subjects | map({key: .id, value: {pass: true, reasoning: "fine"}}) | from_entries)}`

func ledgerLines(t *testing.T, f *evalFixture) []string {
	t.Helper()
	body, err := os.ReadFile(f.ledger)
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	return strings.Fields(string(body))
}

// outcomesOf are the outcomes of one kind of step, by subject.
func outcomesOf(outcomes []CheckOutcome, kind string) map[string]CheckOutcome {
	out := map[string]CheckOutcome{}
	for _, o := range outcomes {
		if strings.Contains(o.Kind, kind) {
			out[o.Subject] = o
		}
	}
	return out
}

// A batch of three is judged in one call; the judge refuses one subject, which alone is
// refused, and each subject's verdict is its own.
// sr:proves fileguard/one-verdict-per-subject
// sr:proves fileguard/batch-payload-only-when-batched
func TestBatch_AJudgeDecidesEachSubjectOfItsBatch(t *testing.T) {
	f := batchFixture(t, "b1", func(dir, ledger string) []declaration.Check {
		return []declaration.Check{judgeMock(t, dir, ledger, failB)}
	})
	refusals, outcomes := f.evaluateOver(t, f.base, "HEAD", false, false)
	assert.Equal(t, []string{"batch:a,b,c"}, ledgerLines(t, f), "one judge call for the batch")
	require.Len(t, refusals, 1)
	assert.Contains(t, refusals[0].Reason, "b is wrong")
	judged := outcomesOf(outcomes, ":judge:")
	require.Len(t, judged, 3)
	assert.Equal(t, "pass", judged["a"].Status)
	assert.Equal(t, "fail", judged["b"].Status)
	assert.Equal(t, "pass", judged["c"].Status)
	for _, o := range judged {
		assert.Equal(t, "b1", o.Batch, "the outcome names its batch")
	}

	// Each verdict was stored under its own subject: verify reads them, b's fail included.
	refusals, _ = f.evaluateOver(t, f.base, "HEAD", true, false)
	require.Len(t, refusals, 1)
	assert.Contains(t, refusals[0].Reason, "b is wrong")
	// A run replays the stored verdicts: the judge is not asked again.
	_, _ = f.evaluateOver(t, f.base, "HEAD", false, false)
	assert.Len(t, ledgerLines(t, f), 1)
}

// A subject with a stored verdict leaves its batch before anything runs: the batch is run
// on the rest, as a batch even when one is left; a batch left empty runs nothing.
// sr:proves fileguard/settled-subject-leaves-its-batch
// sr:proves fileguard/batch-payload-only-when-batched
func TestBatch_ACachedSubjectLeavesItsBatch(t *testing.T) {
	f := batchFixture(t, "b1", func(dir, ledger string) []declaration.Check {
		return []declaration.Check{judgeMock(t, dir, ledger, passAll)}
	})
	refusals, _ := f.evaluateOver(t, f.base, "HEAD", false, false)
	require.Empty(t, refusals)
	f.commitDoc(t, "docs/c.md", "c changed")
	refusals, outcomes := f.evaluateOver(t, f.base, "HEAD", false, false)
	require.Empty(t, refusals)
	assert.Equal(t, []string{"batch:a,b,c", "batch:c"}, ledgerLines(t, f),
		"only c is judged again, and still in the batch payload, alone")
	judged := outcomesOf(outcomes, ":judge:")
	assert.Equal(t, "cached", judged["a"].Source)
	assert.Equal(t, "ran", judged["c"].Source)

	_, _ = f.evaluateOver(t, f.base, "HEAD", false, false)
	assert.Len(t, ledgerLines(t, f), 2, "a batch whose every subject is stored runs nothing")
}

// A script check refused by one subject's verdict takes that subject out of the batch: its
// judge is deferred, and the judge is handed the others.
// sr:proves fileguard/settled-subject-leaves-its-batch
// sr:proves fileguard/one-verdict-per-subject
func TestBatch_AScriptsPerSubjectVerdictsShrinkTheBatch(t *testing.T) {
	f := batchFixture(t, "b1", func(dir, ledger string) []declaration.Check {
		writeScript(t, dir, "check.sh", "input=\"$(cat)\"\n"+handedIDs(ledger)+
			`printf '%s' "$input" | jq -c '{verdicts: (.batch.subjects | map({key: .id, value: {pass: (.id != "a"), reasoning: "a is too long"}}) | from_entries)}'`+"\n")
		return []declaration.Check{{Script: "./check.sh"}, judgeMock(t, dir, ledger, passAll)}
	})
	refusals, outcomes := f.evaluateOver(t, f.base, "HEAD", false, false)
	require.Len(t, refusals, 1)
	assert.Contains(t, refusals[0].Reason, "a is too long")
	assert.Equal(t, []string{"batch:a,b,c", "batch:b,c"}, ledgerLines(t, f))
	judged := outcomesOf(outcomes, ":judge:")
	assert.Equal(t, "skipped", judged["a"].Status, "a's judge is deferred, with the reason")
	assert.Equal(t, "pass", judged["b"].Status)
	assert.Equal(t, "pass", judged["c"].Status)
}

// A script that exits non-zero refuses every subject of its batch, with its words.
func TestBatch_AScriptRefusingRefusesTheWholeBatch(t *testing.T) {
	f := batchFixture(t, "b1", func(dir, ledger string) []declaration.Check {
		writeScript(t, dir, "check.sh", "input=\"$(cat)\"\n"+handedIDs(ledger)+"echo 'none is documented' >&2\nexit 1\n")
		return []declaration.Check{{Script: "./check.sh"}}
	})
	refusals, outcomes := f.evaluateOver(t, f.base, "HEAD", false, false)
	assert.Equal(t, []string{"batch:a,b,c"}, ledgerLines(t, f), "the script ran once, for the batch")
	require.Len(t, refusals, 3, "one refusal per subject")
	for _, r := range refusals {
		assert.Contains(t, r.Reason, "none is documented")
	}
	assert.Len(t, outcomesOf(outcomes, ":script:"), 3)
}

// A judge answer that misses a subject (a mock is not asked twice) is no verdict for any
// subject of the batch: nothing is stored, and verify says "not judged yet".
// sr:proves fileguard/batch-ids-are-exact
func TestBatch_AnAnswerMissingASubjectIsNoVerdictForAny(t *testing.T) {
	f := batchFixture(t, "b1", func(dir, ledger string) []declaration.Check {
		return []declaration.Check{judgeMock(t, dir, ledger, `{verdicts: {a: {pass: true}, b: {pass: true}}}`)}
	})
	refusals, _ := f.evaluateOver(t, f.base, "HEAD", false, false)
	require.Len(t, refusals, 3)
	for _, r := range refusals {
		assert.Contains(t, r.Reason, "could not be evaluated")
	}
	refusals, _ = f.evaluateOver(t, f.base, "HEAD", true, false)
	require.Len(t, refusals, 3)
	for _, r := range refusals {
		assert.Contains(t, r.Reason, "not judged yet")
	}
}

// A post_process maps a batch's answer to each subject's verdict and metadata, stored with
// that subject's verdict.
// sr:proves cache/stored-verdict-keeps-metadata
func TestBatch_PostProcessMetadataIsEachSubjects(t *testing.T) {
	f := batchFixture(t, "b1", func(dir, ledger string) []declaration.Check {
		c := judgeMock(t, dir, ledger, `{scores: (.batch.subjects | map({key: .id, value: (.id | length)}) | from_entries)}`)
		writeScript(t, dir, "post-process.sh", `jq -c '{verdicts: (.scores | to_entries | map({key, value: {pass: true, metadata: {score: .value, of: .key}}}) | from_entries)}'`+"\n")
		c.PostProcess = "./post-process.sh"
		return []declaration.Check{c}
	})
	refusals, outcomes := f.evaluateOver(t, f.base, "HEAD", false, false)
	require.Empty(t, refusals)
	judged := outcomesOf(outcomes, ":judge:")
	for _, id := range []string{"a", "b", "c"} {
		assert.Equal(t, map[string]any{"score": float64(1), "of": id}, judged[id].Metadata, id)
	}
	_, outcomes = f.evaluateOver(t, f.base, "HEAD", true, true)
	assert.Equal(t, "b", outcomesOf(outcomes, ":judge:")["b"].Metadata["of"], "each subject's metadata is stored with its verdict")
}

// A rule whose subjects name no batch is run as it always was: each subject alone, handed
// `subject`, one call each.
// sr:proves fileguard/batch-payload-only-when-batched
func TestBatch_SubjectsWithoutABatchAreHandedAlone(t *testing.T) {
	f := batchFixture(t, "", func(dir, ledger string) []declaration.Check {
		writeScript(t, dir, "check.sh", "input=\"$(cat)\"\n"+handedIDs(ledger))
		return []declaration.Check{{Script: "./check.sh"}}
	})
	refusals, outcomes := f.evaluateOver(t, f.base, "HEAD", false, false)
	require.Empty(t, refusals)
	lines := ledgerLines(t, f)
	assert.ElementsMatch(t, []string{"subject:a", "subject:b", "subject:c"}, lines)
	for _, o := range outcomes {
		assert.Empty(t, o.Batch)
	}
}

// prepare runs once per batch, handed the batch; its skip skips the check for every subject.
func TestBatch_PrepareRunsOncePerBatch(t *testing.T) {
	f := batchFixture(t, "b1", func(dir, ledger string) []declaration.Check {
		writeScript(t, dir, "prepare.sh", "input=\"$(cat)\"\n"+handedIDs(ledger)+"echo '{\"skip\": true}'\n")
		c := judgeMock(t, dir, ledger, passAll)
		c.Prepare = "./prepare.sh"
		return []declaration.Check{c}
	})
	refusals, outcomes := f.evaluateOver(t, f.base, "HEAD", false, false)
	require.Empty(t, refusals)
	assert.Equal(t, []string{"batch:a,b,c"}, ledgerLines(t, f), "prepare ran once and the judge never")
	judged := outcomesOf(outcomes, ":judge:")
	require.Len(t, judged, 3)
	for id, o := range judged {
		assert.Equal(t, "skipped", o.Status, id)
	}
}
