package commandmod

import (
	"fmt"

	"github.com/sloprail/sloprail/internal/event"
)

// Invocation is one program a command line invokes.
type Invocation struct {
	// Bin is the program, as its basename — `npm`, not `/usr/local/bin/npm`.
	// A rule about npm should not have to enumerate the paths npm can live at,
	// and an agent should not be able to evade one by spelling the path out.
	Bin string

	// Argv is the resolved argument vector, program included. Resolved means
	// quoting and escaping are already undone: `n\pm` and `n""pm` both arrive
	// here as `npm`.
	Argv []string

	// Flags are the flags parsed out of Argv, keyed by name without its
	// leading dashes. A flag given without a value carries an empty string, so
	// a rule can ask whether a flag is present without knowing whether that
	// flag takes one.
	Flags map[string]string
}

// CommandEvent is what this module's own code passes around.
//
// Typed here, a map at the boundary — the same split the file module makes,
// and for the same reason. The engine has no use for a struct whose shape it
// cannot know, and this module has no use for a map when it is the one putting
// the values in.
type CommandEvent struct {
	// Raw is the command line exactly as written, before any resolution.
	Raw string

	// Invocations is every invocation the line performs, flattened out of the
	// pipelines, chains, subshells and wrappers that nest them. Order is the
	// order they appear in the line, not the order they would run — a rule
	// asks what is about to run, not when.
	Invocations []Invocation
}

// Event converts to the wire form.
//
// The invocation list is flattened to plain maps rather than left as structs,
// because what reads it is a matcher expression and a hook's JSON decoder,
// neither of which knows this package's types.
func (c CommandEvent) Event() event.Event {
	invs := make([]any, 0, len(c.Invocations))
	for _, inv := range c.Invocations {
		argv := make([]any, 0, len(inv.Argv))
		for _, a := range inv.Argv {
			argv = append(argv, a)
		}
		flags := make(map[string]any, len(inv.Flags))
		for k, v := range inv.Flags {
			flags[k] = v
		}
		invs = append(invs, map[string]any{
			KeyBin:   inv.Bin,
			KeyArgv:  argv,
			KeyFlags: flags,
		})
	}

	return event.Event{
		Kind: KindPreInvoke,
		Fields: map[string]any{
			FieldRaw:         c.Raw,
			FieldInvocations: invs,
		},
	}
}

// FromEvent converts back, for this module's own reads of events it produced.
//
// An event carrying no raw command line is an error rather than a zero value:
// every event this module emits was produced from a command line, so one
// without it did not come from here, and returning an empty command would let
// a caller judge a line nobody ran. An empty invocation list is not an error —
// a line whose programs could not be resolved is exactly the case this module
// promises to report honestly rather than guess at.
func FromEvent(e event.Event) (CommandEvent, error) {
	c := CommandEvent{}
	if v, ok := e.Fields[FieldRaw].(string); ok {
		c.Raw = v
	}
	if c.Raw == "" {
		return CommandEvent{}, fmt.Errorf("commandmod: %q carries no %s", e.Kind, FieldRaw)
	}

	list, _ := e.Fields[FieldInvocations].([]any)
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		inv := Invocation{Flags: map[string]string{}}
		if v, ok := m[KeyBin].(string); ok {
			inv.Bin = v
		}
		if argv, ok := m[KeyArgv].([]any); ok {
			for _, a := range argv {
				if s, ok := a.(string); ok {
					inv.Argv = append(inv.Argv, s)
				}
			}
		}
		if flags, ok := m[KeyFlags].(map[string]any); ok {
			for k, v := range flags {
				if s, ok := v.(string); ok {
					inv.Flags[k] = s
				}
			}
		}
		c.Invocations = append(c.Invocations, inv)
	}
	return c, nil
}
