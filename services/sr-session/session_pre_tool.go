package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/harness"
	"github.com/sloprail/sloprail/internal/module/modules"
)

// newSessionPreToolCmd is the hook point that fires before a tool call runs.
//
// It is the only one that can refuse an action before it happens, which makes
// it where rules belong whose value is preventing work rather than auditing it.
func newSessionPreToolCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pre-tool",
		Short: "Before a tool call: run the guardrails bound to what it would do",
		Args:  cobra.NoArgs,
		RunE:  runSessionPreTool,
	}
}

// runSessionPreTool dispatches the new-format nature rules for a pending tool
// call and refuses it on a block.
//
// The nature dispatch reads the project's own `.sloprail/gate/*`,
// `.sloprail/file-guard/*` and `.sloprail/file-guard/structure.yaml` (plus those
// shipped by the plugins the project has enabled), runs the gates and preventive
// file-guards whose trigger matches a fired pre-event, and blocks the tool call on
// a refusal via deny(). A permit falls through and the action proceeds.
//
// It never refuses because a declaration is broken: newNatureDeclarations reports
// an unloadable declaration on stderr and dispatches nothing for it, so a malformed
// rule blocks nothing while still being named where its author looks. See
// nature_dispatch.go.
func runSessionPreTool(cmd *cobra.Command, _ []string) error {
	p := readPayload(cmd)

	reg, err := modules.Registry()
	if err != nil {
		return nil
	}

	// A sub-agent's citation of "the user" that cannot resolve is refused
	// first, whatever the rules: sr-file and cite, run by a sub-agent, cannot
	// tell it why. See subagent_cite_check.go.
	if reason := subagentUserCitationRefusal(p); reason != "" {
		return deny(cmd, reason)
	}
	if reason := natureDispatchPreTool(cmd, p, reg); reason != "" {
		return deny(cmd, reason)
	}
	return nil
}

// reportUnresolved names every enabled plugin whose files could not be found, on
// stderr, so a moved cache layout or a bumped manifest schema cannot silently
// disable a plugin's shipped rules. Called from the nature dispatch at every hook
// point that resolves the plugin set — see harness.Unresolved.
func reportUnresolved(cmd *cobra.Command, unresolved []harness.Unresolved) {
	for _, u := range unresolved {
		fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: %s\n", u.Message())
	}
}
