// Package declaration reads the rule declarations a project keeps under its
// `.sloprail/` directory into typed, validated Go values.
//
// It is the NEW-format counterpart to internal/guardrail's old-format loader.
// Where internal/guardrail reads one `guardrails/<name>/GUARDRAIL.md` per rule —
// frontmatter keyed by raw event kind, prose folded into a judge's rubric — this
// package reads the declaration formats the current spec defines that the engine
// actually loads and dispatches on (dot-dir-file-store/main.tsp):
//
//   - file-guard/<name>/file-guard.yaml  → FileGuard      (a rule on a file's state)
//   - gate/<name>/gate.yaml              → Gate           (a checkpoint on an event)
//   - context/<name>/context.yaml        → Context        (an activatable scope)
//   - file-guard/structure.yaml          → StructureGate  (one tree-wide allowlist)
//
// A GOAL is deliberately NOT among them. A goal is a higher-level COMPOSITE — a
// project-level `goal/<name>/goal.yaml` (a sibling of `.sloprail/`, maintained by
// the user) paired with an ordinary Context whose `enter` reads it. The engine
// neither loads nor dispatches on the goal file; it is not a sloprail primitive,
// so it has no type, no Nature, and no place in the loader here.
//
// # What this slice does and does not do
//
// It LOADS and VALIDATES. Given a `.sloprail` root it produces the in-memory
// representation the future dispatch slice consumes: every declaration parsed,
// every exactly-one-of / at-least-one / required-field constraint checked, every
// `match` and trigger `match` COMPILED against the right nature's scope (so a
// bad expression is a load error naming the problem, not a rule that silently
// never fires), every `on:` kind checked against the kinds its nature admits,
// and every `require` reference resolved (an unknown skill is allowed — it is a
// runtime fact — but a context prerequisite naming a context that does not exist
// is a configuration error caught here). It does NOT match events against these
// declarations or run their checks; that is the next batch.
//
// # Why a package of its own rather than extending internal/natures
//
// internal/natures holds the runtime STATE maps a rule reads about the world —
// ContextState, GateState — the `{active, payload}` and `{status}` a matcher
// sees. Those are facts the engine writes and reads across a process boundary.
// The DECLARATIONS here are a different thing: the authored rule as it sits on
// disk, the shape a loader parses and a validator refuses. Keeping them apart
// keeps internal/natures free of the loader's dependencies (this package imports
// internal/guardrail for its scope compilers and internal/module for the event
// vocabulary; internal/natures imports neither), and lets a reader find "the
// state a rule reads" and "the rule a project declared" in two named places.
//
// This package REUSES rather than reinvents. The runtime state shapes are
// internal/natures'; the match compilers (CompileFileMatch / CompileGateMatch /
// CompileContextMatch) and the expression evaluator underneath them are
// internal/guardrail's; the event vocabulary and its field declarations are
// internal/module's. What this package adds is the declaration types, the
// per-nature event-kind rules, the alias expansion, and the loaders that tie
// them together with load-time diagnostics.
package declaration

// Nature names which of the rule natures a declaration is, so a loaded
// declaration says what it is without a caller having to type-switch on the
// concrete struct. The three file-backed natures plus the structure primitive
// each have one. A goal has none: it is a composite realised on the user's side
// (see the package doc), not a thing this loader reads.
type Nature string

const (
	// NatureFileGuard is a rule bound to a file's settled state.
	NatureFileGuard Nature = "file-guard"

	// NatureGate is a checkpoint on a pre-action event.
	NatureGate Nature = "gate"

	// NatureContext is an activatable scope with a lifecycle.
	NatureContext Nature = "context"

	// NatureStructure is the tree-wide structure gate — one path allowlist for
	// the whole project, deny by default. A primitive, not a fourth nature; it
	// has no per-name folder and is loaded from the singleton
	// `file-guard/structure.yaml`.
	NatureStructure Nature = "structure"
)

// FileGuard is a file-guard declaration: a rule bound to a FILE'S STATE, not to
// an event (dot-dir-file-store/main.tsp FileGuardDeclaration). A guarded file
// must be FINE, and the check is the same whether the file was created or
// modified — a failing guard does not reject once; the file stays not-fine and
// the guard keeps failing every cycle until its content satisfies the checks.
//
// Stored at `file-guard/<name>/file-guard.yaml`, with the scripts and judge
// prompts it names living as siblings in the same folder.
type FileGuard struct {
	// Name is the guard's name and its folder's name. Taken from the folder
	// rather than the file, the same reasoning the old loader records: a name
	// recorded twice is a name that can disagree with itself. Not a YAML field.
	Name string `yaml:"-"`

	// Match is which files this guard covers, and under which contexts. A
	// FileMatchExpression: either a bare glob (the common "this path" case) or a
	// full expression over a file's own facts — its path, the markers it carries,
	// and `context[<name>]`. Compiled against the file scope at load, so a bad
	// expression is refused with a diagnostic naming it rather than silently
	// never matching.
	Match string `yaml:"match"`

	// Preventive asks the engine to also try to prevent the file from becoming
	// not-fine BEFORE the write lands, in addition to the always-available check
	// after. Best-effort by nature (the engine cannot always predict a write),
	// which is why it is opt-in. A boolean, not an array of events: the only two
	// cases are "fine at the end" (the default) and "always fine" (checked before
	// too), and they nest.
	Preventive bool `yaml:"preventive"`

	// Require are preconditions that must hold before a guarded write is
	// permitted — see Prerequisite. Optional.
	Require []Prerequisite `yaml:"require"`

	// Checks are the checks a guarded file must pass, in order. Each is a script
	// or a judge (both may appear across the list), and order is significant: a
	// cheap deterministic script runs first and can settle the matter before a
	// judge is paid for. The first check that refuses ends it.
	Checks []Check `yaml:"checks"`

	// Dir is the guard's own folder, so its scripts and judge prompts resolve
	// relative to it. Not a YAML field — the loader fills it from where the file
	// was found.
	Dir string `yaml:"-"`
}

// Gate is a gate declaration: a checkpoint on an event
// (dot-dir-file-store/main.tsp GateDeclaration). It wakes on a pre-action event,
// evaluates its Require and Checks, and passes or blocks the action. One-shot —
// it decides at the moment and is done, unlike a file-guard that re-fires until a
// file is fine.
//
// Stored at `gate/<name>/gate.yaml`.
type Gate struct {
	// Name is the gate's name and its folder's name. Not a YAML field.
	Name string `yaml:"-"`

	// On are the events this gate wakes on — pre-action only (GateEventKind). A
	// trigger's `event` may be the alias `PreFileWrite`, which the loader expands
	// to PreFileCreate + PreFileUpdate. Each trigger's optional `match` is
	// compiled against the gate scope for the kind it names.
	On []GateTrigger `yaml:"on"`

	// Require are preconditions that must hold before the gated action is
	// permitted — the same Prerequisite a file-guard uses. Optional, BUT a gate
	// must carry at least one of Require / Checks (a gate with neither would wake
	// and do nothing).
	Require []Prerequisite `yaml:"require"`

	// Checks are the checks the gated action must pass, in order — the same Check
	// shape a file-guard uses. Optional, subject to the same at-least-one rule as
	// Require.
	Checks []Check `yaml:"checks"`

	// Dir is the gate's own folder. Not a YAML field.
	Dir string `yaml:"-"`
}

// Context is a context declaration: an activatable scope with a lifecycle, the
// third rule nature (dot-dir-file-store/main.tsp ContextDeclaration). Where a
// file-guard hangs on a file's state, a context hangs on nothing until it is
// entered — then it is a scope the agent is inside, contributing an entry in the
// `context` map that guards naming it can read. Purely lifecycle tracking: a
// context does NOT block a Stop itself; a gate bound to Stop does.
//
// Stored at `context/<name>/context.yaml`.
type Context struct {
	// Name is the context's name, its folder's name, and its key in the `context`
	// map. Not a YAML field.
	Name string `yaml:"-"`

	// On are the events that can (re-)run `enter` — the cheap first stage. Wider
	// than a gate's (ContextEventKind): a context may wake on the Post file
	// events and PostTagWrite, because it sometimes must recognise itself from a
	// file's SETTLED content. A trigger's `event` may be the alias `PreFileWrite`
	// or `PostFileWrite`, expanded to the matching Create + Update pair.
	On []ContextTrigger `yaml:"on"`

	// Require are preconditions that must hold before `enter` runs — see
	// Prerequisite. Optional.
	Require []Prerequisite `yaml:"require"`

	// Enter is the script that decides whether to (re-)activate and what the
	// context's payload should be. Runs on EVERY `on` trigger, active or not.
	// Required and non-empty.
	Enter string `yaml:"enter"`

	// Exit is the script that decides whether the context is done, consulted on a
	// Stop while active. Its verdict only flips this context's own `active`; it
	// does not refuse the Stop. Required and non-empty.
	Exit string `yaml:"exit"`

	// Dir is the context's own folder. Not a YAML field.
	Dir string `yaml:"-"`
}

// StructureGate is the tree-wide structure gate (dot-dir-file-store/main.tsp
// StructureGateDeclaration): an allowlist of paths a project may write under,
// deny by default. `deny` only carves exceptions out of `allow` — a `deny` entry
// matching nothing in `allow` is a no-op. A primitive, not a rule nature, and a
// singleton: one file for the whole project, `file-guard/structure.yaml`, sibling
// of the per-guard subfolders.
type StructureGate struct {
	// Allow is the allowlist. Each entry is a glob or a regex (exactly one set).
	Allow []StructureEntry `yaml:"allow"`

	// Deny carves exceptions out of Allow. Optional. Each entry is a glob or a
	// regex (exactly one set).
	Deny []StructureEntry `yaml:"deny"`

	// Dir is the folder the structure.yaml sits in — `.sloprail/file-guard`. Not
	// a YAML field.
	Dir string `yaml:"-"`
}

// Prerequisite is a precondition that must hold before a rule's own check runs
// (dot-dir-file-store/main.tsp Prerequisite). A single list carrying two kinds,
// exactly one field set per entry — like Check's script/judge. What differs
// between them is not the shape but WHO establishes it and WHEN a violation is
// discovered:
//
//   - Skill is read from the trajectory at the moment the check runs — a fact the
//     agent may or may not have produced, discovered at runtime and refused with
//     a remedy the agent can act on ("load the skill"). An unknown skill NAME is
//     therefore NOT a load error: the loader has no list of skills to check
//     against, and a skill that does not exist yet is a runtime miss, not a
//     malformed rule.
//   - Context names another context declaration the engine must run first when
//     both match the same event — a guarantee the engine gives by ordering, not a
//     fact it goes looking for. Naming one that cannot resolve (unknown name) IS
//     a configuration error caught when the rule loads.
//
// The spec's Prerequisite carries no `gate` field yet — dropped deliberately, no
// gate-depends-on-gate case exists among the units built so far.
type Prerequisite struct {
	// Skill is the name of a skill that must have been loaded (a real Skill
	// tool_use in the trajectory, not a claim) before the write. Optional.
	Skill string `yaml:"skill"`

	// Context is the name of a context declaration that must run first, when both
	// this rule and that context wake on the same event in the same cycle.
	// Optional. Validated to resolve against the loaded contexts.
	Context string `yaml:"context"`
}

// isEmpty reports whether this prerequisite sets neither field — a list entry
// that establishes nothing, which the exactly-one-of check refuses.
func (p Prerequisite) isEmpty() bool { return p.Skill == "" && p.Context == "" }

// bothSet reports whether this prerequisite sets both fields — the other half of
// the exactly-one-of check.
func (p Prerequisite) bothSet() bool { return p.Skill != "" && p.Context != "" }

// Check is one check in a rule's ordered list (dot-dir-file-store/main.tsp
// Check). Exactly one of Script or Judge is set — a rule needing both
// deterministic and model judgement lists two checks. Prepare is optional and
// only meaningful alongside Judge: it runs before the judge and adds to what the
// prompt template sees.
//
// Both Script and Prepare receive the nature's CheckPayload on stdin; a Judge's
// prompt renders against the nature's judge-input shape (FileJudgeInput on a
// file-guard, GateJudgeInput on a gate). See payload.go for those shapes.
type Check struct {
	// Script is a deterministic executable, resolved relative to the rule's
	// folder. Receives the payload on stdin and exits with a pass/fail code.
	// Exactly one of Script / Judge is set.
	Script string `yaml:"script"`

	// Judge is a model judgement, a Jinja2 prompt template (`.md.j2`) resolved
	// relative to the rule's folder. Exactly one of Script / Judge is set.
	Judge string `yaml:"judge"`

	// Prepare is an optional deterministic executable that runs before Judge and
	// adds to the prompt's variables under `additionalContext`. Only meaningful
	// alongside a Judge; a Prepare set on a script-only check is a mistake the
	// validator refuses, since a script check has nothing to prepare for.
	Prepare string `yaml:"prepare"`
}

// isScript reports whether this check is the script half of the union.
func (c Check) isScript() bool { return c.Script != "" }

// isJudge reports whether this check is the judge half of the union.
func (c Check) isJudge() bool { return c.Judge != "" }

// hasPrepare reports whether this check names a prepare script.
func (c Check) hasPrepare() bool { return c.Prepare != "" }

// StructureEntry is one entry in structure.yaml's allow/deny lists
// (dot-dir-file-store/main.tsp StructureEntry). Exactly one of Glob / Regex is
// set — a model with two optional fields in XOR, the same shape as Check's
// script/judge, tagged by key rather than by a gitignore-style string prefix.
type StructureEntry struct {
	// Glob is a bare glob pattern. Exactly one of Glob / Regex is set.
	Glob string `yaml:"glob"`

	// Regex is a full regex pattern, for the cases a glob cannot express (a
	// dated-folder shape, say). Exactly one of Glob / Regex is set.
	Regex string `yaml:"regex"`
}

// isGlob reports whether this entry is the glob half of the union.
func (e StructureEntry) isGlob() bool { return e.Glob != "" }

// isRegex reports whether this entry is the regex half of the union.
func (e StructureEntry) isRegex() bool { return e.Regex != "" }

// -- the trigger types follow --

// GateTrigger is one event a gate wakes on, optionally narrowed by a cheap
// expression over that event's own fields (dot-dir-file-store/main.tsp
// GateTrigger). Typed to GateEventKind — the pre-action subset, narrower than a
// context's (no Post events; a gate that already happened is too late to gate).
type GateTrigger struct {
	// Event is the event kind. May be the config-level alias `PreFileWrite`,
	// which the loader expands to PreFileCreate + PreFileUpdate. No `PostFileWrite`
	// alias here — a gate does not wake on settled content.
	Event string `yaml:"event"`

	// Match is an optional expression over the event's fields; absent means every
	// occurrence of the kind. Scoped to the gate scope for whichever kind Event
	// names, so the event's fields are read under `event` (`event.path`,
	// `any(event.invocations, …)`).
	Match string `yaml:"match"`
}

// ContextTrigger is one event a context wakes on to enter, optionally narrowed
// by a cheap expression (dot-dir-file-store/main.tsp ContextTrigger). Wider than
// a gate's: `event` is typed to ContextEventKind, which includes the Post file
// events and PostTagWrite, because a context sometimes must recognise itself from
// a file's SETTLED content.
type ContextTrigger struct {
	// Event is the event kind. May be the config-level alias `PreFileWrite` or
	// `PostFileWrite`, each expanded to the matching Create + Update pair on load.
	Event string `yaml:"event"`

	// Match is an optional expression over the event's fields; absent means every
	// occurrence. Scoped to the context scope for whichever kind Event names — the
	// event's fields under `event` (`any(event.tags, …)`, `event.path startsWith …`).
	Match string `yaml:"match"`
}
