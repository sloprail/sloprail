package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The provenance guard, proved against a REAL `claude` rather than the mock.
//
// # Why this file exists at all
//
// T015_01 through T015_05 drive a10n-claude-mock through a shim named `claude`.
// That shim is the component the guard's proof actually turns on, and it is the
// one thing the mock cannot stand in for. The property under test is:
//
//	SLOPRAIL_LAUNCHED_BY, set by the engine on the OUTER hook, survives the exec
//	into a real harness, is loaded by that harness into ITS own hook environment,
//	and is read there by a second, independent invocation of the engine.
//
// Every link in that chain except the first belongs to the real binary. The
// mock fires hooks it finds in settings.json because it was written to; that a
// real `claude` also does, and that it passes its own process environment down
// into them unmodified, is an assumption about someone else's program. This
// task was closed once on the mock-only proof and reopened for exactly that.
//
// # Why it is opt-in rather than part of the suite
//
// It spends the operator's money, and it cannot be made not to: the thing being
// proved is that a real, billed agent behaves a certain way. Two facts make an
// always-on version impossible rather than merely expensive:
//
//   - harness.New deliberately overrides HOME and CLAUDE_CONFIG_DIR to isolate
//     the run from the host's claude data. A real `claude` started under that
//     environment has no credentials and cannot run at all. Authenticating it
//     would mean handing a test suite the operator's real config — the exact
//     thing that isolation exists to prevent.
//   - CI has no `claude` and no account to bill.
//
// So it is gated on SLOPRAIL_REAL_AGENT=1 and skips otherwise. A skip is honest
// here in a way it would not be inside a mocked test: the thing skipped is a
// billed side effect, not a branch of the logic.
//
// # What it costs when it does run
//
// Two haiku invocations, each capped by --max-budget-usd, on a prompt that asks
// for one small file. Measured at well under $0.01 per run. The cap is passed to
// the INNER agent through the guardrail's own script, which is where a runaway
// would spend, and the depth counter below bounds it a second way.
//
// # The measurement
//
// Two guardrails bind the same paths:
//
//	judge-notes   launches an agent — the rule that must NOT re-enter itself.
//	env-witness   launches nothing and only records the environment it sees.
//
// The witness is what makes the observation positive rather than an absence.
// Because it is a DIFFERENT rule, the guard does not silence it, so it still
// fires inside the launched agent and can report that agent's own
// SLOPRAIL_LAUNCHED_BY. Without it the pass condition would be "judge-notes did
// not run twice", which is equally satisfied by an inner session whose hooks
// never fired at all — the vacuous pass this test exists to rule out.
//
// This run was performed by hand before the file was written, and the ledger it
// produced is quoted in the task. The three lines that matter:
//
//	WITNESS pid=31797 depth=0 launched_by=[env-witness]
//	depth=0 guardrail=judge-notes launched_by=[judge-notes]
//	WITNESS pid=32435 depth=1 launched_by=[judge-notes:env-witness]
//
// The third line is the whole proof: a hook inside the real launched agent,
// reporting the outer rule's name inherited across the exec with its own
// appended. judge-notes has no depth=1 line beside it — it declined.
//
// The same experiment run against a binary with the guard mutated to `false &&`
// recursed to the depth-2 kill switch, which is what shows the measurement
// discriminates rather than passing on a technicality.
const realJudgeDecl = `---
hooks:
  PreFileCreate:
    - matcher: path startsWith "notes/"
      hooks:
        - type: command
          command: ./judge.sh
---

# Asks a real agent whether the note is any good

Launches sr-agent against the operator's actual claude.
`

const realWitnessDecl = `---
hooks:
  PreFileCreate:
    - matcher: path startsWith "notes/"
      hooks:
        - type: command
          command: ./witness.sh
---

# Records the provenance environment of every process it fires in

Launches nothing. Exists only to observe SLOPRAIL_LAUNCHED_BY, including
inside a session some other rule launched.
`

// realJudgeScript launches a real agent, bounded three ways.
//
// The `cd "$SR_WORKSPACE"` is not tidiness and removing it invalidates the test.
// A hook runs with its working directory set to the GUARDRAIL'S OWN FOLDER, so
// an agent launched without it starts there — outside the project, where the
// plugin is not installed and no hook fires. Measured: the inner agent wrote its
// file into .sloprail/guardrails/judge-notes/notes/ and the witness never ran,
// which reads exactly like a guard that works. The guarded tree is where a real
// judging agent works, and it is the only place the recursion exists.
//
// The depth counter is the kill switch. With the guard in place it is never
// reached; with the guard removed it is the only thing that ends the run, which
// is why the number is 2 and not 8.
const realJudgeScript = `#!/bin/sh
cat >/dev/null

LEDGER="$SLOP_EXP_LEDGER"
D="${SLOP_TEST_DEPTH:-0}"

printf 'depth=%s guardrail=%s launched_by=[%s]\n' \
  "$D" "$SR_GUARDRAIL" "$SLOPRAIL_LAUNCHED_BY" >> "$LEDGER"

if [ "$D" -ge 2 ]; then
  echo "KILL-SWITCH-TRIPPED at depth $D" >> "$LEDGER"
  exit 0
fi

SLOP_TEST_DEPTH=$((D + 1))
export SLOP_TEST_DEPTH

cd "$SR_WORKSPACE" || exit 0

sr-agent --harness claude-code --model size-xs \
  --claude-args '{"max-budget-usd":"0.05","allowed-tools":"Write"}' \
  "Create a file at notes/judged-$D.md containing exactly the word ok. Then stop." \
  >> "$LEDGER.agent" 2>&1
exit 0
`

// realWitnessScript records the provenance variable and nothing else.
const realWitnessScript = `#!/bin/sh
cat >/dev/null
printf 'WITNESS depth=%s launched_by=[%s]\n' \
  "${SLOP_TEST_DEPTH:-0}" "$SLOPRAIL_LAUNCHED_BY" >> "$SLOP_EXP_LEDGER"
exit 0
`

// T015_06: against a real `claude`, the launched agent's OWN hooks see the rule
// that launched it, and that rule declines to enforce there.
//
// Skipped unless SLOPRAIL_REAL_AGENT=1, because it bills the operator.
func TestT015_06_RealAgentInheritsProvenanceAndTheRuleDeclines(t *testing.T) {
	if os.Getenv("SLOPRAIL_REAL_AGENT") != "1" {
		t.Skip("real-agent test: set SLOPRAIL_REAL_AGENT=1 to run it (spends money on a real claude)")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("real-agent test: no `claude` on PATH")
	}

	e := New(t)
	proj := e.Project()
	// A REAL `claude` loads project-scope plugins only inside a repository.
	// Measured: without this the session runs, writes the file, and fires no
	// hook at all — a green run that proves nothing. The mock has no such
	// condition, which is why no other test in this package needs the call.
	e.GitInit(proj)
	e.Guardrail(proj, "judge-notes", realJudgeDecl, map[string]string{"judge.sh": realJudgeScript})
	e.Guardrail(proj, "env-witness", realWitnessDecl, map[string]string{"witness.sh": realWitnessScript})

	// Deliberately NO InstallClaudeShim: the whole point is the real binary.
	ledger := filepath.Join(proj, "ledger.txt")
	t.Setenv("SLOP_EXP_LEDGER", ledger)

	e.RunReal(proj, "Create a file at notes/first.md containing exactly the word hello. Then stop.")

	lines := readLines(t, ledger)
	t.Logf("ledger:\n%s", strings.Join(lines, "\n"))

	for _, l := range lines {
		if strings.Contains(l, "KILL-SWITCH-TRIPPED") {
			t.Fatalf("the recursion ran away and was stopped only by the test's own counter; ledger:\n%s",
				strings.Join(lines, "\n"))
		}
	}

	// The launched agent's own hooks must have fired at all. Without this the
	// test passes vacuously whenever the inner session has no hooks — which is
	// the failure mode that made the first closure wrong.
	var innerWitness string
	for _, l := range lines {
		if strings.HasPrefix(l, "WITNESS depth=1") {
			innerWitness = l
		}
	}
	if innerWitness == "" {
		t.Fatalf("no hook fired inside the launched agent, so the guard was never consulted "+
			"and this proves nothing; ledger:\n%s", strings.Join(lines, "\n"))
	}

	// The observation this test exists for: the inner process's OWN hook
	// environment names the rule that launched it.
	if !strings.Contains(innerWitness, "judge-notes") {
		t.Fatalf("the launched agent's hook did not inherit the launching rule in %s: %q",
			"SLOPRAIL_LAUNCHED_BY", innerWitness)
	}

	// And the rule that launched it did not run there.
	for _, l := range lines {
		if strings.HasPrefix(l, "depth=1 guardrail=judge-notes") {
			t.Fatalf("the rule that launched the agent re-entered itself inside it: %q", l)
		}
	}
}

// readLines reads a ledger written by a hook, skipping blanks.
func readLines(t *testing.T, path string) []string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read ledger %s: %v", path, err)
	}
	var out []string
	for _, l := range strings.Split(string(body), "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}
