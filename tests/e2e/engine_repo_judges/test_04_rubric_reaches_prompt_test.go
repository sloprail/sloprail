package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// INVARIANT: the LOADED meta-rules reach the judge's prompt.
//
// The migration split the old script into a prepare.sh (which loads the guard's
// enforced meta-rules from its rules/ directory as an array under
// additionalContext.meta_rules) and a judge template (which holds the rubric frame
// and renders that array plus the file content with a {% for %} loop). A stubbed
// verdict alone cannot prove the prepare's output reached the template: the
// renderer treats a present-parent/absent-key access as empty, so a template that
// iterates additionalContext.meta_rules renders its frame fine whether prepare
// produced the array or not (internal/dispatch, TestTemplate_UndefinedIsEmptyAndFalsy).
// A test that only flipped the verdict would pass against an engine that never ran
// prepare and never rendered the meta-rules in.
//
// So these capture the rendered prompt and assert two things a rubric that was
// really assembled must carry: the ENFORCED meta-rule's own text (proving prepare
// read rules/, emitted the array, and the template's {% for %} rendered it), AND
// the file's own content (proving event.newContent reached the same prompt). Both
// present means the whole prepare -> template wiring is live — the exact thing the
// judge-check migration and the rules-as-array restructure could have broken.
//
// The assertion targets a phrase from the shipped enforced meta-rule BODY rather
// than restating it, so it tracks whatever the guard actually enforces. A passing
// verdict is stubbed, so the point is the PROMPT, not the block.

// distinctivePhraseHighSignal is a phrase from the BODY of the high-signal
// meta-rule — the enforced one both guards ship ("A rule/skill is the shortest
// text that still carries its meaning") — and it appears nowhere in the rubric
// frame (now in the template) or the test's own file content, so finding it in the
// prompt can only mean the enforced meta-rule's body was rendered from the
// meta_rules array. The shared tail is what both guards' high-signal rules have in
// common, so one const covers both.
const distinctivePhraseHighSignal = "shortest text that still carries its meaning"

// TestRubricReachesRuleJudgePrompt: for rule-quality, the assembled rubric (the
// enforced meta-rule) AND the file content both reach the judge's prompt.
func TestRubricReachesRuleJudgePrompt(t *testing.T) {
	e := New(t)
	proj := guardProject(t, e, "rule-quality")
	e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt", `{"pass": true, "reasoning": ""}`)

	const marker = "ZZ_RULE_BODY_MARKER a distinctive line in the rule body"
	e.Run(proj, "s-erj-wire-rule", "write a rule", Turns("done",
		harness.Write("w1", "guardrails/x/rules/y/RULE.md", "# A rule\n\n"+marker+"\n"),
	))

	prompt := e.JudgePrompt(proj, "judge-prompt.txt")
	if prompt == "" {
		t.Fatalf("the rule-quality judge never ran — no prompt captured (did prepare pass and the judge run?)")
	}
	// The file content: present only if event.newContent reached the template.
	if !strings.Contains(prompt, marker) {
		t.Fatalf("the rule's own content did not reach the judge prompt — event.newContent wiring is broken:\n%s", prompt)
	}
	// The assembled rubric: present only if prepare read rules/ AND the template
	// rendered additionalContext.meta_rules. The enforced meta-rule's own phrase is
	// the proof its body was rendered from the array, not just the frame.
	if !strings.Contains(prompt, distinctivePhraseHighSignal) {
		t.Fatalf("the assembled rubric (the enforced meta-rule) did not reach the judge prompt — prepare/template wiring is broken:\n%s", prompt)
	}
}

// TestRubricReachesSkillJudgePrompt: the same for skill-quality.
func TestRubricReachesSkillJudgePrompt(t *testing.T) {
	e := New(t)
	proj := guardProject(t, e, "skill-quality")
	e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt", `{"pass": true, "reasoning": ""}`)

	const marker = "ZZ_SKILL_BODY_MARKER a distinctive line in the skill body"
	e.Run(proj, "s-erj-wire-skill", "write a skill", Turns("done",
		harness.Write("w1", "skills/x/SKILL.md", "# A skill\n\n"+marker+"\n"),
	))

	prompt := e.JudgePrompt(proj, "judge-prompt.txt")
	if prompt == "" {
		t.Fatalf("the skill-quality judge never ran — no prompt captured (did prepare pass and the judge run?)")
	}
	if !strings.Contains(prompt, marker) {
		t.Fatalf("the skill's own content did not reach the judge prompt — event.newContent wiring is broken:\n%s", prompt)
	}
	if !strings.Contains(prompt, distinctivePhraseHighSignal) {
		t.Fatalf("the assembled rubric (the enforced meta-rule) did not reach the judge prompt — prepare/template wiring is broken:\n%s", prompt)
	}
}

// TestJudgeConfigReachesTheHarness pins that the migrated guardrail's own judge
// config — the haiku model PIN and the allowed_tools: [Read] — reaches the real
// sr-agent -> claude invocation. sr-agent builds `claude -p --model <resolved>
// --settings <isolation> --add-dir <workspace> <answer dir> --allowed-tools
// Edit(//<answer dir>/**) Read --disallowed-tools Edit(//<workspace>/**) --
// <prompt>`, so the recorded argv carries the pinned model and the merged grant.
// This is the D.1/D.2 config threaded end to end through the real binaries, not
// just the unit-level judgeCommand.
func TestJudgeConfigReachesTheHarness(t *testing.T) {
	e := New(t)
	proj := guardProject(t, e, "rule-quality")

	// A recording shim: writes a passing verdict AND records the claude argv.
	argvFile := filepath.Join(proj, "claude-argv.txt")
	e.InstallJudgeClaudeRecordingArgv(argvFile, `{"pass": true, "reasoning": ""}`)

	e.Run(proj, "s-erj-config", "write a rule", Turns("done",
		harness.Write("w1", "guardrails/x/rules/y/RULE.md", "# A rule\n\nA clean body.\n"),
	))

	argv, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("the recording shim captured no claude argv (was the judge invoked?): %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(argv)), "\n")

	// The guardrail's model is size-md — the engine's default judge modelset. sr-agent
	// RESOLVES that size alias to a concrete harness model before invoking claude, so
	// the argv carries the resolved model, not the alias: for the Claude Code harness,
	// size-md resolves to `sonnet`.
	if !hasAdjacent(lines, "--model", "sonnet") {
		t.Errorf("the guardrail's model (size-md) did not resolve to `--model sonnet` at the harness; argv:\n%s", string(argv))
	}
	// allowed_tools: [Read] merged into the same --allowed-tools flag as the
	// answer-file grant, which is an Edit rule scoped to the answer directory —
	// never an unscoped Write (measured to write anywhere on disk).
	allowed := flagValues(lines, "--allowed-tools")
	if !contains(allowed, "Read") {
		t.Errorf("the guardrail's allowed_tools ([Read]) did not reach the harness's --allowed-tools; argv:\n%s", string(argv))
	}
	if contains(allowed, "Write") {
		t.Errorf("the judge was granted an unscoped Write, which writes anywhere on disk; argv:\n%s", string(argv))
	}
	if len(allowed) == 0 || !strings.HasPrefix(allowed[0], "Edit(//") {
		t.Errorf("the answer file was not granted by a scoped Edit rule first in --allowed-tools; argv:\n%s", string(argv))
	}
	// The isolation --settings the old script hand-rolled is now sr-agent's baseArgs.
	if !hasAdjacent(lines, "--settings", `{"hooks":{},"mcpServers":{},"enabledPlugins":{}}`) {
		t.Errorf("the isolation --settings did not reach the harness; argv:\n%s", string(argv))
	}
}

// TestJudgeReadsTheWorkspaceButCannotWriteIt pins the judge's project access at
// the harness: the workspace is a working directory of the judge's agent (so its
// Read/Grep/Glob reach the project — before this, a judge started in the rule's
// folder was denied every read of the project it judged), and every file-writing
// tool is denied there (a deny rule beats any allow), while the only write grant
// is the answer directory. The engine also tells the judge where the project is.
func TestJudgeReadsTheWorkspaceButCannotWriteIt(t *testing.T) {
	e := New(t)
	proj := guardProject(t, e, "rule-quality")

	argvFile := filepath.Join(t.TempDir(), "claude-argv.txt")
	e.InstallJudgeClaudeRecordingArgv(argvFile, `{"pass": true, "reasoning": ""}`)

	e.Run(proj, "s-erj-workspace", "write a rule", Turns("done",
		harness.Write("w1", "guardrails/x/rules/y/RULE.md", "# A rule\n\nA clean body.\n"),
	))

	argv, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("the recording shim captured no claude argv (was the judge invoked?): %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(argv)), "\n")
	ws := resolved(t, proj)

	// Readable: the workspace is one of the --add-dir working directories.
	dirs := flagValues(lines, "--add-dir")
	// The engine passes the workspace as `--add-dir:readonly`; sr-agent adds the
	// answer folder after it as one more (writable) dir, through the same path.
	if len(dirs) != 2 || resolved(t, dirs[0]) != ws {
		t.Fatalf("--add-dir must carry the workspace %s then the answer dir; got %q; argv:\n%s", ws, dirs, string(argv))
	}
	answerDir := dirs[1]

	// Not writable: every spelling of the workspace is denied to the Edit family.
	deny := flagValues(lines, "--disallowed-tools")
	if !contains(deny, "Edit(/"+ws+"/**)") {
		t.Errorf("the workspace %s is not denied to file-writing tools; --disallowed-tools %q; argv:\n%s", ws, deny, string(argv))
	}

	// The grant is EXACTLY the answer directory's Edit rule (one per spelling —
	// the answer dir may sit under a symlinked temp root) plus the rule's own
	// allowed_tools ([Read]). Anything else — an Edit allow on the workspace, an
	// unscoped Write — fails here. The answer dir itself is gone (sr-agent
	// removed it), so its resolved spelling is built from its parent's.
	wantAllowed := []string{"Edit(/" + answerDir + "/**)"}
	if parent := resolved(t, filepath.Dir(answerDir)); parent != filepath.Dir(answerDir) {
		wantAllowed = append(wantAllowed, "Edit(/"+filepath.Join(parent, filepath.Base(answerDir))+"/**)")
	}
	wantAllowed = append(wantAllowed, "Read")
	if got := flagValues(lines, "--allowed-tools"); strings.Join(got, "\n") != strings.Join(wantAllowed, "\n") {
		t.Errorf("--allowed-tools must be exactly the answer dir's Edit rule(s) and Read;\n got %q\nwant %q\nargv:\n%s", got, wantAllowed, string(argv))
	}
	// And the answer dir is outside the readonly workspace, or its deny would
	// block the verdict (sr-agent places it outside every readonly dir).
	if strings.HasPrefix(resolved(t, filepath.Dir(answerDir))+string(filepath.Separator), ws+string(filepath.Separator)) {
		t.Errorf("the answer dir %s lies inside the workspace %s", answerDir, ws)
	}

	// And the judge is told where the project is, so it need not guess the root.
	if !strings.Contains(string(argv), "The project being judged is at ") {
		t.Errorf("the judge's prompt does not name the workspace; argv:\n%s", string(argv))
	}
}

// flagValues is every value a variadic flag carried: the argv lines after flag
// up to the next flag (or the `--` that ends them).
func flagValues(lines []string, flag string) []string {
	var values []string
	for i := 0; i < len(lines); i++ {
		if lines[i] != flag {
			continue
		}
		for _, v := range lines[i+1:] {
			if strings.HasPrefix(v, "--") {
				break
			}
			values = append(values, v)
		}
	}
	return values
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

// resolved is path with its symlinks resolved (macOS's /var is /private/var), so
// the workspace compares equal however the engine and sr-agent spelled it.
func resolved(t *testing.T, path string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolve %s: %v", path, err)
	}
	return r
}

// hasAdjacent reports whether flag is immediately followed by value in the argv
// lines — how sr-agent passes a flag and its value as two consecutive arguments.
func hasAdjacent(lines []string, flag, value string) bool {
	for i := 0; i+1 < len(lines); i++ {
		if lines[i] == flag && lines[i+1] == value {
			return true
		}
	}
	return false
}

// TestEmptyRulesIsRefusalNotFailOpen pins the PRESERVED asymmetry: a guard whose
// rules/ has no enforced meta-rule must REFUSE (not permit), even though every
// other machinery failure's old fail-open became a refusal too. This installs the
// guard, then strips its rules/ down to nothing enforced, and asserts the write is
// refused with prepare's own "no standard to judge" message — the one fail-CLOSED
// that was always fail-closed, unchanged by the migration.
func TestEmptyRulesIsRefusalNotFailOpen(t *testing.T) {
	e := New(t)
	proj := guardProject(t, e, "rule-quality")
	// A passing verdict, so if the guard wrongly reached the judge it would ADMIT —
	// the test would then fail, which is what makes the refusal meaningful.
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	// Neuter the one enforced meta-rule by flipping its flag, so rules/ has zero
	// enforced rules — the empty-standard case prepare refuses on.
	e.WriteFile(proj, ".sloprail/file-guard/rule-quality/rules/high-signal/RULE.md",
		"---\nenforced: false\n---\n# high-signal\n\nno longer enforced\n")
	e.Git(proj, "add", "-A")
	e.Git(proj, "commit", "-m", "disable the only enforced meta-rule")

	e.Run(proj, "s-erj-emptyrules", "write a rule", Turns("done",
		harness.Write("w1", "guardrails/x/rules/y/RULE.md", "# A rule\n\nA body.\n"),
	))

	// The file-guard's refusal blocks the turn at Stop: prepare refuses with its
	// "no standard to judge" message rather than permitting — the asymmetry.
	if !sawRefusal(e.BlockingErrors(proj, "s-erj-emptyrules"), "no standard to judge") {
		t.Fatalf("an empty (no-enforced-rule) rules/ did not refuse — the empty-rules asymmetry was lost:\n%v", e.BlockingErrors(proj, "s-erj-emptyrules"))
	}
}
