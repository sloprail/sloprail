// Command sr-eval proves that a guardrail's use-case doc is true of a real
// agent, not just of a mock transcript.
//
// The engine's own e2e suite (tests/e2e/harness) proves that a rule fires
// against a scripted, mocked `claude` — which proves the engine's wiring, not
// that a real agent, given a realistic prompt and no hints, actually runs into
// the rule and recovers the way the use-case doc says it does. sr-eval runs
// the SECOND proof: a real harness, launched through sr-agent so this stays
// harness-agnostic, against a seeded project, scored by reading what actually
// happened in its transcript.
//
// It is its own STANDALONE binary, following the same shape as every other
// high-level sloprail command: one binary per command, with a root `sr` that
// proxies to them.
//
//	sr-eval run --fixture examples/required-context-precondition/eval/require-skill-decisions
//
// A FIXTURE is a directory: fixture.yaml (what to seed, what to ask, how to
// score) beside prompt.md (the exact words the agent-under-test receives) and
// score.sh (the deterministic check of what the transcript shows). See
// fixture.go for the format.
//
// WHY THE AGENT-UNDER-TEST RUNS THROUGH sr-agent RATHER THAN A HARDCODED
// `claude` invocation: an eval binary that shells out to `claude` directly has
// smuggled the Claude-Code-specific coupling sr-agent exists to remove back in
// through its most important caller. sr-agent's own isolation
// (--settings '{"hooks":{},...}') exists so a JUDGE call cannot recurse into
// this project's own guardrails — but the agent-under-test is the opposite
// case: the whole point is for the project's real .sloprail/ gate to fire. So
// sr-eval overrides that isolation explicitly via --claude-args, rather than
// bypassing sr-agent to get hooks back. See run.go.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	err := newRoot().Execute()
	if err != nil {
		if err.Error() != "" {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(exitCode(err))
	}
}

func newRoot() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sr-eval <command>",
		Short: "Prove a guardrail's use case against a real agent",
		Long: `Prove a guardrail's use case against a real agent, not a mock.

A fixture directory names a seed, a prompt, and a scorer. sr-eval seeds an
isolated project, launches a real agent-under-test through sr-agent (so this
stays harness-agnostic), and scores what the transcript shows actually
happened — not what a mock was scripted to produce.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.AddCommand(newRunCmd())
	return cmd
}
