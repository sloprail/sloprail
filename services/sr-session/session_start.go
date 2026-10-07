package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/module/modules"
)

// newSessionStartCmd is the hook point that fires when a session begins.
//
// It records where this session measures from — the baseline commit and branch —
// where it can (a fresh session has no record to key it by yet, and its first
// tool call records it instead; see recordNotYetWritten), and loads the
// project's declarations once so a malformed one surfaces while the
// person is still watching. It never refuses: a session that cannot start because
// of a guardrail is worse than a session with none.
//
// The load report is the new-format vocabulary oracle the authoring skill sends
// authors to: `sr-session start` reports a declaration that binds a kind this
// build does not have (naming the kinds it does), a match naming a field the kind
// does not carry (naming the fields it does), a duplicate key, and a check that
// names neither a script nor a judge. newNatureDeclarations produces exactly that
// report (reportNatureInvalid / reportNatureShadowed / reportUnresolved) as a side
// effect of loading — the same report every pre-tool and Stop dispatch prints, so
// a fault an author reads here is recognisably the one they meet at a write.
func newSessionStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Session start: record the baseline and report the project's declarations",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := readPayload(cmd)
			if skipWithoutTranscript(cmd, p, true) {
				return nil
			}

			// Where this session measures from. Recorded before the declarations
			// are touched: a malformed declaration is a reason to print something,
			// never a reason for the session to have no point to diff against.
			recordBaseline(cmd, p)

			// A session running under a fallback identity is told so here, the
			// one hook whose output is seen, once per harness session. See
			// noteDegradedIdentity.
			if !isLoadCheck(p) && !recordNotYetWritten(p) {
				if id, err := stableIdentity(p); err == nil {
					noteDegradedIdentity(cmd.OutOrStdout(), cmd.ErrOrStderr(), p, id)
				}
			}

			reg, err := modules.Registry()
			if err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "sloprail:", err)
				return nil
			}

			// Load and report. newNatureDeclarations reports every declaration that
			// could not load, every shadowed one, every overlapping pair of plugin
			// structure scopes, and every unresolved plugin — the load check an
			// author runs, and the one place a person is reliably watching. Session
			// start enforces nothing, by design; the loaded set is used only to say
			// which part of the tree each plugin's structure gate owns.
			loaded := newNatureDeclarations(cmd, p.Cwd, reg)
			reportStructureScopes(cmd, loaded)
			if isLoadCheck(p) {
				reportLoadCheck(cmd, loaded)
			}
			return nil
		},
	}
}

// recordBaseline records the commit this session measures from and the branch
// it belongs to.
//
// Every failure is reported and swallowed. A session that cannot start because
// of a guardrail is worse than a session with none, and that applies with more
// force to the engine's own bookkeeping than to any rule a project wrote: a
// missing baseline costs the difference this session would have measured, while
// a refusal here costs the session itself. The first tool call asks again, and
// takes the point then — which, for a fresh session, is where it is actually
// taken: see recordNotYetWritten.
//
// Still attempted here because it is not always a fresh session. A resumed one
// reports a record that already exists, and taking the point (or noticing the
// tree left it while the session was closed) before the first prompt is
// strictly earlier than the first tool call.
func recordBaseline(cmd *cobra.Command, p HookPayload) {
	// No payload at all is the documented load check (`sr-session start <
	// /dev/null`, which install.sh and the install docs tell a newcomer to
	// run), not a session: there is nothing to record a baseline for, and
	// reporting that as a failure makes a working install look broken.
	if isLoadCheck(p) {
		return
	}
	// A fresh session's record does not exist yet, and that is the harness
	// working as designed rather than a fault worth a line of stderr. Claude
	// Code writes the transcript after SessionStart's hooks have run — in a
	// fresh session the record's origin is the attachment for this very hook —
	// so the identity the store is keyed by cannot be read here. The session's
	// first tool call takes the point instead, before anything the agent does
	// can have moved HEAD (ensureBaselineRecorded, which says why that is early
	// enough).
	//
	// Only the record's OWN absence is quiet. A record that exists but cannot
	// be resolved — a continuation whose earlier transcript is missing, a chain
	// that loops — is a broken identity, and still reported below.
	if recordNotYetWritten(p) {
		return
	}
	store, err := openEngineState(p)
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: no baseline recorded:", err)
		return
	}
	defer store.Close()

	if outcome, err := ensureBaseline(store, p.Cwd); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail: no baseline recorded:", err)
	} else if outcome == baselineMoved {
		// Between turns: every cited-change point so far is on the line the
		// tree left. See pruneHistory.
		if err := pruneHistory(store, nowNano()); err != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), "sloprail:", err)
		}
	}
}

// recordNotYetWritten reports whether the payload names this session's record
// and that record does not exist yet — the ordinary state of a fresh session at
// SessionStart.
//
// Asked of the path itself rather than read off an error from the identity
// walk, because the walk opens more than one file: a continuation reads the
// transcript it continues, and THAT one missing is a broken identity, not an
// early hook. Only this session's own record being absent is the expected case.
//
// A path that cannot be resolved at all answers false, so the attempt goes
// ahead and reports why — this only ever silences the one case that is known
// to be harmless.
func recordNotYetWritten(p HookPayload) bool {
	path, err := recordOf(p)
	if err != nil || path == "" {
		return false
	}
	_, err = os.Stat(path)
	return errors.Is(err, fs.ErrNotExist)
}

// reportStructureScopes says, once per session, which part of the tree each
// plugin's structure gate owns — so a person learns at the start that writes
// under `.mdmap/` answer to plugin mdmap rather than discovering it at a refusal.
// Stderr, beside the load report: SessionStart's stdout is context for the agent,
// and this is for the person.
func reportStructureScopes(cmd *cobra.Command, loaded declaration.Loaded) {
	for _, sg := range loaded.PluginStructures() {
		fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: %s owns %s\n",
			sg.Describe(), strings.Join(sg.ScopeGlobs(), ", "))
	}
}

// isLoadCheck reports whether this start carries no session at all — the
// documented load check (`sr-session start < /dev/null`) rather than a harness
// starting a session.
func isLoadCheck(p HookPayload) bool {
	return p.TranscriptPath == "" && p.AgentTranscriptPath == "" && p.SessionID == ""
}

// reportLoadCheck ends the load check with what it did and did not do. Without
// it a clean load prints nothing, and an agent that runs the load check after
// its own work reads that silence as "my work passes the guardrails" — when no
// rule ran against anything. Only for the load check: a harness's SessionStart
// has no reader for it. Stderr, beside the rest of the load report.
func reportLoadCheck(cmd *cobra.Command, loaded declaration.Loaded) {
	n := len(loaded.FileGuards) + len(loaded.Gates) + len(loaded.Contexts) + len(loaded.Structures)
	failed := ""
	if len(loaded.Invalid) > 0 {
		failed = fmt.Sprintf(", %d could not load (above)", len(loaded.Invalid))
	}
	if len(loaded.Degraded) > 0 {
		failed += fmt.Sprintf(", %d cannot run a declared script and refuse what they guard (above)", len(loaded.Degraded))
	}
	if broken := reportJudgeTemplates(cmd, loaded); broken > 0 {
		failed += fmt.Sprintf(", %d judge template(s) cannot be rendered (above)", broken)
	}
	fmt.Fprintf(cmd.ErrOrStderr(),
		"sloprail: %d rules loaded%s. This only checked that they load: no rule ran against any file or action.\n",
		n, failed)
}
