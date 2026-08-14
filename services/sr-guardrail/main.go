// Command sr-guardrail answers questions about the guardrail declarations
// themselves, as opposed to any session that runs against them.
//
// It is its own STANDALONE binary, not a subcommand of `sr-session`, following
// the shape every high-level sloprail command has: one binary per command, with
// a root `sr` that proxies to them. `sr-mark` and `sr-file` are the same
// pattern.
//
//	sr-guardrail help    the event kinds this build can produce
//
// WHY NOT UNDER sr-session. This is the one command in the old single binary
// whose home was not obvious, so the reasoning is worth keeping. `guardrail
// help` belongs to no session: it takes no payload on stdin, reads no state
// store, resolves no session identity, and its answer is the same before any
// session has ever run. It is typed by an authoring agent deciding what to bind
// a rule to. Filing it under the session binary would say that the event
// vocabulary is a property of a running session, when it is a property of the
// BUILD — the modules compiled into it. That is the same argument a10n's own
// services/mark/main.go makes for standing apart from a10n-spec: markers are a
// general annotation concept, not a spec-specific one, so they are not a
// subcommand of the thing that happens to use them most.
//
// The split is also what makes the dependency honest. An author asking which
// kinds exist should not be running a binary that links the session state store
// to answer, and after the split it no longer does — this binary needs only the
// module registry.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	if err := newRoot().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "sr-guardrail",
		Short: "The declarations a project holds its agents to",
		Long: `The declarations a project holds its agents to.

A project declares guardrails under .sloprail/guardrails/. This command
describes what they may be written against — the event kinds this build can
produce, and the fields each carries.

  sr-guardrail help    the event kinds this build can produce

It asks nothing of a running session, so it can be typed at any time, in any
project, including one that has no guardrails yet.

To write a guardrail, use the authoring-guardrails skill.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newGuardrailHelpCmd())
	return root
}
