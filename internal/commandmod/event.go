package commandmod

import (
	"fmt"

	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/grounding"
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
	// leading dashes. Each value is every occurrence of that flag, in the
	// order given — a flag given once is a one-element slice, not a bare
	// string, so a repeated flag (`--cite:user a --cite:user b`) is not lost
	// to a last-wins collision the way a plain map would lose it. A flag
	// given without a value carries an empty string at that position, so a
	// rule can still ask whether a flag is present without knowing whether
	// that flag takes one.
	Flags map[string][]string

	// Cwd is the directory this program runs in, as far as the line itself
	// says — threaded through every `cd` ahead of it in its own scope (a
	// subshell's `cd` does not leak out of the subshell; see cwd.go):
	//
	//	"."          where the command line started — no `cd` moved it
	//	"sub/dir"    relative to where the line started (`cd sub/dir && …`)
	//	"/abs/dir"   absolute, once a `cd` named an absolute directory
	//	""           unknown — a `cd` this module cannot resolve without
	//	             running something (`cd "$DIR"`, `cd -`, `pushd`)
	//
	// Where the line started is the harness's working directory for the tool
	// call, which this pure function of a string does not have: a consumer
	// joins a relative Cwd onto it. Empty means unknown rather than "the
	// start", because a program whose directory was lost must not be read as
	// running where the line began — that is a guess, and a path resolved
	// against it could name a file the command never touches.
	Cwd string

	// Env is the environment the line itself sets for this program, NAME to
	// value: a prefix (`FOO=1 cmd`), a wrapper's assignments (`env FOO=1 cmd`,
	// `sudo FOO=1 cmd`), and an `export FOO=1` earlier in the same shell scope
	// (a subshell's export stays inside it). The value is "" when it is not a
	// literal word (`FOO=$X`, `FOO=$(cmd)`): the name is certain, the value is
	// not. Nil/empty when the line sets nothing. Only what the text says: the
	// environment the harness started with is not in it.
	Env map[string]string
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
		// Each value is EVERY occurrence, so the wire form is always a JSON
		// array — never a bare string — even for a flag given once. One shape
		// for every flag, rather than a scalar-or-array union a matcher or a
		// script would have to branch on before it can read a value.
		flags := make(map[string]any, len(inv.Flags))
		for k, vs := range inv.Flags {
			arr := make([]any, 0, len(vs))
			for _, v := range vs {
				arr = append(arr, v)
			}
			flags[k] = arr
		}
		env := make(map[string]any, len(inv.Env))
		for k, v := range inv.Env {
			env[k] = v
		}
		invs = append(invs, map[string]any{
			KeyBin:   inv.Bin,
			KeyArgv:  argv,
			KeyFlags: flags,
			KeyCwd:   inv.Cwd,
			KeyEnv:   env,
		})
	}

	return event.Event{
		Kind: KindPreInvoke,
		Fields: map[string]any{
			FieldRaw:                 c.Raw,
			FieldInvocations:         invs,
			grounding.FieldCitations: grounding.ToWire(nil),
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
		inv := Invocation{Flags: map[string][]string{}}
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
		if v, ok := m[KeyCwd].(string); ok {
			inv.Cwd = v
		}
		if env, ok := m[KeyEnv].(map[string]any); ok {
			for k, v := range env {
				if s, ok := v.(string); ok {
					if inv.Env == nil {
						inv.Env = map[string]string{}
					}
					inv.Env[k] = s
				}
			}
		}
		if flags, ok := m[KeyFlags].(map[string]any); ok {
			for k, v := range flags {
				// The wire form is always an array (see Event()); a bare
				// string is also accepted here so an event handed in from
				// somewhere that has not adopted the array form still reads,
				// rather than silently losing the flag.
				switch vv := v.(type) {
				case []any:
					for _, item := range vv {
						if s, ok := item.(string); ok {
							inv.Flags[k] = append(inv.Flags[k], s)
						}
					}
				case string:
					inv.Flags[k] = []string{vv}
				}
			}
		}
		c.Invocations = append(c.Invocations, inv)
	}
	return c, nil
}
