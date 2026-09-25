package declaration

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/sloprail/sloprail/internal/guardrail"
	"github.com/sloprail/sloprail/internal/module"
)

// This file holds the per-nature validation: every exactly-one-of, at-least-one,
// required-field, valid-`on`-kind, compilable-`match`, and resolvable-`require`
// rule the spec states, each producing a Problem naming what is wrong when it
// does not hold.
//
// Validation is SPLIT from parsing (store.go) deliberately, the same way the old
// loader splits loadOne from Validate: parsing turns bytes into a struct and can
// fail only on unreadable YAML; validation asks whether the struct is a rule the
// engine could actually run, which needs the event vocabulary (a registry) and
// the set of sibling contexts. A caller with neither can still parse; a caller
// about to act validates.

// Env is what validation needs beyond the declaration itself: the event
// vocabulary, to know a kind's fields when compiling a trigger's `match` against
// its scope, and the set of context names a `require: [{context}]` may resolve
// to.
//
// Passed in rather than discovered here for the reason internal/guardrail passes
// its registry in: this package's job is to validate declarations, and which
// modules a build ships and which contexts a project declared are facts assembled
// elsewhere (modules.Registry, and the load of the context folder). A validator
// that reached for them would couple to how they are found.
type Env struct {
	// Registry is the module registry, for the field declarations a trigger's
	// `match` is compiled against. A gate/context trigger's `match` reads the
	// fired event's fields under `event`, and those fields are the kind's — so the
	// scope must be built from the kind's KindDecl, which the registry holds.
	Registry *module.Registry

	// Contexts is the set of declared context names, for resolving a
	// `require: [{context: X}]`. Built by the loader from the context folder
	// before any declaration is validated, because a prerequisite may name a
	// context declared in a different folder than the rule that requires it.
	Contexts map[string]bool
}

// ValidateFileGuard reports everything wrong with a file-guard, given the
// validation environment. An empty result means the guard can do what it says.
//
// The order mirrors the struct: the required `match` compiles against the file
// scope, `deletions` is one of its three values (or absent), each prerequisite
// is exactly-one-of and resolves, each check is exactly-one-of with a
// well-placed prepare. A file-guard has no `on` list (it binds to a file's
// state, not to events). It carries the SAME at-least-one rule
// a gate does — `require` and `checks` are each optional individually, but a
// guard with NEITHER would select a file and decide nothing, so that is refused
// (a file-guard whose whole enforcement is a `require:` precondition is
// meaningful without any checks — the engine evaluates `require` before any check
// and refuses the write when it is unmet — so forcing it to carry a pass-through
// check solely to satisfy the validator was pure boilerplate).
func ValidateFileGuard(g FileGuard, env Env) []Problem {
	var problems []Problem

	if strings.TrimSpace(g.Match) == "" {
		problems = append(problems, prob(ErrMissingField, "match",
			"a file-guard must say which files it covers"))
	} else if _, err := guardrail.CompileFileMatch(g.Match); err != nil {
		problems = append(problems, prob(ErrBadMatch, "match",
			"%s — a file-guard's match reads a file's own facts (path, markers, context)", oneLine(err.Error())))
	}

	// `deletions:` is a closed enum. An unknown value is refused, not read as
	// the default: `deletions: inlcude` quietly becoming `skip` would switch off
	// the very deletions the author wrote the key to catch.
	if !g.Deletions.valid() {
		problems = append(problems, prob(ErrBadValue, "deletions",
			"%q is not a deletions value — use one of %s (absent means %s)",
			string(g.Deletions), deletionsValueList(), DeletionsSkip))
	}

	problems = append(problems, validatePrerequisites(g.Require, env)...)
	problems = append(problems, validateChecks(g.Checks)...)

	// The at-least-one rule, identical to the gate's. Both are optional
	// individually; a guard with neither require nor checks is refused — it would
	// match a file and have nothing to say about it.
	if len(g.Require) == 0 && len(g.Checks) == 0 {
		problems = append(problems, prob(ErrAtLeastOne, "",
			"a file-guard must carry at least one of require or checks — one with neither would select a file and decide nothing"))
	}

	return problems
}

// ValidateGate reports everything wrong with a gate.
//
// A gate's distinctive rules: its `on` kinds must be GateEventKinds (with the
// PreFileWrite alias), each trigger's `match` compiles against the GATE scope for
// the kind it names, and it must carry AT LEAST ONE of require/checks — the one
// rule unique to gates, since a gate with neither would wake and do nothing.
func ValidateGate(g Gate, env Env) []Problem {
	var problems []Problem

	if len(g.On) == 0 {
		problems = append(problems, prob(ErrMissingField, "on",
			"a gate must name at least one event to wake on"))
	}
	for i, t := range g.On {
		problems = append(problems, validateGateTrigger(t, i, env)...)
	}

	problems = append(problems, validatePrerequisites(g.Require, env)...)
	problems = append(problems, validateChecks(g.Checks)...)

	// The one at-least-one rule. Both are optional individually; a gate with
	// neither is refused.
	if len(g.Require) == 0 && len(g.Checks) == 0 {
		problems = append(problems, prob(ErrAtLeastOne, "",
			"a gate must carry at least one of require or checks — one with neither would wake on an event and do nothing"))
	}

	return problems
}

// ValidateContext reports everything wrong with a context.
//
// A context's distinctive rules: its `on` kinds must be ContextEventKinds (with
// the PreFileWrite AND PostFileWrite aliases), each trigger's `match` compiles
// against the CONTEXT scope, and `enter` and `exit` are both required and
// non-empty (a context with no enter cannot activate; with no exit cannot decide
// it is done).
func ValidateContext(c Context, env Env) []Problem {
	var problems []Problem

	if len(c.On) == 0 {
		problems = append(problems, prob(ErrMissingField, "on",
			"a context must name at least one event to wake on"))
	}
	for i, t := range c.On {
		problems = append(problems, validateContextTrigger(t, i, env)...)
	}

	problems = append(problems, validatePrerequisites(c.Require, env)...)

	if strings.TrimSpace(c.Enter) == "" {
		problems = append(problems, prob(ErrMissingField, "enter",
			"a context must name an enter script — the stage that decides whether to activate and what its payload is"))
	}
	if strings.TrimSpace(c.Exit) == "" {
		problems = append(problems, prob(ErrMissingField, "exit",
			"a context must name an exit script — the stage consulted on a Stop that decides whether the context is done"))
	}

	return problems
}

// ValidateStructureGate reports everything wrong with the structure gate.
//
// Its rules: `allow` must be present (an absent allowlist denies everything
// within this gate's scope, which is worth refusing rather than treating as a
// deliberate empty allowlist); every allow/deny/scope entry is exactly-one-of
// glob/regex, with the pattern compiling; and a PLUGIN's structure gate must
// declare `scope` — unlike a project's own, which may leave it empty to mean
// the whole tree, a plugin shipping an unscoped structure gate would lock the
// whole consuming project's tree the moment it is installed, so that is refused
// here rather than discovered by every consumer the hard way.
func ValidateStructureGate(s StructureGate, _ Env) []Problem {
	var problems []Problem

	if s.Origin.FromPlugin() && !s.isScoped() {
		problems = append(problems, prob(ErrMissingField, "scope",
			"a structure gate shipped in a plugin must declare `scope` — the paths it owns — "+
				"or installing the plugin would lock the whole consuming project's tree"))
	}
	for i, e := range s.Scope {
		problems = append(problems, validateStructureEntry(e, fmt.Sprintf("scope %d", i))...)
	}

	if len(s.Allow) == 0 {
		problems = append(problems, prob(ErrMissingField, "allow",
			"a structure gate must allow at least one path — an empty allowlist denies everything within its scope"))
	}
	for i, e := range s.Allow {
		problems = append(problems, validateStructureEntry(e, fmt.Sprintf("allow %d", i))...)
	}
	for i, e := range s.Deny {
		problems = append(problems, validateStructureEntry(e, fmt.Sprintf("deny %d", i))...)
	}

	return problems
}

// validateGateTrigger checks one gate trigger: its `event` is a valid gate kind
// or alias, and its `match` compiles against the gate scope for each concrete
// kind the event resolves to.
func validateGateTrigger(t GateTrigger, i int, env Env) []Problem {
	where := fmt.Sprintf("on %d", i)
	if strings.TrimSpace(t.Event) == "" {
		return []Problem{prob(ErrMissingField, where, "a trigger must name an event")}
	}

	kinds, known := expandGateEvent(t.Event)
	if !known {
		return []Problem{prob(ErrUnknownEventKind, where,
			"a gate does not wake on %q — a gate admits %s (Post events are too late to gate)",
			t.Event, strings.Join(gateEventNames(), ", "))}
	}

	return compileTriggerMatch(t.Match, where, kinds, env, guardrail.CompileGateMatch)
}

// validateContextTrigger checks one context trigger the same way, against the
// context vocabulary and the context scope.
func validateContextTrigger(t ContextTrigger, i int, env Env) []Problem {
	where := fmt.Sprintf("on %d", i)
	if strings.TrimSpace(t.Event) == "" {
		return []Problem{prob(ErrMissingField, where, "a trigger must name an event")}
	}

	kinds, known := expandContextEvent(t.Event)
	if !known {
		return []Problem{prob(ErrUnknownEventKind, where,
			"a context does not wake on %q — a context admits %s (Stop is never a context entry event; a Stop is where exit runs)",
			t.Event, strings.Join(contextEventNames(), ", "))}
	}

	return compileTriggerMatch(t.Match, where, kinds, env, guardrail.CompileContextMatch)
}

// compileTriggerMatch compiles a trigger's `match` against its nature's scope for
// EVERY concrete kind the trigger's event resolved to, and returns a problem for
// the first kind whose scope refuses it.
//
// Compiling against every expanded kind, not just the first, is what makes an
// alias honest: `PreFileWrite` expands to create + update, and a `match` reading
// a field one of them carries and the other does not must be caught. In practice
// the two share their fields, but the loop is what guarantees the alias cannot
// smuggle in a match that is valid for one half and not the other.
//
// An empty match compiles trivially (every occurrence), which the scope compilers
// handle — so a trigger with no `match` produces no problem here.
//
// The compiler is passed in (CompileGateMatch or CompileContextMatch) rather than
// branched on, because the two differ only in which scope they build and the
// caller already knows which nature it is validating.
func compileTriggerMatch(
	match, where string,
	kinds []string,
	env Env,
	compile func(string, module.KindDecl) (*guardrail.Matcher, error),
) []Problem {
	if strings.TrimSpace(match) == "" {
		return nil
	}
	// Without a registry there is no kind declaration to compile the scope
	// against — the same position internal/guardrail's Load (as opposed to
	// LoadWith) is in. A caller that wants trigger matches checked supplies one;
	// one that only wants the declaration parsed does not, and the match is left
	// unchecked rather than refused on the engine's own missing vocabulary.
	if env.Registry == nil {
		return nil
	}
	for _, kind := range kinds {
		decl, known := env.Registry.KindDeclFor(kind)
		if !known {
			// The kind is in the nature's static vocabulary but no module in this
			// build produces it. That is a build gap, not a declaration fault —
			// the same reasoning the old loader applies to an unknown kind is
			// inverted here because the nature-level check already passed, so this
			// can only mean a module that should declare the kind is missing. Skip
			// rather than blame the author; the affected-kinds machinery reports
			// the gap elsewhere. (Every kind in gate/contextEventKinds IS declared
			// by a shipped module, so this branch is defensive.)
			continue
		}
		if _, err := compile(match, decl); err != nil {
			return []Problem{prob(ErrBadMatch, where,
				"%s — a trigger's match reads the fired event's fields under `event`, and %s carries %s",
				oneLine(err.Error()), kind, fieldList(decl))}
		}
	}
	return nil
}

// validatePrerequisites checks a require list: each entry sets exactly one of
// skill/context, and a named context resolves against the declared ones.
func validatePrerequisites(reqs []Prerequisite, env Env) []Problem {
	var problems []Problem
	for i, r := range reqs {
		where := fmt.Sprintf("require %d", i)
		switch {
		case r.isEmpty():
			problems = append(problems, prob(ErrExactlyOne, where,
				"a prerequisite must set exactly one of skill or context, but sets neither"))
		case r.bothSet():
			problems = append(problems, prob(ErrExactlyOne, where,
				"a prerequisite must set exactly one of skill or context, but sets both"))
		case r.Context != "":
			// A context prerequisite must name a context that exists — the engine
			// orders against it, and cannot order against a name nothing declares.
			// A skill prerequisite is NOT checked: skills are read from the
			// trajectory at runtime, and the loader has no list of them.
			if env.Contexts != nil && !env.Contexts[r.Context] {
				problems = append(problems, prob(ErrUnknownContext, where,
					"names context %q, but no context declaration defines it — %s",
					r.Context, availableContexts(env.Contexts)))
			}
		}
	}
	return problems
}

// validateChecks checks a checks list: each is exactly-one-of script/judge, and
// a prepare appears only alongside a judge.
func validateChecks(checks []Check) []Problem {
	var problems []Problem
	for i, c := range checks {
		where := fmt.Sprintf("check %d", i)
		switch {
		case !c.isScript() && !c.isJudge():
			problems = append(problems, prob(ErrExactlyOne, where,
				"a check must set exactly one of script or judge, but sets neither"))
		case c.isScript() && c.isJudge():
			problems = append(problems, prob(ErrExactlyOne, where,
				"a check must set exactly one of script or judge, but sets both"))
		}
		// A prepare adds to a judge's prompt, so it is only meaningful with a
		// judge. On a script-only check it can only be a mistake — refused rather
		// than ignored, so the author learns the prepare they wrote does nothing.
		if c.hasPrepare() && !c.isJudge() {
			problems = append(problems, prob(ErrStrayPrepare, where,
				"sets prepare without a judge — prepare adds to a judge's prompt, and a script check has nothing to prepare for"))
		}
		problems = append(problems, validateJudgeTuning(c, where)...)
	}
	return problems
}

// validateJudgeTuning checks a check's judge-only tuning fields — `model`,
// `timeout` and `allowed_tools`: they are meaningful ONLY on a judge (a script
// makes no model call, bounds its own runtime, and names its own tools by being
// an executable), and when present on a judge the model is a well-formed modelset,
// the timeout parses to a positive duration, and every allowed-tools entry is
// non-empty.
//
// The stray-on-script rules mirror ErrStrayPrepare exactly — a judge-only field on
// a script-only check can only be a mistake, and is refused rather than ignored so
// the author learns the field does nothing. The format checks are done here too so
// a value that would fail at the judge is caught at load, the same place a bad
// match or a bad glob is.
func validateJudgeTuning(c Check, where string) []Problem {
	var problems []Problem

	// Stray on a script check: refuse. model/timeout share one sentinel (they are
	// the one "tunes a model call" idea); allowed_tools gets its own, because its
	// fix names a different field. As with the model/timeout pair, a stray field is
	// not also format-checked — its misplacement is the finding, not its shape.
	if (c.hasModel() || c.hasTimeout()) && !c.isJudge() {
		problems = append(problems, prob(ErrStrayModel, where,
			"sets model/timeout without a judge — both tune a model call, and a script check makes none (its runtime is the author's to bound)"))
	}
	if c.hasAllowedTools() && !c.isJudge() {
		problems = append(problems, prob(ErrStrayAllowedTools, where,
			"sets allowed_tools without a judge — it grants tools to a judge's agent, and a script check names its own tools by being an executable"))
	}
	if !c.isJudge() {
		return problems
	}

	if c.hasModel() {
		if err := validateModelSet(c.Model); err != nil {
			problems = append(problems, prob(ErrBadModel, where,
				"model %q is not a well-formed modelset: %s", c.Model, err.Error()))
		}
	}
	if c.hasTimeout() {
		if err := validateTimeout(c.Timeout); err != nil {
			problems = append(problems, prob(ErrBadTimeout, where,
				"timeout %q %s", c.Timeout, err.Error()))
		}
	}
	if c.hasAllowedTools() {
		if err := validateAllowedTools(c.AllowedTools); err != nil {
			problems = append(problems, prob(ErrBadAllowedTools, where,
				"allowed_tools %s", err.Error()))
		}
	}
	return problems
}

// validateAllowedTools checks a judge's allowed-tools list carries no empty
// entry — a blank tool name would reach sr-agent as an empty `--allowed-tools`
// argument, which names no tool and can only be a stray or trailing list item.
// Mirrors validateModelSet's empty-entry refusal.
func validateAllowedTools(tools []string) error {
	for i, t := range tools {
		if strings.TrimSpace(t) == "" {
			return fmt.Errorf("entry %d is empty (a blank tool name grants nothing)", i+1)
		}
	}
	return nil
}

// validateModelSet checks a judge model is a well-formed modelset, mirroring
// what sr-agent's ParseModelSet refuses: a non-empty set whose every
// comma-separated entry is non-empty. Classification (alias vs concrete) is not
// checked because a concrete entry is any non-empty token — the harness decides
// whether it has such a model, exactly as sr-agent leaves it. Kept as its own
// small function rather than importing sr-agent (a main package) so the loader
// has no dependency on the binary, only on its documented format.
func validateModelSet(set string) error {
	if strings.TrimSpace(set) == "" {
		return fmt.Errorf("it is empty — a modelset needs at least one entry (a size alias like size-md, or a model name)")
	}
	for i, part := range strings.Split(set, ",") {
		if strings.TrimSpace(part) == "" {
			return fmt.Errorf("entry %d is empty (a stray or trailing comma)", i+1)
		}
	}
	return nil
}

// validateTimeout checks a judge timeout parses as a Go duration and is > 0.
// Returns the trailing half of the complaint sentence so the caller can prefix
// it with the offending value.
func validateTimeout(timeout string) error {
	d, err := time.ParseDuration(strings.TrimSpace(timeout))
	if err != nil {
		return fmt.Errorf("is not a Go duration string (e.g. 45s, 2m, 1m30s)")
	}
	if d <= 0 {
		return fmt.Errorf("must be greater than zero — a timeout that never fires is not a timeout")
	}
	return nil
}

// validateStructureEntry checks one allow/deny entry: exactly one of glob/regex,
// with the pattern actually compiling so a malformed one is refused at load
// rather than at the moment it should have matched.
func validateStructureEntry(e StructureEntry, where string) []Problem {
	switch {
	case !e.isGlob() && !e.isRegex():
		return []Problem{prob(ErrExactlyOne, where,
			"a structure entry must set exactly one of glob or regex, but sets neither")}
	case e.isGlob() && e.isRegex():
		return []Problem{prob(ErrExactlyOne, where,
			"a structure entry must set exactly one of glob or regex, but sets both")}
	case e.isGlob():
		// A glob is validated by compiling it the same way a file-guard's bare
		// glob is — CompileFileMatch routes a whitespace-and-quote-free string to
		// the glob compiler, so a malformed glob (an unterminated class) is refused
		// here with the glob compiler's own error.
		if _, err := guardrail.CompileFileMatch(e.Glob); err != nil {
			return []Problem{prob(ErrBadMatch, where, "glob does not compile: %s", oneLine(err.Error()))}
		}
	case e.isRegex():
		if err := compileRegex(e.Regex); err != nil {
			return []Problem{prob(ErrBadMatch, where, "regex does not compile: %s", oneLine(err.Error()))}
		}
	}
	return nil
}

// fieldList names what a kind carries, so a refused trigger match says what it
// could have read instead — the same courtesy the old validator's fieldList
// extends, adapted to the `event.`-nested spelling by leaving the bare names (the
// author writes `event.path`, and "path (string)" is enough to spot a typo).
func fieldList(decl module.KindDecl) string {
	if len(decl.Fields) == 0 {
		return "no fields"
	}
	names := make([]string, 0, len(decl.Fields))
	for _, f := range decl.Fields {
		names = append(names, fmt.Sprintf("%s (%s)", f.Name, f.Type))
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// deletionsValueList renders the admitted `deletions:` values for a diagnostic.
func deletionsValueList() string {
	out := make([]string, 0, len(deletionsValues))
	for _, v := range deletionsValues {
		out = append(out, string(v))
	}
	return strings.Join(out, ", ")
}

// availableContexts names the declared contexts, so an author who mistyped one in
// a prerequisite can see the one they meant.
func availableContexts(contexts map[string]bool) string {
	if len(contexts) == 0 {
		return "this project declares no contexts"
	}
	names := make([]string, 0, len(contexts))
	for c := range contexts {
		names = append(names, c)
	}
	sort.Strings(names)
	return "declared contexts are " + strings.Join(names, ", ")
}
