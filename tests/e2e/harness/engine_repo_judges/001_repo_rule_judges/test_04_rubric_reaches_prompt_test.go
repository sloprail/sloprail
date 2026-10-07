package e2e

import (
	"os"
	"path/filepath"
	"regexp"
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
	).ThenCommit("write the file"))

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
	).ThenCommit("write the file"))

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
// config — the size-md model and the allowed_tools: [Read] — reaches the real
// sr-agent -> harness invocation, and that the judge's agent is launched with the
// project's hooks off. Each harness spells the launch its own way (Claude Code:
// `--model`, `--allowed-tools Edit(//<answer dir>/**) Read`, an isolation
// `--settings`; Codex: `-m` and a sandbox; Cursor: `--model` and a private
// permission config), so the recorded launch is read through the driver and the
// assertions are the ones every harness can be asked: the resolved model, the grant
// of Read, nothing wider than Read and the answer directory, hooks off.
func TestJudgeConfigReachesTheHarness(t *testing.T) {
	e := New(t)
	proj := guardProject(t, e, "rule-quality")

	// A recording shim: writes a passing verdict AND records the harness launch.
	argvFile := filepath.Join(proj, "claude-argv.txt")
	e.InstallJudgeClaudeRecordingArgv(argvFile, `{"pass": true, "reasoning": ""}`)

	e.Run(proj, "s-erj-config", "write a rule", Turns("done",
		harness.Write("w1", "guardrails/x/rules/y/RULE.md", "# A rule\n\nA clean body.\n"),
	).ThenCommit("write the file"))

	argv, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("the recording shim captured no harness argv (was the judge invoked?): %v", err)
	}
	access := e.JudgeAccess(argvFile)

	// The guardrail's model is size-md — the engine's default judge modelset. sr-agent
	// RESOLVES that size alias to a concrete harness model before invoking the harness, so
	// the launch carries the resolved model, not the alias.
	if _, want := e.MediumJudgeModelArgs(); access.Model != want {
		t.Errorf("the guardrail's model (size-md) did not resolve to %q at the harness; got %q; argv:\n%s", want, access.Model, string(argv))
	}
	// allowed_tools: [Read] reached the harness, merged with the answer-file grant — which
	// is scoped to the answer directory, never an unscoped Write (measured to write
	// anywhere on disk) — and nothing wider.
	if !access.ReadGranted {
		t.Errorf("the guardrail's allowed_tools ([Read]) did not reach the harness; argv:\n%s", string(argv))
	}
	if len(access.OtherTools) != 0 {
		t.Errorf("the judge was granted more than Read and its answer directory: %q; argv:\n%s", access.OtherTools, string(argv))
	}
	if len(access.Writable) == 0 {
		t.Errorf("the answer directory was not granted a scoped write; argv:\n%s", string(argv))
	}
	// The isolation the old script hand-rolled is now sr-agent's: the judge's agent runs
	// without the project's hooks.
	if !e.JudgeHooksOff(string(argv), proj) {
		t.Errorf("the isolation (hooks off) did not reach the harness; argv:\n%s", string(argv))
	}
}

// projectBeingJudged is the directory the engine tells the judge the project is at.
var projectBeingJudged = regexp.MustCompile(`The project being judged is at (\S+?)\. Paths in`)

// TestJudgeReadsTheWorkspaceButCannotWriteIt pins the judge's project access at
// the harness: the project is readable (a judge started in the rule's folder was
// denied every read of the project it judged), and it cannot be written — by a deny
// that beats any allow, or by a sandbox whose only writable root is the answer
// directory — while the only write grant is the answer directory. The engine also
// tells the judge where the project is.
// sr:proves judges/judge-cannot-change-the-project
func TestJudgeReadsTheWorkspaceButCannotWriteIt(t *testing.T) {
	e := New(t)
	proj := guardProject(t, e, "rule-quality")

	argvFile := filepath.Join(t.TempDir(), "claude-argv.txt")
	e.InstallJudgeClaudeRecordingArgv(argvFile, `{"pass": true, "reasoning": ""}`)

	e.Run(proj, "s-erj-workspace", "write a rule", Turns("done",
		harness.Write("w1", "guardrails/x/rules/y/RULE.md", "# A rule\n\nA clean body.\n"),
	).ThenCommit("write the file"))

	argv, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("the recording shim captured no harness argv (was the judge invoked?): %v", err)
	}
	access := e.JudgeAccess(argvFile)
	realWs := resolved(t, proj)

	// The judge is told where the project is, so it need not guess the root. A file-guard
	// judges commits, so the project is the read-only snapshot of the committed tip
	// (.../sr-tree-*/tree), never the folder's checked-out working tree.
	m := projectBeingJudged.FindStringSubmatch(string(argv))
	if m == nil {
		t.Fatalf("the judge's prompt does not name the workspace; argv:\n%s", string(argv))
	}
	ws := m[1]
	if filepath.Base(ws) != "tree" || !strings.Contains(ws, "sr-tree-") || strings.HasPrefix(ws, realWs) {
		t.Fatalf("the judge must be pointed at the tip's tree snapshot, not the working tree; got %q (workspace %s)", ws, realWs)
	}

	// Readable: the snapshot is among the directories the judge is given, or the harness
	// reads the whole disk.
	if !access.ReadsAnywhere && !within(ws, access.Readable) {
		t.Errorf("the workspace %s is not readable by the judge; readable %q; argv:\n%s", ws, access.Readable, string(argv))
	}

	// The only write grant is the answer directory (one entry per spelling — the answer dir
	// may sit under a symlinked temp root). Anything else — a write allow on the workspace,
	// an unscoped Write — fails here. The answer dir itself is gone (sr-agent removed it),
	// so its resolved spelling is built from its parent's.
	if len(access.Writable) == 0 {
		t.Fatalf("the judge has no writable answer directory; argv:\n%s", string(argv))
	}
	answerDir := access.Writable[0]
	for _, w := range access.Writable {
		if filepath.Base(w) != filepath.Base(answerDir) || resolved(t, filepath.Dir(w)) != resolved(t, filepath.Dir(answerDir)) {
			t.Errorf("the judge may write more than its answer directory %s: %q; argv:\n%s", answerDir, access.Writable, string(argv))
		}
	}
	if len(access.OtherTools) != 0 {
		t.Errorf("the judge was granted more than Read and its answer directory: %q; argv:\n%s", access.OtherTools, string(argv))
	}

	// Not writable: the workspace is denied outright, or the harness confines writes to
	// the granted directories and the workspace is not one of them.
	denied := within(ws, access.Readonly) || within(resolvedLoose(ws), access.Readonly)
	confined := access.ConfinedToWritable && !within(ws, access.Writable) && !within(resolvedLoose(ws), access.Writable)
	if !denied && !confined {
		t.Errorf("the workspace %s is writable by the judge; readonly %q, writable %q (confined %v); argv:\n%s",
			ws, access.Readonly, access.Writable, access.ConfinedToWritable, string(argv))
	}
	// And the answer dir is outside the readonly workspace, or its deny would block the
	// verdict (sr-agent places it outside every readonly dir).
	if strings.HasPrefix(resolved(t, filepath.Dir(answerDir))+string(filepath.Separator), resolvedLoose(ws)+string(filepath.Separator)) {
		t.Errorf("the answer dir %s lies inside the workspace %s", answerDir, ws)
	}
}

// within reports whether path is one of dirs or lies under one.
func within(path string, dirs []string) bool {
	for _, d := range dirs {
		if path == d || strings.HasPrefix(path, strings.TrimSuffix(d, "/")+"/") {
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
	e.CommitAll(proj, "disable the only enforced meta-rule")

	e.Run(proj, "s-erj-emptyrules", "write a rule", Turns("done",
		harness.Write("w1", "guardrails/x/rules/y/RULE.md", "# A rule\n\nA body.\n"),
	).ThenCommit("write the file"))

	// The file-guard's refusal blocks the turn at Stop: prepare refuses with its
	// "no standard to judge" message rather than permitting — the asymmetry.
	if !sawRefusal(e.BlockingErrors(proj, "s-erj-emptyrules"), "no standard to judge") {
		t.Fatalf("an empty (no-enforced-rule) rules/ did not refuse — the empty-rules asymmetry was lost:\n%v", e.BlockingErrors(proj, "s-erj-emptyrules"))
	}
}

// resolvedLoose resolves symlinks in the longest existing prefix of p (a snapshot
// the engine already removed has no path of its own to resolve).
func resolvedLoose(p string) string {
	if r, err := filepath.EvalSymlinks(filepath.Dir(p)); err == nil {
		return filepath.Join(r, filepath.Base(p))
	}
	return p
}
