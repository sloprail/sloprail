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
	Dir string `yaml:"-"`

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
func (d Declaration) IsEnabled() bool { return d.Enabled == nil || *d.Enabled }

// BoundKinds returns every event kind this declaration binds to.
func (d Declaration) BoundKinds() []string {
	kinds := make([]string, 0, len(d.Hooks))
	for k := range d.Hooks {
		kinds = append(kinds, k)
	}
	return kinds
}
