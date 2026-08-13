package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/module"
	"github.com/sloprail/sloprail/internal/module/modules"
)

// newGuardrailCmd groups what is about the declarations themselves rather than
// about a running session.
func newGuardrailCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "guardrail",
		Short: "The declarations a project holds its agents to",
	}
	cmd.AddCommand(newGuardrailHelpCmd())
	return cmd
}

// newGuardrailHelpCmd prints the event vocabulary this build can produce.
//
// It does NOT teach how to write a guardrail. A help command documents the
// command it belongs to, and the declaration format is not a command — there is
// nothing here to take a flag or an argument, so a reader arriving for the
// format is arriving at the wrong place. Authoring is taught by the
// authoring-guardrails skill, which the plugin ships.
//
// What remains is the one thing that could not live in a skill without going
// stale: the kinds and their fields are printed from the modules that declare
// them, so this is the vocabulary THIS BUILD has rather than a description of
// it written elsewhere. A skill is a file someone edits; this is derived, and
// the two cannot disagree because there is only one of them.
//
// A subcommand rather than a longer `--help` because the output is a per-build
// inventory that grows with the module list, and burying it in the flag help
// makes the flags unfindable.
func newGuardrailHelpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "help",
		Short: "The event kinds this build can produce, and the fields each carries",
		Long: `Print the event kinds this build can produce.

Every kind a guardrail may bind to, the fields each carries — which are the
variables a matcher may read — and the module that declares it.

Printed from the modules themselves, so this is the vocabulary this build
actually has rather than a list written alongside it. The kinds are per-build:
they cannot be guessed, and binding to one this does not name is a rule that
never fires.

To write a guardrail, use the authoring-guardrails skill.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			reg, err := modules.Registry()
			if err != nil {
				return err
			}
			return writeGuardrailHelp(cmd.OutOrStdout(), reg)
		},
	}
}

func writeGuardrailHelp(w io.Writer, reg *module.Registry) error {
	sections := []func(io.Writer, *module.Registry) error{
		helpModules,
		helpKinds,
		helpAuthoring,
	}
	for _, s := range sections {
		if err := s(w, reg); err != nil {
			return err
		}
	}
	return nil
}

// helpModules prints every module this build registered.
//
// EVENT KINDS below already attributes each kind to its owner, so for a module
// that declares kinds this repeats what is derivable. It exists for the module
// that declares NONE. Attribution-per-kind cannot mention such a module at all,
// which left it invisible from outside the binary: nothing reading this output
// could tell a build carrying a silent module from a build without it, and
// TestT003_05 — which reads exactly this output to check the binary against the
// declared list — was blind in the same spot for the same reason.
//
// It is worth an author's attention too. A module here with no kinds under
// EVENT KINDS produces no events, so it is either unfinished or misregistered,
// and either way nothing can be bound to it.
func helpModules(w io.Writer, reg *module.Registry) error {
	if _, err := fmt.Fprint(w, `MODULES IN THIS BUILD

Every module registered, whether or not it declares any kinds. A module listed
here with no kinds below produces no events — nothing can be bound to it.

`); err != nil {
		return err
	}
	for _, name := range reg.ModuleNames() {
		if _, err := fmt.Fprintf(w, "  (module: %s)\n", name); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(w)
	return err
}

// helpKinds prints the event vocabulary from the registry.
//
// The whole reason this command exists rather than a document: the modules
// declare their kinds and the fields each carries, so this is printed from the
// same declarations the loader checks a matcher against. A hand-written list
// would be a second copy, and — being the one an author reads — it would be the
// copy trusted when the two disagreed.
func helpKinds(w io.Writer, reg *module.Registry) error {
	if _, err := fmt.Fprint(w, `EVENT KINDS

The keys a guardrail's `+"`hooks`"+` map is keyed by, and the variables a matcher may
read. These are printed from the modules that declare them, so this is what this
build can actually produce.

`); err != nil {
		return err
	}

	for _, kind := range reg.DeclaredKinds() {
		decl, ok := reg.KindDeclFor(kind)
		if !ok {
			continue
		}
		owner, _ := reg.Lookup(kind)
		module := ""
		if owner != nil {
			module = "  (module: " + owner.Name() + ")"
		}
		if _, err := fmt.Fprintf(w, "  %s%s\n", kind, module); err != nil {
			return err
		}
		if len(decl.Fields) == 0 {
			if _, err := fmt.Fprint(w, "      (no fields)\n"); err != nil {
				return err
			}
		}
		for _, f := range decl.Fields {
			if _, err := fmt.Fprintf(w, "      %-12s %s\n", f.Name, f.Type); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}

	if _, err := fmt.Fprint(w, `A kind's name carries its timing, and nothing else does. A `+"`Pre`"+` kind fires
before the action and can refuse it, which also makes it a prediction of what a
tool call is about to do. A `+"`Post`"+` kind reports what a cycle turned out to have
done, established by diff rather than by trusting what any action announced.

Bind only to a kind on this list. A kind no module declares is an event that
will never arrive, and nothing warns you: the rule sits in the project looking
enforced and never fires once. Spelling is the whole defence — the names are
case-sensitive.

A matcher naming a field its kind does not carry IS caught: it is type-checked
against the fields above when the guardrail loads, and the binding is refused by
name. What is not caught is a key read off an element of a `+"`list`"+` field. The
check reaches exactly as deep as the declaration does, and a module that does
not say what its list holds leaves the predicate body unchecked — so a mistyped
key in there compiles, loads, and evaluates to false forever.

`); err != nil {
		return err
	}
	return helpUndispatched(w, reg)
}

// helpUndispatched names the declared kinds nothing dispatches yet.
//
// Being on the list above means a module declares the kind, which is what makes
// it bind and validate. It does not mean anything ever produces one. Only
// pre-tool dispatches today, and only with PhasePre, so every Post kind loads,
// validates, and never arrives — the exact silent no-op the rest of this output
// warns about, sitting inside the list an author picks from.
//
// This belongs here rather than in the skill for the same reason the kinds do:
// it is derived. Which kinds are stranded follows from the phases the commands
// actually pass, so it corrects itself the day a Post dispatch lands, where a
// sentence in a hand-written file would sit there going quietly out of date in
// whichever direction the code moved.
func helpUndispatched(w io.Writer, reg *module.Registry) error {
	var stranded []string
	for _, kind := range reg.DeclaredKinds() {
		if !dispatchedPhases[phaseOf(kind)] {
			stranded = append(stranded, kind)
		}
	}
	if len(stranded) == 0 {
		return nil
	}

	if _, err := fmt.Fprint(w, `NOT DISPATCHED YET, though declared and bindable:

`); err != nil {
		return err
	}
	for _, kind := range stranded {
		if _, err := fmt.Fprintf(w, "  %s\n", kind); err != nil {
			return err
		}
	}
	_, err := fmt.Fprint(w, `
These load, validate, and never arrive. The cycle-end hook point does not yet
produce them, so a rule bound to one is inert in the way that looks enforced.
Bind to a Pre kind, or do not write the rule yet.

`)
	return err
}

// dispatchedPhases is the set of phases some hook point actually extracts with.
//
// One entry, and that is the point: session pre-tool passes module.PhasePre and
// nothing passes module.PhasePost, so this is the honest set rather than the
// intended one. Adding the Post dispatch means adding PhasePost here, and the
// warning above disappears on its own.
var dispatchedPhases = map[string]bool{
	module.PhasePre: true,
}

// phaseOf reads a kind's timing off its name, which is where it lives — see the
// note above about the name carrying the timing and nothing else doing so.
func phaseOf(kind string) string {
	if strings.HasPrefix(kind, "Post") {
		return module.PhasePost
	}
	return module.PhasePre
}

// helpAuthoring points at the skill rather than teaching the format.
//
// Without this line the command is a vocabulary list with no way to find out
// what to do with it, and an agent that reached this output would invent a
// declaration shape. Naming where the format lives is about this command's own
// output; describing the format would not be.
func helpAuthoring(w io.Writer, _ *module.Registry) error {
	_, err := fmt.Fprint(w, `WRITING ONE

This is the vocabulary, not the format. The declaration's shape, the matcher
operators, the hook contract, and what makes a rule worth writing are taught by
the authoring-guardrails skill, which ships with the sloprail plugin.

`)
	return err
}
