// Package dispatch is the shared CHECK-RUNNER at the heart of the new
// nature-based guardrails: given a matched declaration's `require` and `checks`
// and the context of a fired event, it runs them in order and returns one
// verdict — pass, or refuse-with-reason.
//
// # Why a package of its own
//
// Every nature that decides against an event does it the same way. A gate wakes
// on a trigger, evaluates `require` then `checks`, and blocks or admits. A
// file-guard (the next slice) wakes on a file's state and does the identical
// thing — same Prerequisite semantics, same Check ordering, same payload
// assembly, same judge substrate. The one part that differs is which PAYLOAD a
// check is handed (a file-guard's CheckPayload vs a gate's GateCheckPayload) and
// which JUDGE-INPUT a judge renders against (FileJudgeInput vs GateJudgeInput) —
// and the spec keeps even those at parity (dot-dir-file-store/main.tsp). So the
// running is factored here once, parameterised by the payload shape, and each
// nature's dispatch (in services/sr-session) supplies only the matched rule and
// the fired event.
//
// This package REUSES rather than reinvents. The declaration types (Prerequisite,
// Check) and the payload/judge shapes are internal/declaration's; the runtime
// state maps (ContextState, GateState) are internal/natures'; the trajectory
// reader (for a `{skill}` prerequisite) is internal/transcript's; the judge
// substrate is the sr-agent binary, run BY NAME the same way the old-format judge
// hooks run it. What this package adds is the ordering, the native `require`
// evaluation, the payload assembly, and the script/judge/prepare execution.
//
// # The contract, in one place
//
// A caller builds a Request naming the require + checks, the fired event, the
// transcript path, the context[]/gates[] state maps the next slice populates, the
// guard's own folder (scripts resolve relative to it), and which nature this is
// (so the right payload shape is assembled). Run returns a Verdict: Refused plus
// a Reason when a prerequisite is missing or a check refused, or a clean pass.
//
// # Fail-closed
//
// The engine's default is that a check which cannot be RUN is a refusal, not a
// permission — a mechanism that failed must never read as approval (the same rule
// services/sr-session's old dispatch follows for a hook that cannot start). A
// script that will not execute, a prepare that fails, a judge whose substrate is
// missing: each refuses, with a reason naming what to fix. The ONE documented
// exception is a judge's own model-call flakiness, which an individual judge may
// choose to fail-open on inside its own verify script — but this runner's default
// for the judge substrate failing to run at all is closed, matching sr-agent's
// own --verify fail-closed default.
package dispatch

import (
	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/natures"
)

// Nature selects which payload/judge-input shape a check is handed — the one
// axis on which the two natures wired so far actually differ.
//
// A file-guard's checks receive declaration.CheckPayload (and a judge
// FileJudgeInput); a gate's receive declaration.GateCheckPayload (and a judge
// GateJudgeInput). The runner is otherwise identical, so this is a small enum
// rather than two parallel entry points — a caller says which nature it is and
// the runner assembles the matching shape.
type Nature string

const (
	// NatureFileGuard assembles a file-guard's CheckPayload / FileJudgeInput.
	NatureFileGuard Nature = "file-guard"

	// NatureGate assembles a gate's GateCheckPayload / GateJudgeInput.
	NatureGate Nature = "gate"
)

// Request is everything the check-runner needs to reach a verdict on one fired
// event against one matched rule.
//
// It carries the state maps (Context, Gates) as INPUTS rather than reading them
// itself, which is the coordination point with the next slice: for this slice a
// gate's `require: [{context}]` reads whatever context state exists (an engine
// ordering guarantee the full context lifecycle will provide), and the maps are
// threaded through so the slice that populates them changes only the caller, not
// this runner. Neither map is required — a nil map is an empty world, which is
// the correct reading before any context has entered or any gate has run.
type Request struct {
	// Nature is which payload/judge-input shape the checks are handed.
	Nature Nature

	// Require are the preconditions that must hold before any check runs, in
	// order. A failed prerequisite refuses before a check is paid for.
	Require []declaration.Prerequisite

	// Checks are the checks to run, in order, first-refusal-ends-it.
	Checks []declaration.Check

	// Event is the fired event the checks decide about — a pre-action GateEventKind
	// for a gate, a file event for a file-guard. Carried into the payload as-is.
	Event event.Event

	// TranscriptPath names the session record a check (or the native `{skill}`
	// prerequisite) reads for what the event does not carry. May be "" when the
	// session's record could not be resolved; a `{skill}` prerequisite then
	// refuses, because a precondition it cannot check has not passed.
	TranscriptPath string

	// Context is every declared context by name, at parity with the nature's match
	// scope — what a check reads as `context[<name>]`, and what a `{context}`
	// prerequisite consults. Nil is an empty map.
	Context map[string]natures.ContextState

	// Gates is every declared gate's most recent verdict by name. Carried for the
	// payloads that expose it (a context's enter/exit; not a gate/file-guard check,
	// which the spec gives no `gates`) and threaded through for the next slice. Nil
	// is an empty map.
	Gates map[string]natures.GateState

	// Dir is the rule's own folder, against which a check's `script`, `judge` and
	// `prepare` paths resolve — the same relative-to-the-guard rule the spec states
	// and the old format's guardrailDir provides.
	Dir string

	// GuardName is the rule's name, used only for diagnostics and for the judge's
	// isolation. Not load-bearing for the verdict.
	GuardName string

	// Workspace is the tree being guarded, passed into a check's environment as
	// SR_WORKSPACE so a script can resolve a workspace-relative path (a goal's
	// verify.sh under <workspace>/goal/…) and key its own `sr-session state`. The
	// caller resolves it (the payload's cwd, git-rooted); the runner only forwards
	// it. Empty leaves SR_WORKSPACE unset.
	Workspace string

	// SessionID is the conversation's identity, passed as SR_SESSION_ID so a
	// check's own `sr-session state` lands in this session's keyspace. Empty leaves
	// it unset (diagnosable). Not load-bearing for the verdict.
	SessionID string

	// LaunchedBy is the re-entry provenance — the colon-separated list of guards
	// whose checks are on the current call stack — passed into every check's
	// environment as SLOPRAIL_LAUNCHED_BY. A check that spawns sr-agent (a judge,
	// or a script that shells it) carries this across the exec into the launched
	// agent, whose own hooks read it and decline to re-fire THOSE guards on the
	// agent's writes. Without it a judging guard re-fires on itself and recurses.
	//
	// The caller computes it with services/sr-session's appendLaunchedBy (this
	// guard appended to any inherited value, deduped) because this package sits
	// below that one and cannot import its provenance logic. The runner only
	// forwards it onto the scriptCall / judgeCall. Empty leaves the variable unset
	// — correct for a check that cannot launch an agent, harmless for one whose own
	// name is the only entry. Not load-bearing for THIS request's verdict; it
	// governs the verdict of the dispatch one exec down.
	LaunchedBy string
}

// Verdict is what the check-runner concluded about one fired event.
//
// Refusal is a field rather than an empty-reason convention, the same design the
// old dispatch's verdict uses and for the same reason: encoding "refused" as
// "said something" is what let a refusal with nothing to say read as consent. A
// clean pass is the zero value.
//
// Abstained is a THIRD state, distinct from both — a check that reached no verdict
// of its own (a judge whose prepare emitted `skip`; see runJudgeCheck). It is
// meaningful only WITHIN the check loop: an abstaining check neither refuses nor
// counts as an affirmative pass, so the loop continues to the next check rather
// than ending. It is an internal signal — Run itself never returns an Abstained
// verdict to a caller (it collapses to the clean pass when the whole chain reached
// no refusal), so every consumer still reads only Refused/Reason exactly as before.
type Verdict struct {
	// Refused reports whether the action must not proceed. When true, Reason is
	// non-empty.
	Refused bool

	// Reason is what to tell the agent on a refusal — the prerequisite's remedy,
	// or the check's own words. Never empty when Refused.
	Reason string

	// Abstained reports that this ONE check reached no verdict and drops out of the
	// chain — the other checks decide. Never true together with Refused. Set only by
	// a judge check whose prepare asked to skip; the loop treats it as "continue,
	// count nothing", and Run never surfaces it (an all-abstain chain permits, the
	// same as reaching the end with no refusal).
	Abstained bool
}

// pass is the clean verdict.
func pass() Verdict { return Verdict{} }

// refuse is a refusal carrying its reason.
func refuse(reason string) Verdict { return Verdict{Refused: true, Reason: reason} }

// abstain is the no-verdict outcome of a single check — it neither refuses nor
// passes affirmatively, so the check loop skips past it to let the remaining
// checks decide (and permits by default if none refuse). Used by a judge check
// whose prepare emitted `skip`.
func abstain() Verdict { return Verdict{Abstained: true} }

// Runner runs a Request's require + checks and returns a verdict.
//
// A struct rather than a bare function so its two collaborators — the trajectory
// reader a `{skill}` prerequisite uses, and the judge substrate a judge check
// invokes — can be substituted in a unit test without a real transcript file or a
// real model call. Both default to the production implementations when the
// zero-value Runner is used, so a caller that wants the real thing writes
// `dispatch.Runner{}.Run(req)`.
type Runner struct {
	// skillLoaded reports whether a Skill tool_use for the named skill is in the
	// session's own trajectory (excluding sub-agents). Defaults to the transcript
	// reader; a test supplies its own.
	skillLoaded func(transcriptPath, skill string) (bool, error)

	// runJudge invokes the judge substrate and reports its verdict. Defaults to the
	// sr-agent path; a test supplies its own so no model is called.
	runJudge func(j judgeCall) (Verdict, error)

	// runScript runs a script/prepare executable with the payload on stdin.
	// Defaults to the exec path; a test supplies its own.
	runScript func(s scriptCall) (scriptResult, error)
}

// Run evaluates the request and returns a verdict.
//
// The order is the spec's: require first (a missing prerequisite refuses before a
// check is paid for), then checks in declaration order, first-refusal-ends-it. A
// pure-require rule (no checks) refuses on the missing precondition and otherwise
// passes; a pure-checks rule refuses on the first failing check. A rule with
// neither passes — the loaders guarantee a gate has at least one, so that case is
// a caller's, not this runner's, to forbid.
func (r Runner) Run(req Request) (Verdict, error) {
	r = r.withDefaults()

	// Require first. A prerequisite is a gate on whether the checks are even asked,
	// and a missing one is a refusal carrying the remedy — the same "load the skill"
	// / "the context must have run" the spec names.
	if v, err := r.checkRequire(req); err != nil {
		return Verdict{}, err
	} else if v.Refused {
		return v, nil
	}

	// Then the checks, in order. The first that refuses ends it; a check that
	// could not be RUN is itself a refusal (fail-closed), returned here rather
	// than as an error so the caller renders one refusal shape for every outcome.
	//
	// A check that ABSTAINED (a judge whose prepare emitted `skip`) reaches no
	// verdict: it is not a refusal, so it does not end the loop, and it is not an
	// affirmative pass that could stand in for a later check — the loop simply
	// continues to the next check, which still runs and can still refuse. So
	// first-refusal-still-wins across the checks that DID decide, and an abstain
	// only drops out. Reaching the end with no refusal — every check passed, or
	// abstained, or any mix — is the clean pass; the Abstained flag never leaves
	// this loop.
	for i := range req.Checks {
		v, err := r.runCheck(req, req.Checks[i])
		if err != nil {
			return Verdict{}, err
		}
		if v.Refused {
			return v, nil
		}
	}

	return pass(), nil
}

// CheckRequire evaluates only req.Require and returns its verdict, without
// running any check.
//
// It exists for a caller that has its OWN fail-closed reason to refuse before
// Run's checks could even be attempted — services/sr-session's preventive
// file-guard dispatch is the one caller: a Pre write whose bytes are not yet
// derivable (a command-derived edit) cannot be judged by a content check, but a
// `{skill}` or `{context}` prerequisite needs no content at all, and evaluating
// it first lets that caller give the MORE SPECIFIC reason ("the skill was never
// loaded") instead of the generic "could not verify this write" — when both are
// true, require's reason is the actionable one; the write being unverifiable is
// true of every write this session might attempt, while the missing
// prerequisite names exactly what to fix.
//
// Requires no defaulting beyond what checkRequire itself needs (skillLoaded);
// withDefaults is applied here for the same reason Run applies it, so a caller
// need not construct a Runner any differently to use this instead of Run.
func (r Runner) CheckRequire(req Request) (Verdict, error) {
	r = r.withDefaults()
	return r.checkRequire(req)
}

// withDefaults fills in the production collaborators for any left unset, so the
// zero-value Runner is the real one and a test overrides exactly what it must.
func (r Runner) withDefaults() Runner {
	if r.skillLoaded == nil {
		r.skillLoaded = skillLoadedInTrajectory
	}
	if r.runScript == nil {
		r.runScript = runScriptExec
	}
	if r.runJudge == nil {
		r.runJudge = runJudgeAgent
	}
	return r
}
