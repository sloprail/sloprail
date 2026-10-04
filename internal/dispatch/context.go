package dispatch

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/natures"
	"github.com/sloprail/sloprail/internal/scriptexec"
)

// This file is the CONTEXT-LIFECYCLE half of the runner's job: running a
// context's `enter` and `exit` scripts (dot-dir-file-store/main.tsp
// ContextDeclaration). It sits beside checks.go because a context's enter/exit
// are the same kind of executable a check is — a script run directly (no shell)
// from the context's own folder, the payload on stdin, fail-closed if it will not run —
// and reusing the same runShell substrate is what keeps a lifecycle script and a
// check behaving identically when the machinery breaks.
//
// # Why here rather than in services/sr-session
//
// The dispatch that MATCHES a context's `on` trigger to a fired event, and that
// PERSISTS the context[] map, is the caller's (services/sr-session), exactly as
// gate matching is. What is here is only the two things that must live where the
// exec substrate does: assembling the ContextEnterPayload/ContextExitPayload the
// spec names, and running the script fail-closed. The caller supplies the
// matched context and the fired event; this returns enter's replacement payload
// (or exit's verdict) without knowing how the map is stored.
//
// # The reversal (spec slice 7), respected here
//
// A context's `exit` does NOT refuse a Stop — it only reports whether the
// context is still active. So ExitContext returns a Verdict whose Refused means
// "this context considers itself DONE" (mark it inactive), NOT "block the Stop".
// The caller reads it to flip active/inactive and never turns it into a block.
// A gate bound to Stop is what blocks; that goes through Runner.Run like any
// gate. Naming the field Refused keeps one Verdict type across the package; its
// MEANING for exit is documented on ExitContext so a caller cannot mistake it
// for a gate's refusal.

// ContextEnterRequest is everything EnterContext needs to run one context's
// `enter` on one fired trigger.
//
// It carries the same collaborators a check does — the fired event, the
// transcript path, the gates[] map (enter may gate its own activation on a
// gate's verdict), and the context's own folder — plus CurrentContext, this
// context's own last {active, payload}, which enter needs because it runs on
// EVERY trigger (active or not) and its stdout REPLACES the payload rather than
// merging: a script that wants to grow a list reads currentContext.payload and
// returns the grown version itself (spec ContextDeclaration.enter).
type ContextEnterRequest struct {
	// Enter is the enter script path, relative to Dir.
	Enter string

	// Event is the fired `on` trigger — a ContextEventKind (a Post variant is
	// legal, unlike a gate). Carried into the payload flat, as `event`.
	Event event.Event

	// TranscriptPath names the session record enter reads to recognise "now it's
	// the time" and extract the payload. May be "".
	TranscriptPath string

	// Gates is every declared gate's most recent verdict, by name — what enter
	// reads as `gates[<name>].status` to gate its own activation.
	Gates map[string]natures.GateState

	// CurrentContext is this context's own last {active, payload}, so enter sees
	// what was there before deciding whether to grow, replace, or leave it.
	CurrentContext natures.ContextState

	// Dir is the context's own folder — enter resolves relative to it, and its
	// scripts find their siblings there.
	Dir string

	// Name is the context's name, for the script's SR_GUARDRAIL and for
	// diagnostics.
	Name string

	// Workspace and SessionID are the session facts enter's environment carries —
	// SR_WORKSPACE (to resolve a workspace-relative path and key its own state) and
	// SR_SESSION_ID — the same as a check's. Empty leaves them unset.
	Workspace string
	SessionID string

	// LaunchedBy is the re-entry provenance emitted as SLOPRAIL_LAUNCHED_BY, so a
	// context whose enter spawns sr-agent does not re-enter itself on the launched
	// agent's writes. The caller computes it (appendLaunchedBy) and threads it
	// through; the runner forwards it onto the scriptCall. See scriptCall.LaunchedBy.
	LaunchedBy string
}

// ContextExitRequest is everything ExitContext needs to run one context's `exit`
// on a Stop.
//
// The Stop counterpart of ContextEnterRequest. `exit` has no `on` of its own —
// it is always consulted on the Stop while the context is active — so Event is
// always the Stop the caller passes. It reads the same gates[] map (to read a
// paired gate's verdict back) and its own CurrentContext.
type ContextExitRequest struct {
	// Exit is the exit script path, relative to Dir.
	Exit string

	// Event is the Stop that triggered the exit check.
	Event event.Event

	// TranscriptPath names the session record exit reads. May be "".
	TranscriptPath string

	// Gates is every declared gate's most recent verdict, by name — how the
	// paired context reads a gate's own verdict back to decide active/inactive.
	Gates map[string]natures.GateState

	// CurrentContext is this context's own entry from the context[] map.
	CurrentContext natures.ContextState

	// Dir is the context's own folder.
	Dir string

	// Name is the context's name.
	Name string

	// Workspace and SessionID are the session facts exit's environment carries —
	// see ContextEnterRequest. Empty leaves them unset.
	Workspace string
	SessionID string

	// LaunchedBy is the re-entry provenance emitted as SLOPRAIL_LAUNCHED_BY — see
	// ContextEnterRequest.LaunchedBy. The caller threads it through so an exit that
	// spawns sr-agent does not re-enter this context on the launched agent's writes.
	LaunchedBy string
}

// EnterContext runs a context's `enter` script and returns the payload it
// produced, whether the context should be active, and a verdict when the script
// could not run.
//
// # What enter's exit code means
//
// enter is not a pass/fail gate the way a check is: it is the script that
// DECIDES activation and extracts the payload. So a clean exit means "this
// context is active this cycle" and its stdout (a flat JSON object) REPLACES the
// payload. Exiting WITHOUT output leaves the payload as it was (the spec:
// "Exiting without producing output leaves the context's state as it was") — so
// the previous payload is returned unchanged in that case, and active is still
// true, because enter ran and did not decline.
//
// A NON-ZERO exit is how an enter declines to (re-)activate on this trigger —
// the context is not entered by this occurrence. This is distinct from a script
// that could not RUN AT ALL (a missing/again-non-executable script, a NUL in the
// command), which is a mechanism failure: fail-closed, returned as a refusing
// Verdict so the caller can report it, and the context's state is left untouched
// (neither activated nor its payload changed) because a mechanism that failed
// decided nothing.
//
// That refusal is NOT a decline and must not be read as one: a context left inactive by a
// broken enter silently disarms every rule that reads it. The caller (services/sr-session)
// therefore refuses the event that triggered the enter (a Pre* kind is denied; a Post* kind
// is refused at the Stop that handles it), and the context's state is untouched.
//
// The returns: (payload, active, verdict, err). active is meaningful only when
// verdict is a pass. When verdict.Refused, the script could not run (or printed output that
// is not a flat JSON object) and the caller must refuse the trigger with verdict.Reason.
func (r Runner) EnterContext(req ContextEnterRequest) (payload map[string]any, active bool, v Verdict, err error) {
	r = r.withDefaults()

	stdin, err := json.Marshal(declaration.ContextEnterPayload{
		Event:          declaration.FlatEvent(req.Event),
		TranscriptPath: req.TranscriptPath,
		Gates:          gatesMap(req.Gates),
		CurrentContext: req.CurrentContext,
	})
	if err != nil {
		return nil, false, Verdict{}, err
	}

	res, err := r.runScript(scriptCall{
		Dir:            req.Dir,
		Script:         req.Enter,
		Stdin:          stdin,
		GuardName:      req.Name,
		Workspace:      req.Workspace,
		SessionID:      req.SessionID,
		TranscriptPath: req.TranscriptPath,
		LaunchedBy:     req.LaunchedBy,
	})
	if err != nil {
		return nil, false, Verdict{}, err
	}

	// A script that could not be RUN (start error, timeout) is a mechanism
	// failure: runScript already turned it into !Passed with a diagnostic reason,
	// but so did a plain non-zero exit. The two are told apart by whether the
	// reason is a could-not-run diagnosis; runScript does not surface that split,
	// so this treats every non-pass the same way the spec's enter semantics allow
	// — a non-zero exit means "do not (re-)activate on this trigger". That is the
	// safe reading: an enter that declined and an enter that crashed both leave
	// the context not freshly entered by THIS occurrence, and neither should
	// invent a payload. The caller leaves the context's prior state as it was.
	if !res.Passed && res.Unrunnable {
		// An enter that could not RUN (a script that lost its shebang or execute bit, is gone,
		// or was killed on the timeout) is not a decline: reading it as one would leave the
		// context out of force, and what it guards silently unjudged. Refuse, with the
		// diagnosis (the file and the fix). The caller refuses the event that triggered it.
		return nil, false, refuse(unenterableReason(req.Name, req.Enter, res.Reason)), nil
	}
	if !res.Passed {
		// Not activated by this trigger. The caller leaves the context's state
		// (active flag and payload) exactly as it was — enter declining is not the
		// same as exit saying done, and must not clear an already-active context.
		return nil, false, pass(), nil
	}

	// enter ran clean. Its stdout REPLACES the payload — unless it printed
	// nothing, which the spec defines as "leave the state as it was", so the
	// previous payload is carried forward.
	trimmed := trimSpace(res.Stdout)
	if len(trimmed) == 0 {
		return req.CurrentContext.Payload, true, pass(), nil
	}
	replaced, perr := parseEnterPayload(trimmed)
	if perr != nil {
		// enter said it activated but produced output this engine cannot read as a
		// flat object. Fail-closed: refuse, naming the fault, and leave the state
		// as it was rather than store a payload the author did not intend. A
		// context whose enter emits garbage is a bug the author must see, not a
		// silent activation with an empty payload.
		return nil, false, refuse(fmt.Sprintf(
			"the %q context could not be entered: its enter script %q produced output this engine could not read as a flat JSON object (%v). "+
				"A context that cannot be entered cannot be judged, so what it guards cannot be judged either: this is refused rather than read as \"not active\". "+
				"Fix: make the script print nothing, or one flat JSON object, on success", req.Name, req.Enter, perr)), nil
	}
	return replaced, true, pass(), nil
}

// ExitContext runs a context's `exit` script on a Stop and reports whether the
// context now considers itself DONE.
//
// # exit does NOT block the Stop
//
// This is the reversal (spec slice 7): a context's exit only flips its own
// `active`. So `done` means "this context is DONE — mark it inactive", NOT "block
// the turn". The caller reads it to set active=false and NEVER turns it into a
// block. A gate bound to Stop is what blocks a turn, and that goes through
// Runner.Run.
//
// # Exit-code convention (the examples' convention, kept)
//
// A clean exit (zero) means the context is DONE — deactivate. A NON-ZERO exit
// means NOT done — stay active for another cycle. This is exactly what the
// shipped example (examples/eval-loop-maxing goal-tracking/exit.sh) does:
//
//	if [ "$status" = "pass" ]; then exit 0   # target met — deactivate
//	fi;                                exit 1   # not met — stay active
//
// It is the natural shell reading of a "are we done?" question — exit 0 is
// "yes/success" — and matches the spec's "Some contexts never say done, staying
// active for the rest of the session; that falls out of the script": such a
// context's exit simply never exits zero.
//
// A script that could not RUN AT ALL is folded into the NON-ZERO case — stay
// active — and that IS the fail-closed direction here. A context is read two ways
// and both read an ACTIVE context as MORE guarding: a gate's `require:
// [{context}]` runs (and can block) only while the context is active, and a
// file-guard's `context[x].active` match applies only while it is active. So
// keeping a context active when its exit could not run makes every reader keep
// guarding, which is the safe way to be wrong — the same fail-closed principle a
// check follows, arriving at "stay active" because of what reads this flag.
//
// Returns (done, reason). done is whether to mark the context inactive. reason is
// a diagnostic naming the fault when the exit script could not run (empty on a
// clean exit or a plain non-zero "stay active") — for the caller to log, never to
// block on.
func (r Runner) ExitContext(req ContextExitRequest) (done bool, reason string, err error) {
	r = r.withDefaults()

	stdin, err := json.Marshal(declaration.ContextExitPayload{
		Event:          declaration.FlatEvent(req.Event),
		TranscriptPath: req.TranscriptPath,
		CurrentContext: req.CurrentContext,
		Gates:          gatesMap(req.Gates),
	})
	if err != nil {
		return false, "", err
	}

	res, err := r.runScript(scriptCall{
		Dir:            req.Dir,
		Script:         req.Exit,
		Stdin:          stdin,
		GuardName:      req.Name,
		Workspace:      req.Workspace,
		SessionID:      req.SessionID,
		TranscriptPath: req.TranscriptPath,
		LaunchedBy:     req.LaunchedBy,
	})
	if err != nil {
		return false, "", err
	}

	if res.Passed {
		// Clean exit (zero): the context is DONE — deactivate.
		return true, "", nil
	}

	// Non-zero (or could-not-run): NOT done — stay active. A plain "stay active"
	// exit carries no diagnostic; a could-not-run carries runScript's reason so the
	// caller can log a genuine fault. Either way the context stays active, the
	// more-guarding direction. Never a block.
	return false, res.Reason, nil
}

// parseEnterPayload reads enter's stdout as the flat JSON object that REPLACES
// the context's payload.
//
// The spec calls enter's stdout "a flat JSON object" that replaces payload
// (ContextDeclaration.enter). Unlike a prepare script's `{additionalContext:…}`
// envelope, enter's whole stdout IS the payload — so this reads a top-level
// object directly. Anything that is not a JSON object (a bare array, a string, a
// number) is an error the caller turns into a fail-closed refusal, because a
// payload is a keyed object every reader indexes by name.
func parseEnterPayload(trimmed []byte) (map[string]any, error) {
	var payload map[string]any
	if err := json.Unmarshal(trimmed, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// gatesMap returns a non-nil gates map, so a payload always carries `gates` as
// an object rather than a null — the same "always an object" discipline the
// context map keeps.
func gatesMap(g map[string]natures.GateState) map[string]natures.GateState {
	if g == nil {
		return map[string]natures.GateState{}
	}
	return g
}

// unenterableReason is the refusal for an enter that could not run: the context, the script, the
// diagnosis (cause carries the file and its fix for a script that lost its shebang or execute
// bit), and why this is a refusal rather than a decline.
func unenterableReason(context, script, cause string) string {
	return fmt.Sprintf(
		"the %q context could not be entered: its enter script %q could not run (%s). "+
			"A context that cannot be entered cannot be judged, so what it guards cannot be judged either: "+
			"this is refused rather than read as \"not active\". "+
			"Fix: the script must exist, be executable (`chmod +x`) and start with `#!/usr/bin/env bash`",
		context, script, cause)
}

// ContextScriptFault reports why a context's declared enter or exit script cannot run, or nil
// when it can (or may: a bare word is a program on PATH, left to the exec). Unlike
// scriptexec.VerifyDeclared, a script file that does not exist is a fault here: a context whose
// enter or exit is missing is never entered or never closed, and nothing else says so.
func ContextScriptFault(dir, script string) error {
	if strings.TrimSpace(script) == "" {
		return nil
	}
	if err := scriptexec.VerifyDeclared(dir, script); err != nil {
		return err
	}
	if p := scriptexec.Path(dir, script); p != "" {
		if _, err := os.Stat(p); err != nil {
			return fmt.Errorf("%s was not found (%v)", p, err)
		}
	}
	return nil
}
