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
//   - file-guard/structure.yaml          → StructureGate  (a path allowlist per root)
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

import "path/filepath"

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

	// NatureStructure is the structure gate — a path allowlist, deny by
	// default. A primitive, not a fourth nature; it has no per-name folder and is
	// loaded from each root's one `file-guard/structure.yaml`: the project's
	// covers the whole tree, each plugin's covers only the `scope` it declares,
	// and all of them combine (see StructureGate).
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

	// HadPreventiveKey records that the RETIRED `preventive:` key was present,
	// whatever its value. A file-guard judges only the settled result at Stop;
	// preventing a write is a gate's job (a PreFileWrite or PreFileDelete gate).
	// The field exists only so the loader can SEE the key and refuse the
	// declaration by name (ValidateFileGuard, ErrRetiredKey) — unknown keys are
	// otherwise tolerated, and a `preventive: true` quietly ignored would leave
	// the write it was written to stop unguarded. Nothing else reads it.
	HadPreventiveKey bool `yaml:"-"`

	// Deletions says whether a DELETED file is this guard's business: `skip`
	// (the default, and what an absent key means), `include`, or `only`. See
	// Deletions for the three values.
	//
	// One axis with three values, NOT a list of events — the same reasoning that
	// keeps a file-guard's other axes small. A file-guard binds to a
	// file's STATE, not to events; the one place the state question genuinely
	// forks is a file that no longer exists, which has no end state, no
	// newContent and no newMarkers. Most guards validate content and have nothing
	// to say about a file that is gone (so they skip it); a few exist precisely
	// to catch the loss (so they include it, or look at nothing else). An `on:`
	// list would reopen every create/update/delete combination a state rule has
	// no use for, so it is deliberately not offered.
	Deletions Deletions `yaml:"deletions"`

	// Require are preconditions that must hold before a guarded write is
	// permitted — see Prerequisite. Optional.
	Require []Prerequisite `yaml:"require"`

	// Subjects is an optional script, resolved from the rule's folder, that splits the
	// selected files into the units the rule is judged on, each cached on its own. Run with
	// the changeset payload on stdin and no session; prints a JSON array of
	// {"id", "files", "fingerprint"} (fingerprint optional: what that subject's verdict depends
	// on beyond its files' content). Absent: one subject made of every selected file.
	Subjects string `yaml:"subjects"`

	// Checks are the checks a guarded file must pass, in order. Each is a script
	// or a judge (both may appear across the list), and order is significant: a
	// cheap deterministic script runs first and can settle the matter before a
	// judge is paid for. The first check that refuses ends it.
	Checks []Check `yaml:"checks"`

	// Dir is the guard's own folder, so its scripts and judge prompts resolve
	// relative to it. Not a YAML field — the loader fills it from where the file
	// was found.
	Dir string `yaml:"-"`

	// Origin is where this guard was found — the project's own `.sloprail`, or a
	// plugin that ships it. Not a YAML field; the loader fills it from which root it
	// read the guard from, so a refusal can name the plugin a shipped guard came
	// from (see Origin). The zero value is a project's own guard.
	Origin Origin `yaml:"-"`
}

// Root is the `.sloprail` directory this guard was loaded from — the project's own
// or a plugin's: `<root>/file-guard/<name>`. The guard's rule hash does not read it: it
// covers the guard's own folder (g.Dir), the files git tracks there (changeset.RuleHashAt);
// the floor of its range is over the guard's folder too.
func (g FileGuard) Root() string { return filepath.Dir(filepath.Dir(g.Dir)) }

// Deletions is a file-guard's `deletions:` value — whether the guard is asked
// about a file that was deleted. The three values are the whole vocabulary; the
// loader refuses anything else (ErrBadValue), so a typo cannot quietly become
// the default.
type Deletions string

const (
	// DeletionsSkip: a deleted file is not this guard's business. It is not run
	// on PreFileDelete or PostFileDelete — only on creates and updates. The
	// DEFAULT, and what an absent key means: a guard that validates content has
	// nothing to validate once the content is gone, and asking it anyway hands
	// it an event with no newContent that each guard would otherwise have to
	// recognise and wave through on its own.
	DeletionsSkip Deletions = "skip"

	// DeletionsInclude: creates, updates AND deletes. For a guard whose rule
	// covers losing the file as well as changing it — "nothing under memories/
	// is removed without an ask", "an invariant-pinned file may not silently
	// disappear".
	DeletionsInclude Deletions = "include"

	// DeletionsOnly: deletes only. The guard is not run on creates or updates —
	// for a rule that exists purely to catch a file going away. On a delete a
	// check reads `oldContent` / `oldMarkers` (what is being or was lost); there
	// is no `newContent`.
	DeletionsOnly Deletions = "only"
)

// deletionsValues is the admitted vocabulary, in the order a diagnostic lists it.
var deletionsValues = []Deletions{DeletionsSkip, DeletionsInclude, DeletionsOnly}

// valid reports whether this is one of the admitted values — the empty string
// (an absent key, meaning the default) included.
func (d Deletions) valid() bool {
	if d == "" {
		return true
	}
	for _, v := range deletionsValues {
		if d == v {
			return true
		}
	}
	return false
}

// Mode is the effective value: the one written, or DeletionsSkip when the key
// was absent.
func (d Deletions) Mode() Deletions {
	if d == "" {
		return DeletionsSkip
	}
	return d
}

// IsFileDeleteKind reports whether an event kind is one of the two file-delete
// kinds, PreFileDelete or PostFileDelete.
func IsFileDeleteKind(kind string) bool {
	return kind == KindPreFileDelete || kind == KindPostFileDelete
}

// Covers reports whether this guard is asked about a file event of the given
// kind, as its `deletions:` value decides: a delete kind only when the guard
// includes deletions (include / only), and a create or update kind unless the
// guard is deletions-only. Any other kind is not a file event and is answered
// true — this filter has no opinion on it; whether a guard runs on it at all is
// the dispatch's to decide.
//
// The ONE place the filter is written, so the after-check (Post) path and the
// event-extraction binding cannot disagree about which events a guard sees.
func (g FileGuard) Covers(kind string) bool {
	mode := g.Deletions.Mode()
	if IsFileDeleteKind(kind) {
		return mode == DeletionsInclude || mode == DeletionsOnly
	}
	switch kind {
	case KindPreFileCreate, KindPreFileUpdate, KindPostFileCreate, KindPostFileUpdate:
		return mode != DeletionsOnly
	}
	return true
}

// Attribution is this guard's name as a refusal should carry it — the bare name
// for a project's own, and the name plus " from plugin X" for a shipped one, so
// the agent hears where a rule it cannot find in its tree actually lives. Mirrors
// guardrail.Declaration.Attribution.
func (g FileGuard) Attribution() string { return quoteName(g.Name) + g.Origin.Describe() }

// Qualified is this guard's disable key, `<plugin>/file-guard/<name>` for a
// shipped guard and `file-guard/<name>` for a project's own — what a consumer
// writes in `disabled:` to switch it off.
func (g FileGuard) Qualified() string { return g.Origin.Qualified(NatureFileGuard, g.Name) }

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

	// Enabled, when false, ships the gate OFF: it is inert until the project lists its
	// qualified name under `enabled:` in `.sloprail/config.yaml`. Absent means on.
	Enabled *bool `yaml:"enabled"`

	// Dir is the gate's own folder. Not a YAML field.
	Dir string `yaml:"-"`

	// Origin is where this gate was found — the project's own `.sloprail`, or a
	// plugin that ships it. Not a YAML field; the loader fills it. The zero value
	// is a project's own gate.
	Origin Origin `yaml:"-"`
}

// Attribution is this gate's name as a refusal should carry it — bare for a
// project's own, "…" plus " from plugin X" for a shipped one.
func (g Gate) Attribution() string { return quoteName(g.Name) + g.Origin.Describe() }

// Qualified is this gate's disable key, `<plugin>/gate/<name>` for a shipped gate
// and `gate/<name>` for a project's own.
func (g Gate) Qualified() string { return g.Origin.Qualified(NatureGate, g.Name) }

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

	// Origin is where this context was found — the project's own `.sloprail`, or a
	// plugin that ships it. Not a YAML field; the loader fills it. The zero value
	// is a project's own context.
	Origin Origin `yaml:"-"`
}

// Attribution is this context's name as a diagnostic should carry it — bare for a
// project's own, "…" plus " from plugin X" for a shipped one.
func (c Context) Attribution() string { return quoteName(c.Name) + c.Origin.Describe() }

// Qualified is this context's disable key, `<plugin>/context/<name>` for a shipped
// context and `context/<name>` for a project's own.
func (c Context) Qualified() string { return c.Origin.Qualified(NatureContext, c.Name) }

// StructureGate is one structure gate (dot-dir-file-store/main.tsp
// StructureGateDeclaration): an allowlist of paths that may be written, deny by
// default. `deny` carves exceptions out of `allow`. A primitive, not a rule
// nature: one file per ROOT, `file-guard/structure.yaml`, sibling of the
// per-guard subfolders.
//
// # Combined, not a singleton
//
// Every root may declare one — the project's own AND each enabled plugin's — and
// all of them load together; none shadows another. What keeps them from fighting
// is OWNERSHIP:
//
//   - the PROJECT's structure covers the whole tree and must not declare a
//     `scope`;
//   - a PLUGIN's structure must declare a `scope` — the folders it owns — and
//     its allow/deny only ever decide paths inside that scope.
//
// A written path inside exactly one plugin's scope is decided by that plugin
// (the project's `allow` does not widen it, but a project `deny` still vetoes);
// a path inside two plugins' scopes is refused as an ownership conflict; a path
// in no plugin's scope is decided by the project's structure, or permitted when
// the project declares none. The runtime order lives in internal/dispatch
// (StructureSet.Decide); the load rules in ValidateStructureGate.
type StructureGate struct {
	// Scope is the part of the tree a PLUGIN's structure gate owns: a list of
	// folders, each a `glob` ending in `/` (".mdmap/", "**/.adr/"). Required for a
	// plugin's structure.yaml and forbidden in a project's (the project's covers
	// the whole tree implicitly). Glob-only for now; the object shape leaves room
	// for `regex` later. A scope may not cover the whole tree (`**/`, `*/`, `/`).
	Scope []StructureEntry `yaml:"scope"`

	// Allow is the allowlist. Each entry is a glob or a regex (exactly one set).
	// In a plugin's structure every entry must lie inside its scope when the
	// scope is a literal folder.
	Allow []StructureEntry `yaml:"allow"`

	// Deny carves exceptions out of Allow. Optional. Each entry is a glob or a
	// regex (exactly one set). A PROJECT deny also vetoes writes inside a
	// plugin's scope.
	Deny []StructureEntry `yaml:"deny"`

	// Dir is the folder the structure.yaml sits in — `.sloprail/file-guard`. Not
	// a YAML field.
	Dir string `yaml:"-"`

	// Origin is where this structure gate was found — the project's own
	// `.sloprail`, or a plugin that ships it. Not a YAML field; the loader fills it.
	// The zero value is a project's own structure gate. It decides which rules
	// apply: a plugin's must carry a scope, a project's must not.
	Origin Origin `yaml:"-"`
}

// Attribution is the structure gate's provenance as a refusal should carry it —
// empty for a project's own, " from plugin X" for a shipped one. The structure
// gate has no per-name folder, so there is no name to quote; only the origin.
func (sg StructureGate) Attribution() string { return sg.Origin.Describe() }

// Qualified is the structure gate's disable key, `<plugin>/structure` for a
// shipped one and `structure` for a project's own.
func (sg StructureGate) Qualified() string { return sg.Origin.Qualified(NatureStructure, "") }

// Path is the structure.yaml this gate was read from, for a diagnostic that
// sends an author to the file.
func (sg StructureGate) Path() string { return filepath.Join(sg.Dir, fileStructure) }

// Describe names this structure gate for a person — "this project's structure
// gate" or "plugin \"x\"'s structure gate" — the one wording refusals and
// reports use.
func (sg StructureGate) Describe() string {
	if !sg.Origin.FromPlugin() {
		return "this project's structure gate"
	}
	return "plugin " + quoteName(sg.Origin.Plugin) + "'s structure gate"
}

// ScopeGlobs are the scope entries as written (each ending in `/`), in order —
// what a report lists as the folders a plugin owns.
func (sg StructureGate) ScopeGlobs() []string {
	out := make([]string, 0, len(sg.Scope))
	for _, e := range sg.Scope {
		out = append(out, e.Glob)
	}
	return out
}

// Prerequisite is a precondition that must hold before a rule's own check runs
// (dot-dir-file-store/main.tsp Prerequisite). A single list carrying three
// kinds, exactly one field set per entry — like Check's script/judge. What differs
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
//   - Citation is read off the EVENT: the action must carry a citation that
//     resolved in the session's record (see CitationPrerequisite). The agent
//     remedies a miss by making the change the grounded way.
//
// The spec's Prerequisite carries no `gate` field yet — dropped deliberately, no
// gate-depends-on-gate case exists among the units built so far.
type Prerequisite struct {
	// Skill is the name of a skill that must have been loaded (a real Skill
	// tool_use in the trajectory, not a claim) before the write. Optional.
	Skill string `yaml:"skill"`

	// Files, optional and meaningful only alongside Skill, names one or more
	// files INSIDE that skill (relative to its own directory — the one holding
	// its SKILL.md, e.g. "script-checks.md", "file-guard.md") that must ALSO
	// have been read (a Read tool_use, or a file-reading Bash command) before
	// the write — the same evidence a bare `{skill}` already accepts for the
	// skill's own SKILL.md, extended to its subpages.
	//
	// This exists because loading a skill only guarantees its SKILL.md was
	// read; a skill's detail — a per-event-kind script skeleton, a judge
	// contract — often lives in a page SKILL.md merely links to, and an agent
	// that loaded the skill has no guarantee of ever opening it. Measured
	// against a real onboarding run: every agent loaded authoring-guardrails
	// yet none read file-guard.md, so every check script was written from
	// memory and refused several times before it was right.
	//
	// A relative path with no `..` and no leading `/` — it names a file inside
	// the skill's own directory, never an escape from it. Load error otherwise.
	Files []string `yaml:"files"`

	// Context is the name of a context declaration that must run first, when both
	// this rule and that context wake on the same event in the same cycle.
	// Optional. Validated to resolve against the loaded contexts.
	Context string `yaml:"context"`

	// Citation requires the action to carry a resolved citation. Optional.
	Citation *CitationPrerequisite `yaml:"citation"`

	// When, optional, is a script that decides whether this prerequisite
	// applies to the action at all — for a requirement that is conditional on
	// the change ("a citation, but only when the body changes"). Resolved
	// relative to the rule's folder and handed the same payload on stdin as a
	// script check. Exit 0: the prerequisite applies. Exit 1: it does not, and
	// is skipped. Any other outcome — another exit code, a script that cannot
	// run, a timeout — applies it: a condition that could not be decided must
	// not waive a requirement.
	When string `yaml:"when"`
}

// kindsSet counts how many of skill/context/citation this prerequisite sets —
// exactly one is the only valid answer.
func (p Prerequisite) kindsSet() int {
	n := 0
	if p.Skill != "" {
		n++
	}
	if p.Context != "" {
		n++
	}
	if p.Citation != nil {
		n++
	}
	return n
}

// filesWithoutSkill reports whether Files is set on a prerequisite that does not
// also set Skill — Files names a subpage INSIDE a skill, so it has no meaning
// without one. Checked separately from isEmpty/bothSet: Files is not itself
// one of the exactly-one-of pair, it is an optional refinement of Skill alone.
func (p Prerequisite) filesWithoutSkill() bool { return len(p.Files) > 0 && p.Skill == "" }

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

	// Prepare is an optional deterministic executable that runs before the check
	// (a Judge or a Script). What it returns under `additionalContext` is added to
	// the judge's prompt variables, or to the script's payload. It only builds context:
	// it names no fingerprint, and neither its output nor the rendered prompt is part of
	// the verdict's cache key (a `subjects:` script supplies a fingerprint).
	Prepare string `yaml:"prepare"`

	// Model is which model a Judge asks, in the same modelset format sr-agent's
	// `--model` takes (a `size-xs`..`size-xxl` alias, a concrete harness model
	// name, or a comma-separated preference list). Empty means the engine's
	// default (size-md). Only meaningful alongside a Judge — a script has no
	// model to choose — so Model set on a script-only check is a load error the
	// validator refuses. See
	// dot-dir-file-store/main.tsp Check.model.
	Model string `yaml:"model"`

	// Timeout is how long a Judge may take before it is killed and read as a
	// refusal, overriding the engine's default (defaultCheckTimeout, 600s). A Go duration string
	// (`45s`, `2m`, `1m30s`) parsed with time.ParseDuration; the validator
	// refuses one that does not parse or is <= 0. Only meaningful alongside a
	// Judge — a script's own runtime is the author's to bound — so Timeout set
	// on a script-only check is a load error. See
	// dot-dir-file-store/main.tsp Check.timeout.
	Timeout string `yaml:"timeout"`

	// AllowedTools are the tools a Judge's agent is permitted to use, threaded to
	// sr-agent's `--allowed-tools` (which maps to claude's own `--allowed-tools`).
	// A judge that must Read the file it judges, or WebFetch a URL it verifies
	// against, names those here; the engine's judge substrate already grants the
	// Write the verdict file needs, so this is only the tools the RUBRIC's own
	// work requires. Only meaningful alongside a Judge — a script names its own
	// tools by being an executable — so AllowedTools set on a script-only check is
	// a load error, mirroring the stray-model/timeout rule. See
	// dot-dir-file-store/main.tsp Check.allowed_tools.
	AllowedTools []string `yaml:"allowed_tools"`

	// DisallowedTools are harness permission rules the Judge's agent is DENIED,
	// threaded to sr-agent's `--disallowed-tools` (claude's own
	// `--disallowed-tools`). A deny beats every allow, so this is how a rule that
	// grants a command family — `Bash(curl:*)` — takes back the forms of it the
	// judge must not use (`Bash(curl * -o *)`). Each entry is one rule in the
	// harness's own syntax, parentheses and spaces included. Judge-only, like
	// AllowedTools.
	DisallowedTools []string `yaml:"disallowed_tools"`

	// ResponseSchema is an optional JSON Schema file, resolved relative to the
	// rule's folder, naming the shape a Judge must answer in. The judge is shown
	// the schema in place of the default verdict instruction, and an answer that
	// does not match it is sent back to the judge like a malformed verdict. Empty
	// means the default shape, `{"pass": bool, "reasoning": string}`. Without a
	// PostProcess the schema must still describe a top-level boolean `pass` and
	// string `reasoning`, or nothing could read the verdict out of the answer.
	// Judge-only.
	ResponseSchema string `yaml:"response_schema"`

	// PostProcess is an optional deterministic executable, resolved relative to
	// the rule's folder, that runs AFTER a Judge answered: the mirror of Prepare.
	// It receives the judge's answer on stdin and prints the check's result,
	// `{"pass": bool, "reasoning": string, "metadata": {...}}`. Judge-only.
	PostProcess string `yaml:"post_process"`
}

// hasResponseSchema reports whether this check names the shape its judge answers in.
func (c Check) hasResponseSchema() bool { return c.ResponseSchema != "" }

// hasPostProcess reports whether this check runs a script on its judge's answer.
func (c Check) hasPostProcess() bool { return c.PostProcess != "" }

// hasModel reports whether this check sets a judge model override.
func (c Check) hasModel() bool { return c.Model != "" }

// hasTimeout reports whether this check sets a judge timeout override.
func (c Check) hasTimeout() bool { return c.Timeout != "" }

// hasAllowedTools reports whether this check sets a judge allowed-tools list.
func (c Check) hasAllowedTools() bool { return len(c.AllowedTools) > 0 }

// hasDisallowedTools reports whether this check sets a judge disallowed-tools list.
func (c Check) hasDisallowedTools() bool { return len(c.DisallowedTools) > 0 }

// isScript reports whether this check is the script half of the union.
func (c Check) isScript() bool { return c.Script != "" }

// isJudge reports whether this check is the judge half of the union.
func (c Check) isJudge() bool { return c.Judge != "" }

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
