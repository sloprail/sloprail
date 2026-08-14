// Package guardrail reads what a project declares it holds its agents to.
package guardrail

// Declaration is one guardrail: the frontmatter of a GUARDRAIL.md, plus the
// prose beneath it.
//
// The prose is not decoration. A judge hook reads it as its rubric, which is
// why the rule and its documentation are one file rather than two that drift.
type Declaration struct {
	// Name is the guardrail's name and its folder's name. Taken from the
	// folder rather than the frontmatter: a name recorded twice is a name that
	// can disagree with itself.
	Name string `yaml:"-"`

	// Enabled defaults to true. Turning a rule off is a declaration rather
	// than a deletion, so the reasoning that produced it survives the decision
	// to stop enforcing it.
	Enabled *bool `yaml:"enabled"`

	// Hooks is keyed by event kind. Keying by event is what lets one guardrail
	// bind to several and reuse a script across them — a rule that must hold
	// before a file is written and again once the cycle ends is declared once.
	Hooks map[string][]Binding `yaml:"hooks"`

	// Body is the prose beneath the frontmatter, byte for byte. A parser that
	// normalised it would silently change what a judge is judging against.
	Body string `yaml:"-"`

	// Dir is the guardrail's own folder, so a hook can read what sits beside
	// it and so its commands resolve relative to it.
	//
	// For a plugin's guardrail this is inside the plugin's installation, which
	// is what makes a shipped hook script work at all: the script travels with
	// the declaration, and the engine chdirs here before running it, so
	// `./check-rules.sh` resolves to the copy that was installed rather than to
	// something the consumer would have had to copy in.
	Dir string `yaml:"-"`

	// Origin says whether this rule came from the project or from an installed
	// plugin, and which. Read off where the file was found, not out of the
	// frontmatter: a declaration that named its own plugin could lie, or could
	// simply go stale when the plugin was renamed, and the loader already knows
	// the truth.
	Origin Origin `yaml:"-"`

	// Warnings are problems found at load that did not disqualify the rule —
	// the machine being wrong rather than the declaration. A hook that is not
	// executable is the case: the rule is loaded anyway so that it can still
	// refuse, and this is what says so out loud. See Fault.
	Warnings []Problem `yaml:"-"`
}

// Binding ties one event kind to the hooks that run for it.
type Binding struct {
	// Matcher decides which occurrences this binding responds to, over the
	// event's own fields. Absent means every occurrence.
	Matcher string `yaml:"matcher"`

	// Hooks run when the event fires and the matcher admits it.
	Hooks []Hook `yaml:"hooks"`
}

// Hook is one executable a binding runs.
type Hook struct {
	// Type is how the hook is invoked. One member for now, so that another
	// mechanism can be added without breaking declarations already written.
	Type string `yaml:"type"`

	// Command is executed relative to the guardrail's folder.
	Command string `yaml:"command"`
}

// HookCommand executes a shell command, passing the event on stdin and reading
// the outcome from stdout.
const HookCommand = "command"

// IsEnabled reports whether the engine loads this guardrail. Absent means yes:
// a declaration that says nothing about being switched off is switched on.
//
// This answers only what the DECLARATION says. A consumer switching off a
// plugin's rule cannot edit that declaration, and says so in the project's own
// config instead — which the loader applies before a declaration ever reaches
// here. See Config.
func (d Declaration) IsEnabled() bool { return d.Enabled == nil || *d.Enabled }

// Qualified is how a consumer names this rule to switch it off, and how a
// refusal identifies it when two plugins ship the same name.
func (d Declaration) Qualified() string { return d.Origin.Qualified(d.Name) }

// Attribution is the guardrail's name as a refusal should carry it: the name a
// project author would search for, plus where it came from when that is not
// this project.
//
// A refusal already names the rule. What it could not say before was where the
// rule LIVES, and for a plugin's rule that is the whole difficulty — the file is
// in an install cache, the project never wrote it, and a name alone sends the
// reader looking in .sloprail/guardrails/ where there is nothing to find.
func (d Declaration) Attribution() string {
	return quote(d.Name) + d.Origin.Describe()
}

// BoundKinds returns every event kind this declaration binds to.
func (d Declaration) BoundKinds() []string {
	kinds := make([]string, 0, len(d.Hooks))
	for k := range d.Hooks {
		kinds = append(kinds, k)
	}
	return kinds
}
