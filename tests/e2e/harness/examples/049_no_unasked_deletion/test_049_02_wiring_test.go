package e2e

// TODO(D3): drive verdict via a10n-claude-mock once a10n-cli#470 lands + new mock
// on PATH; today InstallJudgeClaudeCapturing supplies the verdict AND records the
// rendered prompt.
//
// PREPARE -> TEMPLATE WIRING for no-unasked-deletion. The judge-tier behavioural
// tests (test_049_01) key on the stub's reasoning, which proves the verdict path
// but NOT that the change's diff and its citations reached the judge TEMPLATE:
// change-is-clean-and-absolute.md.j2 renders `{{ change }}` (the engine's diff of
// the event's old and new content) and `event.citations`, and a template reading
// a value nobody supplied would render it empty. These tests close that: they capture the rendered prompt and assert
// the unified DIFF (including the collaterally-removed line, as a `-` line) AND the
// grounded quote are in it, and that a DIFFERENT removal yields a DIFFERENT prompt.
//
// The seed file is COMMITTED before the removing write. This is deliberate and
// necessary: the guard judges twice on a passing verdict — once at PreFileUpdate
// (oldContent = the file on disk) and once at Stop (oldContent = the session's git
// baseline). The capturing shim keeps the last render, so the seed must be in the
// git baseline too, or the Stop render would diff against an empty old and show no
// `-` lines. A memory that already exists in the repo is the realistic case anyway.

import (
	"github.com/sloprail/sloprail/tests/e2e/harness"
	"strings"
	"testing"
)

// seedCommittedMemory writes a memory file and commits it, so it is in the git
// baseline the Stop-time diff is computed against.
func seedCommittedMemory(t *testing.T, e *env, proj, rel, body string) {
	t.Helper()
	e.WriteFile(proj, rel, body)
	e.CommitSeedThenRules(proj, "seed "+rel)
}

// T049_11: the cited quote AND the unified diff reach the judge's prompt. A
// grounded-ask removal reaches the judge; the template renders the quote off
// `.event.citations` and the engine's `change` diff of old->new. Both the quote and
// the diff's collaterally-removed line must appear in the rendered prompt — only
// possible if the template interpolated both. (A passing verdict here, so the point is the PROMPT, not
// the block; the block path with the same real diff is T049_09.)
func TestT049_11_PreparedQuoteAndDiffReachJudgePrompt(t *testing.T) {
	e := newEnv(t)
	proj := nudProject(t, e)

	// A distinctive collateral line the removal also drops, and a distinctive
	// user prompt the change cites (so the citation resolves).
	const collateral = "provenance: ZZ_PROV derived from source X"
	const prompt = "please remove the second line"
	seedCommittedMemory(t, e, proj, "memories/topic.md",
		"keep this line\nremove the second line\n"+collateral+"\n")

	e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt", `{"pass": true, "reasoning": ""}`)

	sess := "s-049-11"
	e.Run(proj, sess, prompt, Turns("done",
		// Removes the asked line AND the collateral provenance line.
		srWrite("w1", "memories/topic.md", "keep this line\n", "please remove the second line"),
	).ThenCommit("write the files", harness.CitesUser("please remove the second line")))

	captured := e.JudgePrompt(proj, "judge-prompt.txt")
	if captured == "" {
		t.Fatalf("the judge never ran — no prompt captured (did the grounded ask reach the judge?)")
	}
	// The grounded quote: present only if prepare read it off the marker AND the
	// template rendered additionalContext.asked_quote.
	if !strings.Contains(captured, prompt) {
		t.Fatalf("the asked_quote did not reach the judge prompt — prepare/template wiring is broken:\n%s", captured)
	}
	// The unified DIFF, including the collaterally-removed line as a `-` line:
	// present only if the engine computed the change AND the template rendered
	// it. This is the diff the judge rules "clean" on.
	if !strings.Contains(captured, "-"+collateral) {
		t.Fatalf("the removed collateral line did not reach the judge prompt as a diff `-` line — the change wiring is broken:\n%s", captured)
	}
}

// T049_12: a DIFFERENT removal yields a DIFFERENT prompt. Without this, a prompt
// that ignored prepare (or hard-coded a diff) would still pass T049_11. Two runs
// removing two different collateral lines must produce two prompts, each carrying
// its OWN removed line and not the other's — which can only happen if the diff is
// computed from the actual change each time.
func TestT049_12_DifferentRemovalYieldsDifferentPrompt(t *testing.T) {
	const prompt = "please remove the second line"
	runWithCollateral := func(tag, collateral string) string {
		e := newEnv(t)
		proj := nudProject(t, e)
		seedCommittedMemory(t, e, proj, "memories/topic.md",
			"keep this line\nremove the second line\n"+collateral+"\n")
		e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt", `{"pass": true, "reasoning": ""}`)
		e.Run(proj, "s-049-12-"+tag, prompt, Turns("done",
			srWrite("w1", "memories/topic.md", "keep this line\n", "please remove the second line"),
		).ThenCommit("write the files", harness.CitesUser("please remove the second line")))
		p := e.JudgePrompt(proj, "judge-prompt.txt")
		if p == "" {
			t.Fatalf("[%s] the judge never ran — no prompt captured", tag)
		}
		return p
	}

	const collA = "provenance: AAA_ONLY from the northern archive"
	const collB = "provenance: BBB_ONLY from the southern archive"
	promptA := runWithCollateral("A", collA)
	promptB := runWithCollateral("B", collB)

	if promptA == promptB {
		t.Fatalf("two different removals produced identical judge prompts — the prompt does not reflect the diff")
	}
	if !strings.Contains(promptA, "-"+collA) || strings.Contains(promptA, collB) {
		t.Fatalf("prompt A did not carry ONLY removal A's diff:\n%s", promptA)
	}
	if !strings.Contains(promptB, "-"+collB) || strings.Contains(promptB, collA) {
		t.Fatalf("prompt B did not carry ONLY removal B's diff:\n%s", promptB)
	}
}
