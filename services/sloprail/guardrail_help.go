package main

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/guardrail"
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

// newGuardrailHelpCmd prints what an agent needs in order to write a guardrail.
//
// A subcommand rather than a longer `--help`, because the reader is not the
// same reader. `--help` answers what a command takes, and someone reaching for
// it wants a flag; this answers what a declaration is, and the thing reading it
// is a model that arrives knowing none of the format. Putting the second inside
// the first makes the flag unfindable and the format easy to mistake for
// preamble.
//
// It is not a skill either. A skill teaches when a rule is worth writing at all
// and what makes one good. This teaches only the shape — everything below is
// checkable against the code, and nothing below is a judgement about which rules
// a project should have.
func newGuardrailHelpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "help",
		Short: "How to write a guardrail — the format, the event kinds, the matcher language",
		Long: `Print the format of a guardrail declaration.

Written for an agent about to author or fix one. Covers the file's shape, every
event kind and the fields it carries, what a hook is handed and what it may
write back, and what a matcher expression can say.

The event kinds are printed from the modules that declare them, so this is the
vocabulary this build actually has rather than a list written alongside it.`,
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
		helpWhereItLives,
		helpDeclarationShape,
		helpBody,
		helpModules,
		helpKinds,
		helpMatcher,
		helpHookContract,
		helpDisabling,
		helpWorkedExample,
	}
	for _, s := range sections {
		if err := s(w, reg); err != nil {
			return err
		}
	}
	return nil
}

func helpWhereItLives(w io.Writer, _ *module.Registry) error {
	_, err := fmt.Fprintf(w, `WHERE A GUARDRAIL LIVES

  %s/guardrails/<name>/GUARDRAIL.md

One folder per guardrail. The folder name IS the guardrail's name — it is not
repeated in the file, because a name recorded twice is a name that can disagree
with itself. It must be kebab-case: lower-case, starting with a letter.

Hook scripts sit beside the declaration in the same folder, and a hook's command
is resolved relative to it. `+"`sloprail init`"+` creates the guardrails directory;
it deliberately does not create an example rule.

`, DotDirName)
	return err
}

func helpDeclarationShape(w io.Writer, _ *module.Registry) error {
	_, err := fmt.Fprint(w, `THE DECLARATION

A GUARDRAIL.md is YAML frontmatter followed by a Markdown body. Both halves
matter — see THE BODY below.

  ---
  enabled: true            # optional, defaults to true
  hooks:                   # keyed by event kind
    PreFileCreate:
      - matcher: path startsWith "memories/"   # optional; absent means every occurrence
        hooks:
          - type: command
            command: ./check.sh
  ---

  # Prose body: what the rule is, and the rubric a judge hook reads.

`+"`hooks`"+` is a map from event kind to a LIST of bindings. Each binding narrows the
event with an optional matcher and names the hooks to run when it is admitted.

Two rules under one event are two entries in that list, not two keys. Repeating
a key is not an error YAML reports — the second silently replaces the first, and
the guardrail would enforce half of what it appears to.

One guardrail may bind to several kinds and reuse the same script across them. A
rule that must hold before a file is written and again once the cycle ends is
declared once, not twice.

`)
	return err
}

func helpBody(w io.Writer, _ *module.Registry) error {
	_, err := fmt.Fprint(w, `THE BODY

The prose under the frontmatter is not documentation of the rule sitting next to
the rule. It is read by judge hooks as their rubric — the criteria a model is
asked to judge against. Write it as the standard being applied, not as a note
about it: say what passes and what fails, in the words you would want a judge to
weigh.

It reaches a hook byte for byte. Nothing normalises it, because normalising it
would change what a judge is judging against.

A hook is handed its guardrail's folder on stdin (`+"`guardrailDir`"+`), which is how
it reads this body.

`)
	return err
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

The keys under `+"`hooks`"+`, and the variables a matcher may read. These are printed
from the modules that declare them, so this is what this build can actually
produce.

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

	_, err := fmt.Fprint(w, `A kind's name carries its timing, and nothing else does. A `+"`Pre`"+` kind fires
before the action and can refuse it, which also makes it a prediction of what a
tool call is about to do. A `+"`Post`"+` kind reports what a cycle turned out to have
done, established by diff rather than by trusting what any action announced.

Bind only to a kind on this list. A kind no module declares is an event that
will never arrive, and nothing warns you: the rule sits in the project looking
enforced and never fires once. Spelling is the whole defence — the names are
case-sensitive.

`)
	return err
}

func helpMatcher(w io.Writer, _ *module.Registry) error {
	_, err := fmt.Fprint(w, `MATCHERS

A matcher is an expression over the event's OWN fields — the ones listed above
for that kind, and nothing else. It reaches nothing beyond the occurrence it was
handed: no filesystem, no environment, no other events. It must evaluate to a
boolean; one that does not is reported and its binding does not run.

Absent means every occurrence of the kind.

Check your field names against the list above before you write. A matcher
naming a field the kind does not carry is NOT currently caught — it evaluates
to false, so the binding silently never fires and the rule looks satisfied
every time. This is a known gap, not the intended behaviour, and it is the one
mistake here that costs nothing to make and gives no sign it was made.

Operators:

  startsWith     path startsWith "memories/"
  endsWith       path endsWith ".md"
  contains       path contains "/decisions/"
  matches        path matches "^docs/[0-9]+-"      # regular expression
  &&  ||  !      path startsWith "src/" && !(path endsWith "_test.go")
  ==  !=         path == "README.md"

Every kind this build declares carries string fields only, so the operators
above are the whole usable set. Operators for asking questions of a list or a
map exist in the expression language but have nothing to read yet — a matcher
using one either evaluates false or errors, and both leave a rule that never
fires. They will be worth documenting when a module declares a field of that
shape; until then EVENT KINDS above is the honest inventory.

THERE IS NO GLOB. Not an omission. A glob's semantics differ between tools
enough that a familiar-looking pattern would be familiar and subtly wrong —
whether `+"`*`"+` crosses a directory separator is not the same answer in two places
an author might have learned it. Write what you mean:

  want                          write
  ----                          -----
  everything under memories/    path startsWith "memories/"
  every markdown file           path endsWith ".md"
  markdown under memories/      path startsWith "memories/" && path endsWith ".md"
  a real pattern                path matches "^memories/[0-9]{8}_"

The language has no loops, so a matcher always terminates.

Narrowing is the matcher's job, not the hook's. A hook that re-checks whether
the event concerns it is re-implementing its own binding, and the two will
drift.

`)
	return err
}

func helpHookContract(w io.Writer, _ *module.Registry) error {
	_, err := fmt.Fprintf(w, `HOOKS

  hooks:
    - type: %[1]s
      command: ./check.sh some-argument

`+"`type: %[1]s`"+` is the only mechanism today; it is an enum rather than a fixed
shape so another can be added without breaking declarations already written. The
command runs through a shell, with the guardrail's own folder as its working
directory.

ON STDIN the hook receives one event as JSON — one, not a batch, because
deciding which of a batch a rule applied to is the matcher's work, already done:

  {"event":{"kind":"PreFileCreate","fields":{"path":"memories/x.md","content":"..."}},
   "guardrailDir":"/abs/path/to/%[2]s/guardrails/<name>"}

The event's fields are the ones listed under EVENT KINDS for that kind.
`+"`guardrailDir`"+` is how a hook reads the prose body sitting beside it. Which
guardrail this is, is not carried: the engine ran the hook and already knows.

WHAT IT WRITES BACK is its exit status, and stdout explaining it:

  exit 0                  permitted. Print nothing — silence is consent.
  exit non-zero           refused.

A non-zero exit is never consent. It refuses whatever the hook did or did not
write, and whether or not it managed to run at all — a hook that is not
executable, or whose interpreter is missing, refuses rather than letting the
work through.

On a refusal the engine looks for the reason in this order:

  1. `+"`reason`"+` from JSON on stdout:
     {"decision":"block","reason":"Writing <path> requires the 'x' skill ..."}
  2. plain text on stdout
  3. plain text on stderr — `+"`echo \"...\" >&2; exit 1`"+` is an ordinary way to
     refuse and is read as one
  4. failing all of that, a message naming the hook and its exit status

Write a reason anyway: the synthesised one can name what failed, but only the
hook knows what the agent should do instead. It is rendered into whatever shape
the harness expects and reaches the agent that has to act on it.

Address the reason to that agent and say what to do rather than what went wrong.
Do not name the guardrail in it — the engine adds that.

Hooks under one binding run in order, and the first refusal stops the rest: once
the work is refused, running the others would produce objections to work that is
not going to happen.

`, guardrail.HookCommand, DotDirName)
	return err
}

func helpDisabling(w io.Writer, _ *module.Registry) error {
	_, err := fmt.Fprint(w, `TURNING A RULE OFF

  ---
  enabled: false
  hooks:
    ...
  ---

Set `+"`enabled: false`"+`. Do not delete the folder.

Turning a rule off is a declaration, not a deletion: the reasoning that produced
the rule is in the body, and it is exactly what someone needs six months later
when deciding whether to switch it back on. A deleted guardrail leaves the next
person to rediscover both the rule and the argument against it.

A disabled guardrail is inert — its kinds are not even extracted, so it costs
nothing to leave in place.

`)
	return err
}

func helpWorkedExample(w io.Writer, _ *module.Registry) error {
	_, err := fmt.Fprint(w, `A COMPLETE EXAMPLE

  .sloprail/guardrails/require-skill-before-write/GUARDRAIL.md

  ---
  hooks:
    PreFileCreate:
      - matcher: path startsWith "memories/decisions/"
        hooks:
          - type: command
            command: ./require-skill.sh document-strategy
    PreFileUpdate:
      - matcher: path startsWith "memories/decisions/"
        hooks:
          - type: command
            command: ./require-skill.sh document-strategy
  ---

  # A decision is written by someone who has read how decisions are written

  Writing under memories/decisions/ without having loaded document-strategy
  produces a file shaped like a decision that is not one.

  ## Pass
  The session shows the skill invoked at some point before the write.

  ## Fail
  No such invocation, or one that came after.

  .sloprail/guardrails/require-skill-before-write/require-skill.sh

  #!/usr/bin/env bash
  set -uo pipefail
  path="$(jq -r '.event.fields.path')"
  # ... decide ...
  echo "{\"decision\":\"block\",\"reason\":\"Writing $path requires ...\"}"
  exit 1

Two kinds, one script: the rule is about writing rather than about creating, and
the two kinds carry the same field, so binding to both costs a line rather than
a second implementation.

AFTER WRITING ONE

  sloprail session start < /dev/null

That is the load check: it reads every declaration and names the ones that did
not parse. It is not a check that your rule FIRES — a mistyped kind or field
name loads perfectly well. Confirm firing by causing the event and seeing the
refusal.

Make the hook executable (chmod +x). One that is not refuses every event it is
bound to, with a message saying so — noisy rather than silent, but your rule is
not running until you fix it.

`)
	return err
}
