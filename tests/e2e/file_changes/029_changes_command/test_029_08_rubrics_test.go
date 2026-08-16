package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// rubrics: a plugin whose Stop hook judges every file changed this turn
// against the consumer's own configured rubric, using low-level commands —
// `sr-file changes --turn`, `sr-file checks`, `sr-agent` — rather than a
// GUARDRAIL an engine dispatches.
//
// The judge itself (`claude`, which sr-agent invokes by name off PATH) is
// stubbed for these tests: they are about the plugin's PLUMBING — does it find
// what changed, does it call the judge, does a refusal outlive the run that
// produced it — not about model quality. Same technique the sibling
// review3/r3_guardrails tests already use for the engine's own judged rules.

const rubricsPlugin = "sloprail-rubrics"

const rubricsConfig = `rubrics:
  - match: "**/SKILL.md"
    prompt: skill-quality.md
`

const rubricsPrompt = "Judge whether this skill is high-signal.\n"

// stopBlocked reports whether the turn was stopped rather than allowed to end.
//
// Result.Refused() does NOT cover this — its own doc comment says why: its two
// markers are both PreToolUse channels, measured against that hook point alone.
// A Stop hook that exits 2 never produces either one; the mock instead re-drives
// the agent past its own end (real Claude Code's Stop→re-prompt→continue), so a
// blocked Stop is visible as the final result appearing more than once. One
// result means the turn simply ended. Same technique
// session/024_post_blocking's tests already use for the engine's own
// Stop-driven Post rules.
func stopBlocked(r harness.Result) bool {
	return strings.Count(r.Output, `"subtype":"success"`) >= 2
}

// stubJudge writes a `claude` that reads the verdict path out of its OWN
// ARGUMENTS and writes a fixed body there.
//
// NOT stdin — that is review3/r3_guardrails' technique for A10N_CLAUDE_BIN,
// where a hook script pipes the prompt in by hand (`printf '%s' "$prompt" |
// claude ...`). sr-agent, which sloprail-rubrics calls, passes the prompt as a
// POSITIONAL ARGUMENT after `--` (see services/sr-agent/invoke.go:
// `args = append(args, "--", prompt)`), never on stdin. A stub that reads
// stdin here blocks forever on a pipe nothing writes to — measured: the first
// version of this stub hung until sr-agent's own 25s internal path timed out,
// and the plugin's fail-open swallowed the failure as "no verdict," which
// looked exactly like a judge that ran and found nothing wrong.
func stubJudge(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "claude")
	script := `#!/bin/sh
verdict="$(printf '%s\n' "$*" | tr ' ' '\n' | grep '^/tmp/.*\.json$' | tail -1)"
[ -n "$verdict" ] || exit 1
cat > "$verdict" <<'VERDICT_EOF'
` + body + `
VERDICT_EOF
exit 0
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("stubJudge: write: %v", err)
	}
	return dir
}

// putJudgeOnPath prepends the stub's directory to PATH for this test, so
// sr-agent's own PATH lookup of "claude" finds it. Must happen before e.Run —
// the harness builds the hook's PATH from os.Getenv("PATH") at call time.
func putJudgeOnPath(t *testing.T, stubDir string) {
	t.Helper()
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// T029_08: a file failing its rubric blocks the turn from ending, naming the
// file.
func TestT029_08_AFailingFileBlocksTheTurn(t *testing.T) {
	putJudgeOnPath(t, stubJudge(t, `{"has_issues": true, "reasoning": "padding"}`))

	e := New(t)
	proj := e.ProjectWith(rubricsPlugin)
	e.WriteFile(proj, ".sloprail/rubrics/config.yaml", rubricsConfig)
	e.WriteFile(proj, ".sloprail/rubrics/skill-quality.md", rubricsPrompt)
	e.GitInit(proj)

	got := e.Run(proj, "s-029-08", "write a skill", Turns("done",
		Write("w1", "skills/foo/SKILL.md", "content"),
	))

	if !stopBlocked(got) {
		t.Fatalf("a file the judge flagged did not block the turn:\n%s", got.Output)
	}
	if !strings.Contains(got.Output, "skills/foo/SKILL.md") {
		t.Errorf("the refusal does not name the failing file:\n%s", got.Output)
	}
}

// T029_09: a file passing its rubric lets the turn end normally.
func TestT029_09_APassingFileLetsTheTurnEnd(t *testing.T) {
	putJudgeOnPath(t, stubJudge(t, `{"has_issues": false, "reasoning": ""}`))

	e := New(t)
	proj := e.ProjectWith(rubricsPlugin)
	e.WriteFile(proj, ".sloprail/rubrics/config.yaml", rubricsConfig)
	e.WriteFile(proj, ".sloprail/rubrics/skill-quality.md", rubricsPrompt)
	e.GitInit(proj)

	got := e.Run(proj, "s-029-09", "write a good skill", Turns("done",
		Write("w1", "skills/foo/SKILL.md", "content"),
	))

	if stopBlocked(got) {
		t.Fatalf("a file the judge passed blocked the turn:\n%s", got.Output)
	}
}

// T029_10: THE HEADLINE. A refusal outlives the turn that produced it — the
// same session, run again with nothing fixed, is blocked again. Without
// sr-file checks recording the verdict, this is exactly the failure the whole
// plugin exists to close: the agent is told no once and the next turn starts
// clean with the file still wrong.
func TestT029_10_ARefusalOutlivesTheTurn(t *testing.T) {
	putJudgeOnPath(t, stubJudge(t, `{"has_issues": true, "reasoning": "padding"}`))

	e := New(t)
	proj := e.ProjectWith(rubricsPlugin)
	e.WriteFile(proj, ".sloprail/rubrics/config.yaml", rubricsConfig)
	e.WriteFile(proj, ".sloprail/rubrics/skill-quality.md", rubricsPrompt)
	e.GitInit(proj)

	sessionID := "s-029-10"
	first := e.Run(proj, sessionID, "write a skill", Turns("done",
		Write("w1", "skills/foo/SKILL.md", "content"),
	))
	if !stopBlocked(first) {
		t.Fatalf("the first turn was not blocked:\n%s", first.Output)
	}

	// A SECOND turn in the same session, touching nothing new. If the refusal
	// does not outlive the turn, this is not blocked — the exact
	// silent-forgetting failure the plugin exists to prevent.
	second := e.Run(proj, sessionID, "say something unrelated", Turns("done"))
	if !stopBlocked(second) {
		t.Fatalf("a second turn, with nothing fixed, was not blocked — the refusal did not "+
			"outlive the turn that produced it:\n%s", second.Output)
	}
}

// T029_11: the negative control. A file outside every configured glob is not
// judged at all — proves the refusals above are about matching the config, not
// about the plugin refusing every changed file.
func TestT029_11_AFileOutsideTheConfigIsNotJudged(t *testing.T) {
	// A judge that ALWAYS flags, so an unblocked turn here can only mean the
	// file was never sent to it.
	putJudgeOnPath(t, stubJudge(t, `{"has_issues": true, "reasoning": "padding"}`))

	e := New(t)
	proj := e.ProjectWith(rubricsPlugin)
	e.WriteFile(proj, ".sloprail/rubrics/config.yaml", rubricsConfig)
	e.WriteFile(proj, ".sloprail/rubrics/skill-quality.md", rubricsPrompt)
	e.GitInit(proj)

	got := e.Run(proj, "s-029-11", "write an unrelated file", Turns("done",
		Write("w1", "notes.md", "ordinary"),
	))

	if stopBlocked(got) {
		t.Fatalf("a file outside every configured glob blocked the turn — the judge saw a "+
			"file it should never have been shown:\n%s", got.Output)
	}
}

// T029_12: without the plugin installed, the same failing file does not block
// the turn — the group's control.
func TestT029_12_WithoutThePluginTheSameFileDoesNotBlock(t *testing.T) {
	putJudgeOnPath(t, stubJudge(t, `{"has_issues": true, "reasoning": "padding"}`))

	e := New(t)
	proj := e.Project() // base plugin only
	e.GitInit(proj)

	got := e.Run(proj, "s-029-12", "write a skill with nothing installed", Turns("done",
		Write("w1", "skills/foo/SKILL.md", "content"),
	))

	if stopBlocked(got) {
		t.Fatalf("the turn was blocked with the plugin uninstalled:\n%s", got.Output)
	}
}
