// Package commandmod is the module for commands: what a shell command line is
// about to run.
//
// One command line is rarely one program. A pipeline, an `&&` chain, a
// subshell, a `sudo` or an `xargs` each nest invocations inside a single
// string, and a rule about what an agent is about to run has to see all of
// them. This walks that structure once and emits every invocation it finds, so
// no rule has to recurse through shell syntax itself — and so nesting an
// invocation one level deeper does not defeat a rule written against it.
//
// Resolution has a floor. A program named by a variable, a payload decoded and
// piped to a shell, splitting that depends on the runtime IFS: none of these
// can be known without running them, and running them is exactly what a
// guardrail must not do. What can be seen is emitted; what cannot is left
// alone rather than guessed at. This is a correctness aid, never a security
// boundary.
package commandmod

import (
	"github.com/sloprail/sloprail/internal/grounding"
	"github.com/sloprail/sloprail/internal/module"
)

// Name identifies this module. It is how the engine reports which module
// produced an event, and how a module is switched off — not a prefix the kinds
// carry.
const Name = "command"

// KindPreInvoke is the one kind this module declares.
//
// One kind, not one per program or one per shell construct. What a rule asks
// is whether the agent is about to run something, and the answer is the same
// question whether the something sits in a pipeline or behind a sudo. The
// nesting is flattened into the event rather than encoded in its name.
//
// There is no Post counterpart. A file has a settled state a diff can
// establish afterwards; a command that already ran has no equivalent — what it
// changed shows up as the file module's Post events, which is where a rule
// about consequences belongs.
const KindPreInvoke = "PreCommandInvoke"

// Field names. They appear here, in Kinds below, and in the conversion in
// event.go — nowhere else, so a rename cannot leave a matcher checking against
// a name the events no longer carry.
const (
	FieldRaw         = "raw"
	FieldInvocations = "invocations"

	// Keys within one entry of FieldInvocations. Not fields of the kind: a
	// matcher reads them off an element of the list, and the declaration
	// describes the list itself.
	KeyBin   = "bin"
	KeyArgv  = "argv"
	KeyFlags = "flags"
	KeyCwd   = "cwd"
)

// Module produces command events.
type Module struct{}

// New returns the command module.
func New() *Module { return &Module{} }

// Name implements module.Module.
func (*Module) Name() string { return Name }

// Kinds implements module.Module.
//
// Both fields are carried on purpose. `invocations` is what a rule matches
// against — resolved, flattened, with the quoting already undone. `raw` is what
// a refusal quotes back, because telling an author that `npm` was refused is
// less use than showing them the line they actually wrote.
func (*Module) Kinds() []module.KindDecl {
	return []module.KindDecl{
		{
			Name: KindPreInvoke,
			Fields: []module.FieldDecl{
				{Name: FieldRaw, Type: module.TypeString},
				{
					Name: FieldInvocations,
					Type: module.TypeList,
					// The element's own shape, declared so that a name inside a
					// predicate is checked the same way a top-level one is.
					// Without it `any(invocations, .bni == "npm")` compiles,
					// loads, and evaluates false on every command — a rule that
					// reads as satisfied because nothing it names exists. The
					// typo is one character from a rule that works, and the
					// engine has the declaration needed to catch it.
					Elem: &module.FieldDecl{
						Type: module.TypeMap,
						Fields: []module.FieldDecl{
							{Name: KeyBin, Type: module.TypeString},

							// argv declares ITS element too, and for the same
							// reason one level further down. A list whose Elem
							// is nil has its collection checked and its
							// predicate body left at types.Any, so
							// `any(.argv, #.bin == "rm")` — reaching for the
							// invocation's field from inside a scope whose
							// elements are strings — compiled, loaded, and
							// returned false on every command line forever.
							//
							// This one CAN be enumerated, which is what makes
							// declaring it right rather than merely strict:
							// every entry of an argument vector is a string,
							// always, so the check refuses only what is
							// genuinely a mistake.
							{
								Name: KeyArgv,
								Type: module.TypeList,
								Elem: &module.FieldDecl{Type: module.TypeString},
							},

							// flags stays OPEN, and the asymmetry with argv is
							// the design. A flag name belongs to the command
							// being run, not to this module, so there is no
							// vocabulary to enumerate — a closed type would
							// refuse `.flags.access` because the engine has not
							// heard of npm, which is the checker punishing an
							// author for our missing knowledge rather than for
							// their mistake. See fieldType, which leaves an
							// unenumerated map's keys open for this reason.
							//
							// Its VALUES are declared, though: each is every
							// occurrence of that flag, a list of strings (see
							// Invocation.Flags). Undeclared, `.flags.tag ==
							// "next"` — the spelling from when a flag was one
							// string — loaded and could never match, a rule
							// failing open in silence; declared, the checker
							// refuses it when the rule loads.
							{
								Name: KeyFlags,
								Type: module.TypeMap,
								Elem: &module.FieldDecl{
									Type: module.TypeList,
									Elem: &module.FieldDecl{Type: module.TypeString},
								},
							},

							// cwd is where this program runs as far as the line
							// says: "." (where the line started), a path
							// relative to that, an absolute path, or "" when a
							// `cd` could not be resolved. See Invocation.Cwd.
							{Name: KeyCwd, Type: module.TypeString},
						},
					},
				},
				// The citations the line was grounded in — a
				// `sr-session trajectory cite '<quote>' && ...` chained ahead of
				// the command, or an sr-file --cite: flag. This module always
				// emits it empty: resolving a quote reads the session's record,
				// which is the session's business (services/sr-session).
				grounding.CitationsDecl(),
			},
		},
	}
}
