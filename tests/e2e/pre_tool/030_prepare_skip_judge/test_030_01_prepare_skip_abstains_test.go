// Package e2e covers the ABSTAIN prepare outcome: a JUDGE check's prepare can emit
// `{"skip": true}` on its stdout envelope to say "do not invoke the model, and this
// check reaches no verdict of its own" — the check DROPS OUT of the chain and the
// other checks (or the default permit) decide.
//
// # Why this outcome exists
//
// A judge is only meaningful for some subjects. A task-review guard has nothing to
// judge when the task is not in_review; running the model there spends a call to
// reach a foregone conclusion. So prepare gained a `skip` signal beside its
// existing outcomes (exit non-zero -> refuse; exit zero, no skip -> judge runs;
// malformed stdout -> refuse): exit zero WITH `skip:true` invokes no model and the
// check abstains. Abstain, NOT permit — a skip must not stand in for an affirmative
// verdict that could mask a LATER check that would refuse.
//
// # What these prove, and why the mock drives it
//
// Everything runs through a10n-claude-mock exactly as the rest of the e2e: the
// mock's Write tool fires this repo's real plugin, which reaches the real
// gate dispatch out of the installed `.sloprail/gate/`. Only the MODEL
// a judge invokes is replaced — by InstallJudgeClaudeCapturing, a `claude` on PATH
// that both RECORDS the rendered prompt and writes a fixed verdict. The verdict is
// pinned to `pass: false` (a REFUSAL) on purpose: if a skip ever leaked into a
// model call, the write would be REFUSED and the skip test would fail — a skip that
// secretly judged cannot pass here. And JudgePrompt reading back EMPTY is the
// direct witness that no prompt was ever rendered, i.e. no model was invoked.
//
// The guard is a PreFileWrite gate, so a verdict lands on the PRE write and is observable on
// the same Run (a refusal marker in the stream, or a permitted write). prepare
// records every path it saw into its own ledger, so "prepare ran but the judge did
// not" is a checkable pair rather than an inference.
package e2e

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// skipReviewGuard is a PreFileWrite gate on guarded/ markdown whose JUDGE check
// carries a prepare. It is preceded by the standing require-known-result.sh check:
// the judge reads the pending bytes, so a write whose result the engine could not
// compute is refused before the judge (a Write always carries a known result, so
// it never fires here). The prepare inspects the file's path off the FLAT
// payload (`.event.path`) and:
//   - for a path under guarded/skip-me/, emits `{"skip": true}` — the judge is not
//     invoked and the check abstains (the "not in_review, nothing to judge" case);
//   - otherwise, emits a real `additionalContext.review_note` — the judge runs
//     against it.
//
// Either way it appends the path it saw to its own ledger (SR_GUARDRAIL_DIR/seen),
// so a test can prove prepare ran in BOTH cases while the model ran in only one.
const skipReviewGuard = `on:
  - event: PreFileWrite
    match: 'event.path startsWith "guarded/" and event.path endsWith ".md"'
checks:
  - script: ./require-known-result.sh
  - prepare: ./prepare.sh
    judge: ./judge.md.j2
    model: size-md
    timeout: 25s
`

// prepareScript is the abstain-capable prepare. It emits the skip envelope for the
// skip-me subtree and the additionalContext envelope otherwise, and records the
// path it was handed either way.
//
// reviewNoteMarker is embedded in the additionalContext so a test can assert the
// judge's prompt really carried prepare's output (not just that a judge ran).
const prepareScript = `#!/bin/sh
payload="$(cat)"
echo "$payload" >> "$SR_GUARDRAIL_DIR/seen"
path="$(printf '%s' "$payload" | sed -n 's/.*"path"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)"
case "$path" in
  guarded/skip-me/*)
    # Not in review — nothing for the model to judge. Skip: invoke no model, and
    # abstain (this check reaches no verdict; other checks decide).
    printf '%s\n' '{"skip": true}'
    ;;
  *)
    # In review — hand the judge its context and let the model decide.
    printf '%s\n' '{"additionalContext": {"review_note": "ZZ_REVIEW_NOTE_MARKER prepared for the judge"}}'
    ;;
esac
exit 0
`

// reviewNoteMarker is the distinctive phrase prepare puts in additionalContext for
// the judge case. Finding it in the rendered prompt proves prepare's output reached
// the template; NOT finding a prompt at all proves the judge never ran.
const reviewNoteMarker = "ZZ_REVIEW_NOTE_MARKER prepared for the judge"

// judgeTemplate renders the prepared note and the file's own content. It is only
// ever rendered in the non-skip case; the skip case must produce no prompt at all.
const judgeTemplate = `You are a REVIEW guardrail. Judge the file below.

<note>{{ additionalContext.review_note }}</note>

<file path="{{ event.path }}">
{{ event.newContent }}
</file>

Answer with a verdict.`

// seenPaths is the list of payloads prepare recorded — one per ask.
func seenPaths(e *harness.Env, proj string) []string {
	return e.GateLedgerLines(proj, "skip-review", "seen")
}

// requireKnownResult refuses a write whose result the engine could not compute
// (`resultKnown != true`), because the judge below decides on the pending bytes.
const requireKnownResult = `#!/bin/sh
if [ "$(jq -r '.event.resultKnown // false')" != "true" ]; then
  echo '{"reason":"the pending content could not be computed; write the file content directly"}'
  exit 1
fi
exit 0
`

// installReviewGuard writes the test's gate AND COMMITS it before the cycle.
//
// The commit is not incidental — it is the same discipline engine_repo_judges'
// project() keeps. This repo's plugin is enabled in every mock project, and the
// plugin SHIPS the authoring-slop gate and file-guard, which match any `.sh` / `.md.j2`
// under `.sloprail/gate/` — exactly where this gate's own prepare.sh and
// judge.md.j2 sit. Left UNCOMMITTED, those files are part of the cycle's git diff,
// so authoring-slop fires on THEM and its judge writes into the SAME shared
// judge-prompt.txt this test captures — making "no prompt captured on a skip" fail
// for a reason that has nothing to do with the guard under test. Committing the
// guard's own files takes them out of the cycle's diff, so only the test's own
// write (guarded/…) drives the guards, and the only judge that can render is this
// test's own. files are the gate's scripts/templates beside its yaml.
func installReviewGuard(e *harness.Env, proj, guardYAML string, files map[string]string) {
	files["require-known-result.sh"] = requireKnownResult
	e.Gate(proj, "skip-review", guardYAML, files)
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "install skip-review guard")
}

// T030_01: prepare emits `{"skip": true}` on a guard whose only check is the judge
// -> the write is PERMITTED and the model is NEVER invoked.
//
// The judge stub would REFUSE (pass:false) and would leave a captured prompt behind
// if it ran. The assertions are three, each independently fatal to a broken skip:
// the write is permitted (a leaked judge would have refused), prepare DID run (its
// ledger recorded the path, so this is a real abstain and not the guard failing to
// fire), and NO judge prompt was captured (the model was never rendered a prompt,
// let alone invoked). Permitted because the one check abstained and nothing else
// refused — the chain ends with no refusal, which is the default permit.
func TestT030_01_PrepareSkipAbstainsAndPermitsWithoutModel(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installReviewGuard(e, proj, skipReviewGuard, map[string]string{
		"prepare.sh":  prepareScript,
		"judge.md.j2": judgeTemplate,
	})
	// A judge that REFUSES and CAPTURES its prompt — so a skip that wrongly reached
	// the model would both refuse the write and leave a prompt behind.
	e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt", `{"pass": false, "reasoning": "should never run on a skip"}`)

	res := e.Run(proj, "s-030-01", "write a not-in-review file", Turns("done",
		Write("w1", "guarded/skip-me/note.md", "a body the judge must never see"),
	))

	// The write went through — the abstain left nothing to refuse. A judge that ran
	// would have refused (pass:false), so a permitted write is proof the model was
	// not consulted.
	assert.True(t, res.Permitted(),
		"skip:true must abstain and (with no other check) permit the write; a refusal means the judge ran despite the skip:\n"+res.Output)

	// prepare DID run — so the permit is a real abstain, not the guard failing to match.
	assert.NotEmpty(t, seenPaths(e, proj),
		"prepare never ran — the permit is the guard not firing, not a skip")

	// The decisive one: no judge prompt was ever rendered, so no model was invoked.
	assert.Empty(t, e.JudgePrompt(proj, "judge-prompt.txt"),
		"a judge prompt was captured on a skip — the model was invoked when skip:true said not to")
}

// T030_02: prepare emits a real additionalContext (no skip) -> the judge RUNS,
// against prepare's output — the UNCHANGED behaviour, proven beside the skip so the
// two outcomes are shown to diverge on the same guard.
//
// Here the model IS invoked: the captured prompt exists and carries prepare's
// review_note (so additionalContext reached the template) and the file's own
// content (so event.newContent did too). The stub refuses, so the write
// is blocked — which is the observable proof the judge actually decided.
func TestT030_02_NoSkipRunsTheJudgeWithPreparedContext(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installReviewGuard(e, proj, skipReviewGuard, map[string]string{
		"prepare.sh":  prepareScript,
		"judge.md.j2": judgeTemplate,
	})
	e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt", `{"pass": false, "reasoning": "REVIEW: blocked for the test"}`)

	const marker = "ZZ_FILE_BODY_MARKER the reviewed body"
	res := e.Run(proj, "s-030-02", "write an in-review file", Turns("done",
		Write("w1", "guarded/in-review/note.md", "# note\n\n"+marker+"\n"),
	))

	// The judge ran and refused (pass:false), so the write is blocked.
	assert.True(t, res.Refused(),
		"a non-skip prepare must let the judge run; the refusing stub should have blocked the write:\n"+res.Output)

	prompt := e.JudgePrompt(proj, "judge-prompt.txt")
	if prompt == "" {
		t.Fatalf("the judge never ran on a non-skip prepare — no prompt captured (did prepare pass and the judge run?)")
	}
	// prepare's additionalContext reached the template.
	assert.Contains(t, prompt, reviewNoteMarker,
		"prepare's additionalContext.review_note did not reach the judge prompt — the prepare->template wiring is broken:\n"+prompt)
	// The file's own content reached the same prompt.
	assert.Contains(t, prompt, marker,
		"the file's own content did not reach the judge prompt — event.newContent wiring is broken:\n"+prompt)
}
