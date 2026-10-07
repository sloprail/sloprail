package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/checkrun"
	"github.com/sloprail/sloprail/internal/harness"
	"github.com/sloprail/sloprail/internal/module/modules"
	"github.com/sloprail/sloprail/internal/sessionstate"
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
// shipped by the plugins the project has enabled), runs the gates whose
// trigger matches a fired pre-event (file-guards act only at Stop), and blocks the tool call on
// a refusal via deny(). A permit falls through and the action proceeds.
//
// It never refuses because a declaration is broken: newNatureDeclarations reports
// an unloadable declaration on stderr and dispatches nothing for it, so a malformed
// rule blocks nothing while still being named where its author looks. See
// nature_dispatch.go.
//
// Before any of that, the session's starting point is taken if it has none — see
// ensureBaselineRecorded for why this, and not session start, is where a fresh
// session's point is first recorded. It comes ahead of the registry so that a
// build whose modules fail to load still records where the session began: the
// difference Stop measures does not depend on this tool call's rules.
// sr:invariant gates/broken-declaration-denies-nothing
func runSessionPreTool(cmd *cobra.Command, _ []string) error {
	p := readPayload(cmd)
	if skipWithoutTranscript(cmd, p, false) {
		return nil
	}
	touchAgent(cmd, p) // a sub-agent's own call: it is alive (the registry's last_seen_at)

	store := natureStore(cmd, p)
	if store != nil {
		defer store.Close()
		recordBaselineBeforeTool(cmd, store, p)
	} else {
		// No store for this agent yet (its record is not written): the folder it began
		// in is still registered, in the root's store.
		registerStartFolderReporting(cmd, nil, p)
	}

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
	// sr:invariant gates/refusal-stops-the-action
	if reason := natureDispatchPreTool(cmd, p, reg, store); reason != "" {
		return deny(cmd, reason)
	}
	return nil
}

// recordBaselineBeforeTool takes the session's starting point on its first tool
// call, and reports rather than refuses when it cannot.
//
// The engine's own bookkeeping failing is not a project's rule being violated,
// so it never becomes a denial: the tool call goes ahead, the next one asks
// again, and Stop asks once more before it measures anything.
func recordBaselineBeforeTool(cmd *cobra.Command, store sessionstate.Store, p HookPayload) {
	defer registerStartFolderReporting(cmd, store, p)
	if _, err := ensureBaselineRecorded(store, p.Cwd); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: no baseline recorded:", err)
		// The start could not be recorded before this tool call, which may commit: left
		// to the first Stop it would become that commit, and the agent's own commits
		// would never be judged. Fail closed: with no start kept, a file-guard's range
		// starts at the empty tree.
		if _, had, e := store.Meta(sessionstate.MetaSessionStart); e == nil && !had {
			_ = store.SetMeta(sessionstate.MetaSessionStart, sessionstate.SessionStartUnborn)
		}
	}
}

// reportUnresolved names every enabled plugin whose files could not be found, on
// stderr, so a moved cache layout or a bumped manifest schema cannot silently
// disable a plugin's shipped rules. Called from the nature dispatch at every hook
// point that resolves the plugin set — see harness.Unresolved.
func reportUnresolved(cmd *cobra.Command, unresolved []harness.Unresolved) {
	checkrun.ReportUnresolved(cmd.ErrOrStderr(), unresolved)
}

// registerStartFolderReporting registers the folder this agent began in (see
// registerStartFolder) and reports a failure on stderr without refusing the call.
func registerStartFolderReporting(cmd *cobra.Command, store sessionstate.Store, p HookPayload) {
	if err := registerStartFolder(store, p); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: session folder not registered:", err)
	}
}
