package declaration

import (
	"encoding/json"
	"fmt"

	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/natures"
)

// FlatEvent is an event.Event serialized FLAT — the on-the-wire shape a check's
// script and a judge's template read `event` as (dot-dir-file-store/main.tsp).
//
// # Why flat, and why a distinct type
//
// The engine's event.Event marshals through its own envelope as
// `{"kind":…, "fields":{…}}` — the shape the OLD-format hooks read (`.event.fields.path`).
// The NEW declaration formats read the event FLAT: the spec's judge templates say
// `{{ event.newContent }}` and `{{ event.path }}` (main.tsp ~338), its CheckPayload
// doc names `event.newMarkers`/`event.oldMarkers` (~299), and every shipped example
// script and template reads `.event.newContent` / `.event.path` / `.event.kind` /
// `.event.newMarkers` directly — never under a `fields` sub-object. So the payloads
// these declarations receive must carry the event's own fields DIRECTLY under
// `event`, with `kind` alongside them: `{"kind":"PreFileUpdate","path":"…","newContent":"…","newMarkers":[…]}`.
//
// A distinct type rather than a marshal option on the payload structs, because the
// flatness is a property of the EVENT's wire form specifically — `transcriptPath`,
// `context` and `gates` are already flat as ordinary fields, and only `event` needs
// its envelope opened up. Making it a type means every payload that carries an event
// gets the same flat shape by construction (its field is a FlatEvent), and a caller
// cannot accidentally reintroduce the nested envelope by marshaling a bare
// event.Event into an `event` key. The underlying struct is event.Event's, so a
// caller converts with FlatEvent(e) at no cost.
type FlatEvent event.Event

// eventKindKey is the discriminator key a flat event carries alongside its fields.
// The event's own field names must not collide with it — no module declares a field
// named `kind`, and MarshalJSON writes `kind` LAST so the event's kind always wins
// over a stray field of that name rather than being shadowed by it.
const eventKindKey = "kind"

// MarshalJSON writes the event flat: its declared fields spread at the top level,
// with `kind` alongside them.
//
// A nil Fields map is an event that carries nothing but its kind (a Stop), which
// marshals to `{"kind":"Stop"}` — an object, never a null, so a script indexing
// `.event.<anything>` gets a clean miss rather than an error, the same "always an
// object" discipline the event envelope keeps.
func (e FlatEvent) MarshalJSON() ([]byte, error) {
	out := make(map[string]any, len(e.Fields)+1)
	for k, v := range e.Fields {
		out[k] = v
	}
	// Kind last, so the discriminator cannot be shadowed by a field named `kind`.
	out[eventKindKey] = e.Kind
	return json.Marshal(out)
}

// UnmarshalJSON reads a flat event back into {Kind, Fields}: `kind` becomes Kind and
// every other key becomes a field. The inverse of MarshalJSON, so a payload written
// by this package round-trips — which is what a test asserting on an assembled
// payload, and any Go consumer reading one back, relies on.
func (e *FlatEvent) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	var ev FlatEvent
	if kindRaw, ok := raw[eventKindKey]; ok {
		if err := json.Unmarshal(kindRaw, &ev.Kind); err != nil {
			return fmt.Errorf("flat event: kind: %w", err)
		}
		delete(raw, eventKindKey)
	}
	if len(raw) > 0 {
		ev.Fields = make(map[string]any, len(raw))
		for k, v := range raw {
			var val any
			if err := json.Unmarshal(v, &val); err != nil {
				return fmt.Errorf("flat event: field %q: %w", k, err)
			}
			ev.Fields[k] = val
		}
	}
	*e = ev
	return nil
}

// Event returns the flat event as an ordinary event.Event, for a Go consumer that
// wants the envelope form back after reading a payload.
func (e FlatEvent) Event() event.Event { return event.Event(e) }

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
	// is preventive, otherwise a Post*). A FlatEvent, so its `kind` and fields are
	// read FLAT under `event` (`.event.path`, `.event.newContent`,
	// `.event.newMarkers`) the way the spec models it and every example reads it —
	// not through the nested `{kind, fields}` envelope the old format used.
	Event FlatEvent `json:"event"`

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
	// Event is the fired pre-action event — a GateEventKind variant. A FlatEvent,
	// read FLAT under `event` (`.event.path`, `.event.invocations`) the way the
	// spec models it — see CheckPayload.Event.
	Event FlatEvent `json:"event"`

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
	// Event is the trigger that fired — a ContextEventKind variant. A FlatEvent,
	// read FLAT under `event` (`.event.newContent`, `.event.path`, `.event.kind`,
	// `.event.tags`) the way the context example scripts read it.
	Event FlatEvent `json:"event"`

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
	// Event is always the Stop that triggered the exit check. A FlatEvent, read
	// FLAT under `event` — a Stop carries only `.event.kind`, but the shape is the
	// same flat one every other payload uses so a script reads it uniformly.
	Event FlatEvent `json:"event"`

	// TranscriptPath names the session record exit reads.
	TranscriptPath string `json:"transcriptPath"`

	// CurrentContext is this context's own entry from the `context` map.
	CurrentContext natures.ContextState `json:"currentContext"`

	// Gates is every declared gate's most recent verdict, by name.
	Gates map[string]natures.GateState `json:"gates"`
}
