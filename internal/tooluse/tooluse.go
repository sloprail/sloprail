// Package tooluse is the module for the tool call itself: what the harness is
// about to run, before any file or command meaning is derived from it.
//
// It exists because a rule sometimes has to gate a tool that neither a file
// event nor a command event covers — an MCP call, a web fetch, a bespoke tool —
// or activate a context from "some tool ran" plus the trajectory rather than
// from a specific file or command shape. The file and command modules answer
// "what does this DO to the project"; this one answers the narrower, earlier
// question "what tool is this", unopinionated because it exists precisely for
// the cases the derivation does not already answer.
//
// It is the harness-native pre-action moment, so it has no Post counterpart: a
// tool that already ran leaves its consequences as the file module's Post
// events, and the record already holds the call itself. See events/main.tsp,
// EventKind.PreToolUse, and the TrajectoryEvent union's note on why PreToolUse
// is an engine listener event with nothing to re-derive from a record.
package tooluse

import (
	"encoding/json"

	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/module"
)

// Name identifies this module. It is how the engine reports which module
// produced an event, and how a module is switched off — not a prefix the kinds
// carry.
const Name = "tooluse"

// KindPreToolUse is the one kind this module declares.
//
// One kind, and only a Pre one. A tool is either about to run or has run; the
// "has run" case is not this module's, because what a tool changed is reported
// as the file module's Post events off the tree diff, and the raw call is
// already visible on the record's own entry — there is nothing for a Post
// tool-use event to add that is not said better elsewhere.
const KindPreToolUse = "PreToolUse"

// Field names. They appear here, in Kinds below, and in the conversion in
// event.go — nowhere else, so a rename cannot leave a matcher checking against
// a name the events no longer carry.
const (
	// FieldTool is the tool's name as the harness reports it.
	FieldTool = "tool"

	// FieldInput is the tool's input as the harness reports it. Its shape depends
	// on the tool, so it is declared as an OPEN map — Record<unknown> in the spec
	// — and a matcher reading `input.file_path` reaches into it unchecked, the way
	// commandmod's `flags` is open. A closed type would refuse a key the engine
	// has not heard of, which is the checker punishing an author for a vocabulary
	// this module deliberately does not own.
	FieldInput = "input"
)

// Pending is what this module is given about a tool call a harness is about to
// make. Declared here rather than imported from another module for the reason
// filemod gives for declaring its own: two modules reading one payload is not
// two modules sharing a type. A module reads what it recognises and ignores the
// rest.
type Pending interface {
	// Tool is what the harness calls the tool it is about to run.
	Tool() string
	// Arguments are the tool's own, as the harness gave them, undecoded.
	Arguments() json.RawMessage
}

// Module produces the pre-tool event.
type Module struct{}

// New returns the tool-use module.
func New() *Module { return &Module{} }

// Name implements module.Module.
func (*Module) Name() string { return Name }

// Kinds implements module.Module.
//
// `tool` is a plain string a rule narrows on — `tool == "WebFetch"`. `input` is
// the tool's own arguments, left open because their shape is the tool's business
// and this module has no vocabulary to enumerate. Naming the keys would refuse
// every tool the engine has not been told about, so it declares the map and
// nothing inside it — the same choice commandmod makes for `flags`.
func (*Module) Kinds() []module.KindDecl {
	return []module.KindDecl{
		{
			Name: KindPreToolUse,
			Fields: []module.FieldDecl{
				{Name: FieldTool, Type: module.TypeString},
				{Name: FieldInput, Type: module.TypeMap},
			},
		},
	}
}

// Extract implements module.Module.
//
// One event per pre-tool call, and none at any other time. A harness reports a
// tool call once, at the pre-action moment, and this module turns exactly that
// into one PreToolUse — unlike the file and command modules, it does not branch
// on the SHAPE of the arguments, because it is not asking what the tool does. It
// is reporting that a tool is about to run, whatever tool it is.
//
// It produces nothing in the Post phase: there is no settled state a diff
// establishes for "a tool ran" the way there is for a file, so a Post tool-use
// event would be manufacturing a fact the tree cannot confirm.
//
// A tool that names no arguments, or arguments that are not a JSON object, still
// produces the event with an empty `input`: the fact worth reporting is that the
// tool is about to run, and a rule narrowing on `tool` alone must see it. The
// input is a convenience for a rule that wants more, not a gate on whether the
// event exists.
func (m *Module) Extract(in module.Input) ([]event.Event, error) {
	if in[module.InputPhase] == module.PhasePost {
		// A tool having run is not a difference a diff establishes; the file
		// module's Post events carry what it changed, and the record holds the
		// call. Nothing honest for this module to add after the fact.
		return nil, nil
	}

	pending, ok := in[module.InputPayload].(Pending)
	if !ok {
		return nil, nil
	}

	e := ToolEvent{Tool: pending.Tool()}
	// The arguments are decoded to a map where they are one, and left empty
	// otherwise. A tool whose input is not an object — or a payload with no
	// arguments at all — still gets an event, because the event's purpose is to
	// say a tool is about to run, and a rule on `tool` alone depends on it.
	// Decoding failure is not an error the caller has to handle: an unparseable
	// input is the harness's shape, not this module's mistake.
	var input map[string]any
	if len(pending.Arguments()) > 0 {
		_ = json.Unmarshal(pending.Arguments(), &input)
	}
	e.Input = input

	return []event.Event{e.Event()}, nil
}
