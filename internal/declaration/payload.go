package declaration

import (
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/natures"
)

// This file is the WIRE/TYPE contract for what a rule's script, prepare and
// judge receive — the payload and judge-input shapes the spec names explicitly
// (dot-dir-file-store/main.tsp), rather than leaving them to prose. They are the
// shapes the future dispatch slice will assemble and hand a check on stdin, and
// the Jinja2 variable namespace a judge template renders against.
//
// They are declared here, with the declarations, because a check's shape is part
// of the rule's contract: a judge template that reads `event.newContent` is
// coupled to CheckPayload the same way it is coupled to the guard's `match`. A
// later slice writes these to a hook's stdin; this slice states them so that
// writing and reading agree on one representation.
//
// # A note on `context` and `gates`
//
// The spec keeps `context` at PARITY across a nature's match scope and its check
// payload: a file-guard's `match` can read `context[<name>]`, so its checks see
// the same map, not a narrower one. Both file-guard and gate payloads therefore
// carry `context`. Neither carries `gates` — no unit built so far has a
// file-guard's or a gate's own check reading a gate's verdict (a gate depends on
// something upstream through `require`, not through this field). A context's
// enter/exit payloads DO carry `gates`, because a context's exit reads a paired
// gate's verdict back to decide its own active/inactive.
//
// The state maps are internal/natures' ContextState and GateState, reused rather
// than redefined — the same shapes a matcher sees, so a check reasoning about
// "am I inside the refactor context" reads the identical `{active, payload}` its
// guard's `match` did.

// CheckPayload is what a file-guard's script and prepare receive on stdin, and
// what a judge receives when no prepare is set (dot-dir-file-store/main.tsp
// CheckPayload). `event` carries the file's own state; `transcriptPath` names the
// session record a check can query for context the event itself does not carry
// (which human message grounds this write); `context` is every declared context
// by name, at parity with the file-guard's match scope.
type CheckPayload struct {
	// Event is the file event — a FileEvent variant (a Pre* only when the guard
	// is preventive, otherwise a Post*). Carried as the envelope every boundary
	// in sloprail uses, so its `kind` and fields are read the same way a matcher
	// reads them.
	Event event.Event `json:"event"`

	// TranscriptPath names the session record a check reads for what the event
	// does not carry.
	TranscriptPath string `json:"transcriptPath"`

	// Context is every declared context, by name, carrying `{active, payload}`.
	Context map[string]natures.ContextState `json:"context"`
}

// GateCheckPayload is what a gate's script, prepare and judge receive on stdin
// (dot-dir-file-store/main.tsp GateCheckPayload). Like a file-guard's
// CheckPayload but its `event` is any GateEventKind — a gate wakes on command,
// tool and Stop events too, never a Post variant. `context` is carried at top
// level, at parity with the gate's match scope, so a gate's own checks can read
// what an upstream context left behind rather than reconstruct it.
type GateCheckPayload struct {
	// Event is the fired pre-action event — a GateEventKind variant.
	Event event.Event `json:"event"`

	// TranscriptPath names the session record for the trajectory-reading a gate's
	// checks usually do.
	TranscriptPath string `json:"transcriptPath"`

	// Context is every declared context, by name.
	Context map[string]natures.ContextState `json:"context"`
}

// PreparedContext is what a prepare script's stdout carries under its one
// supported key, `additionalContext` (dot-dir-file-store/main.tsp
// PreparedContext): a freeform, flat JSON object with no fixed shape. Named
// explicitly rather than left as prose, so a prepare script's contract is a real
// type. Only this one key is read from a prepare's stdout; everything else it
// prints is not part of the contract.
type PreparedContext map[string]any

// FileJudgeInput is the wire/type contract for what a file-guard's judge receives
// (dot-dir-file-store/main.tsp FileJudgeInput): the standard CheckPayload spread
// flat at the template's top level, plus `additionalContext` set exactly when the
// check's own prepare ran and returned one. `payload` is ALWAYS present, never
// replaced — the standard payload and prepare's addition are additive, not
// either/or. A prepare script cannot overwrite `event` or `transcriptPath` by
// choosing a colliding key: its return renders under the single top-level
// `additionalContext` field, not spread into the payload's own namespace.
type FileJudgeInput struct {
	// CheckPayload's fields render unprefixed at the template's top level
	// (`{{ event.newContent }}`, `{{ transcriptPath }}`, `{{ context }}`).
	CheckPayload `json:",inline"`

	// AdditionalContext is present only when the check's own prepare returned one.
	AdditionalContext PreparedContext `json:"additionalContext,omitempty"`
}

// GateJudgeInput is the gate-nature counterpart to FileJudgeInput
// (dot-dir-file-store/main.tsp GateJudgeInput). Same rules: GateCheckPayload's
// own fields spread unprefixed at the template's top level, always present, with
// `additionalContext` only adding to that, never replacing it.
type GateJudgeInput struct {
	// GateCheckPayload's fields render unprefixed at the template's top level.
	GateCheckPayload `json:",inline"`

	// AdditionalContext is present only when the check's own prepare returned one.
	AdditionalContext PreparedContext `json:"additionalContext,omitempty"`
}

// ContextEnterPayload is what a context's `enter` script receives on stdin
// (dot-dir-file-store/main.tsp ContextEnterPayload). `event` is the `on` trigger
// that fired — a ContextEventKind, since entering can be a Post event;
// `transcriptPath` names the session record the script reads to recognise "now
// it's the time" and extract the payload; `gates` lets enter gate its own
// activation on another gate's verdict; `currentContext` is THIS context's own
// last `{active, payload}`, so the script sees what was there before deciding
// whether to grow it, replace it, or leave it alone.
type ContextEnterPayload struct {
	// Event is the trigger that fired — a ContextEventKind variant.
	Event event.Event `json:"event"`

	// TranscriptPath names the session record enter reads.
	TranscriptPath string `json:"transcriptPath"`

	// Gates is every declared gate's most recent verdict, by name.
	Gates map[string]natures.GateState `json:"gates"`

	// CurrentContext is this context's own last state — enter runs on every
	// trigger, active or not, so it needs to see what was there.
	CurrentContext natures.ContextState `json:"currentContext"`
}

// ContextExitPayload is what a context's `exit` script receives on stdin
// (dot-dir-file-store/main.tsp ContextExitPayload). `event` is always the Stop
// that triggered the check — exit is never consulted on anything else — plus
// `transcriptPath`; `currentContext` is THIS context's own entry from the
// `context` map; and `gates`, every declared gate's most recent verdict, is how
// the paired context reads a gate's own verdict back to decide its own
// active/inactive.
type ContextExitPayload struct {
	// Event is always the Stop that triggered the exit check.
	Event event.Event `json:"event"`

	// TranscriptPath names the session record exit reads.
	TranscriptPath string `json:"transcriptPath"`

	// CurrentContext is this context's own entry from the `context` map.
	CurrentContext natures.ContextState `json:"currentContext"`

	// Gates is every declared gate's most recent verdict, by name.
	Gates map[string]natures.GateState `json:"gates"`
}
