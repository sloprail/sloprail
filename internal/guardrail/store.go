package guardrail

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/sloprail/sloprail/internal/module"
)

// Store reads the guardrails in force for a project: the ones it declares under
// its own dot-directory, and the ones it installed, which live inside the
// plugins that ship them.
//
// The two are read the same way and validated by the same code, deliberately. A
// plugin's guardrail is not a second kind of thing with its own rules — it is
// the same declaration in a different place, and the moment the two paths
// diverge is the moment a rule can behave one way for its author and another
// for the project that installed it.
type Store struct {
	root string

	// pluginRoots are plugin installation directories, in precedence order.
	// Empty for a store built with New, which is every caller that has no
	// business knowing about plugins — `sr-file validate` reads one project's
	// declarations and nothing else.
	pluginRoots []string
}

// New returns a store rooted at a project's dot-directory, reading only the
// guardrails the project itself declares.
func New(root string) *Store { return &Store{root: root} }

// NewWithPlugins returns a store that reads a project's own guardrails AND the
// ones shipped by the installed plugins whose directories are given.
//
// The plugin directories are supplied rather than discovered, and that is the
// whole harness-agnosticism argument in one signature: this package cannot find
// out which plugins are installed, does not try, and could not be made to
// depend on any particular harness's manifest without changing this line. The
// caller nearest the harness reads PluginDirsEnv and passes the result down.
func NewWithPlugins(root string, pluginRoots []string) *Store {
	return &Store{root: root, pluginRoots: pluginRoots}
}

// guardrailsDir is where a project's own declarations live.
func (s *Store) guardrailsDir() string { return filepath.Join(s.root, "guardrails") }

// pluginGuardrailsDir is where a plugin's declarations live inside its
// installation.
//
// The SAME relative layout a project uses — `guardrails/<name>/GUARDRAIL.md` —
// so a rule can be developed in a project and shipped in a plugin without being
// rewritten, and so an author reading either tree finds the same shape. A plugin
// ships guardrails the way it already ships hooks/ and skills/: a directory at
// its root named for what is in it.
func pluginGuardrailsDir(root string) string { return filepath.Join(root, "guardrails") }

// declarationPath returns the path of one guardrail's declaration under a
// guardrails directory.
func declarationPath(guardrailsDir, name string) string {
	return filepath.Join(guardrailsDir, name, "GUARDRAIL.md")
}

// Invalid is a declaration that could not be read or could not do what it says,
// and why.
//
// Reported rather than fatal: one malformed guardrail must not stop the others
// loading, or a single typo would silently disarm a whole project. The engine
// loads every declaration that is sound and refuses only the one that is not.
type Invalid struct {
	Name string

	// Origin says where this unloadable declaration was found. A broken rule
	// needs attribution more than a working one does, not less: the refusal it
	// causes tells an author to go and fix a file, and for a plugin's rule that
	// file is not in their project and not theirs to fix.
	Origin Origin

	// Problems is every fault found, not the first. A declaration usually
	// carries one mistake repeated — a misremembered field name across three
	// bindings — and fixing them one reload at a time is a cost with nothing
	// bought by it.
	//
	// Each carries a Kind, so a caller can ask what went wrong with errors.Is
	// rather than by matching on wording.
	Problems []Problem

	// Reasons is Problems rendered one line each, and Reason is those joined,
	// for callers that report text. Both derived at construction rather than
	// stored independently, so they cannot come to disagree with the list they
	// summarise.
	Reasons []string
	Reason  string
}

// newInvalid builds an Invalid from the problems found, keeping every view of
// them in step. The only place an Invalid is made.
func newInvalid(name string, problems ...Problem) Invalid {
	reasons := Messages(problems)
	return Invalid{
		Name:     name,
		Problems: problems,
		Reasons:  reasons,
		Reason:   strings.Join(reasons, "; "),
	}
}

// Qualified is how a consumer names this rule to switch it off, matching
// Declaration.Qualified. A broken plugin rule refuses every action it was bound
// to, so being able to name it is what lets a consumer switch it off rather
// than uninstall the plugin.
func (iv Invalid) Qualified() string { return iv.Origin.Qualified(iv.Name) }

// Attribution is this rule's name as a refusal should carry it, matching
// Declaration.Attribution.
func (iv Invalid) Attribution() string { return quote(iv.Name) + iv.Origin.Describe() }

// Has reports whether any of this declaration's problems is of the given kind,
// so a caller can branch on the class of fault rather than on its wording.
func (iv Invalid) Has(kind error) bool {
	for _, p := range iv.Problems {
		if errors.Is(p, kind) {
			return true
		}
	}
	return false
}

// AffectedKinds names the event kinds this broken declaration was bound to, in a
// stable order.
//
// What it is FOR: an enforcement point has to say what the loss of this rule
// costs, and the honest answer is "the events it was watching are no longer
// watched". Naming them lets a refusal be scoped to the work this rule was
// about, rather than to every action in the project — a typo in a rule about
// commands must not block a write no rule was ever written about.
//
// Read off the problems rather than off the declaration, because a declaration
// that failed to PARSE has no bindings to read: `loadOne` returns a zero
// Declaration and one malformed problem carrying no event. Such a declaration
// yields no kinds here, and a caller that scopes by kind will not scope to it —
// which is correct and deliberate. Nothing can be said about what an unreadable
// file was guarding, and inventing a scope here would be this function claiming
// evidence it does not have.
//
// What that means for ENFORCEMENT is a separate decision, and not one this
// function gets to make by staying quiet. "No kinds" must not be read as "no
// consequence": an enforcement point that scoped by kind and found none would
// permit every action while a file the project keeps as a guardrail sits
// unreadable, and — since no channel at PreToolUse delivers text without also
// refusing — would do it silently. The pre-tool path therefore asks about this
// case separately and refuses every action; see refuseForUnreadable in
// services/sr-session, which carries the argument in full. Callers must decide
// what an empty result means rather than defaulting into permission.
func (iv Invalid) AffectedKinds() []string {
	seen := make(map[string]bool)
	var kinds []string
	for _, p := range iv.Problems {
		if p.Event == "" || seen[p.Event] {
			continue
		}
		seen[p.Event] = true
		kinds = append(kinds, p.Event)
	}
	sort.Strings(kinds)
	return kinds
}

// Load reads every declaration, returning those that parsed and those that did
// not. A project with no dot-directory has no guardrails, which is not an
// error — it is the ordinary state of a project that has not adopted any.
//
// This checks a declaration is well-formed, not that it is enforceable: with no
// registry it cannot know which event kinds exist or what fields they carry.
// Callers holding a registry should use LoadWith, which is every caller that
// is about to act on what it loaded.
func (s *Store) Load() ([]Declaration, []Invalid, error) {
	return s.LoadWith(nil)
}

// LoadWith reads every declaration and validates each against the kinds this
// build can produce.
//
// A declaration that cannot do what it says is returned as Invalid rather than
// as a Declaration, so nothing downstream has to wonder whether what it is
// holding is enforceable. But only a fault in the DECLARATION disqualifies it.
// A hook that is merely not executable right now leaves the rule loaded, with
// the complaint on Declaration.Warnings, because the runtime already refuses an
// action whose hook cannot run — and a rule dropped here would instead let that
// action through. See Fault.
func (s *Store) LoadWith(reg *module.Registry) ([]Declaration, []Invalid, error) {
	res, err := s.Resolve(reg)
	return res.Declarations, res.Invalid, err
}

// Resolution is everything a load produced: the rules in force, the ones that
// could not be loaded, and the facts about resolution that a caller has to
// report rather than silently apply.
type Resolution struct {
	// Declarations are the rules in force, project and plugin together, already
	// resolved for precedence and for the project's disable list.
	Declarations []Declaration

	// Invalid are the declarations that could not be loaded.
	Invalid []Invalid

	// Shadowed are plugin rules a project rule of the same name displaced. See
	// Shadow for why this is returned rather than applied quietly.
	Shadowed []Shadow
}

// Shadow is one plugin guardrail that a project's own rule of the same name
// took precedence over.
//
// # Why the project wins
//
// A project must be able to override a rule it did not write. The alternative
// is that installing a plugin can impose a rule the project cannot adjust
// except by uninstalling the whole plugin — which makes the plugin an
// all-or-nothing proposition and guarantees that the first rule anyone
// disagrees with takes the other useful ones down with it.
//
// # Why it is REPORTED
//
// Silently is the failure this product exists to prevent. A project that
// happens to name a rule `no-secrets`, installs a plugin that also ships
// `no-secrets`, and never learns that the plugin's version stopped running, is
// a project that believes it has two protections and has one. Nothing about
// that is visible from the outside: both rules load, both are well-formed, and
// the tree looks exactly as it would if both were enforcing.
//
// So the shadowing travels back to the caller as a fact to announce, not as a
// decision already made and forgotten. The precedence is still applied — the
// project does win — but the consumer is told which rule they displaced and
// where it lives, so overriding stays a choice rather than an accident.
type Shadow struct {
	// Name is the guardrail name both rules share.
	Name string

	// Plugin is the plugin whose rule was displaced, and PluginDir is where
	// that displaced declaration sits — the path an author needs in order to
	// read the rule they have overridden.
	Plugin    string
	PluginDir string

	// WinnerDir is the guardrail folder that took precedence — the project's
	// own, or an earlier-listed plugin's.
	WinnerDir string

	// WinnerPlugin is the plugin the winning rule came from, empty when the
	// project's own rule won. What distinguishes "you overrode this" from "one
	// of your two plugins is quietly not enforcing", which are different
	// situations with different remedies.
	WinnerPlugin string
}

// Message renders a shadow for a person, naming both sides and what to do about
// it. One wording, here, because every hook point reports this and two copies
// would come to describe the same event differently.
//
// The two cases are worded apart because the remedy differs. A project
// overriding a shipped rule is usually deliberate and needs only to be visible.
// One plugin displacing another's rule is nobody's decision — the consumer
// installed both and got an ordering they never chose — so that message has to
// say which one is live, or the reader cannot tell which rule is running.
func (sh Shadow) Message() string {
	if sh.WinnerPlugin == "" {
		return fmt.Sprintf(
			"guardrail %q in this project takes precedence over the one plugin %q ships, "+
				"so the plugin's version (%s) is not enforcing. "+
				"Rename one of them if both were meant to run, or remove the project's copy to go back to the plugin's.",
			sh.Name, sh.Plugin, sh.PluginDir)
	}
	return fmt.Sprintf(
		"two installed plugins ship a guardrail called %q: plugin %q is enforcing (%s) "+
			"and plugin %q is not (%s). The earlier-installed one wins. "+
			"Disable whichever you do not want with `disabled: [<plugin>/%s]` in %s.",
		sh.Name, sh.WinnerPlugin, sh.WinnerDir, sh.Plugin, sh.PluginDir, sh.Name, ConfigFile)
}

// Resolve reads every guardrail in force and reports how it resolved them.
//
// The order of operations is the design, and each step is answerable:
//
//  1. The PROJECT's own guardrails are read first, so they are the ones already
//     in hand when a plugin offers the same name.
//  2. Each PLUGIN's guardrails are read in the order given, and a name already
//     taken is recorded as shadowed rather than loaded. First writer wins, so
//     the project always does, and between two plugins the earlier-listed one
//     does — which is why PluginDirs must not sort.
//  3. The project's DISABLE list is applied last, because it is the consumer's
//     final say over everything above it.
func (s *Store) Resolve(reg *module.Registry) (Resolution, error) {
	cfg, err := LoadConfig(s.root)
	if err != nil {
		// Refused rather than defaulted. A config that exists and cannot be
		// parsed is a project stating overrides nobody can read, and carrying on
		// with none of them would re-enable every rule it had switched off —
		// silently, and looking exactly like a project that had never written
		// one.
		return Resolution{}, err
	}

	var res Resolution
	// Which names are taken, and by what. Precedence is decided here rather than
	// by sorting afterwards, so a shadowed rule is never loaded even briefly and
	// there is one place that decides who wins.
	claimed := make(map[string]Declaration)

	project, invalid, err := s.loadFrom(s.guardrailsDir(), Origin{}, reg)
	if err != nil {
		return Resolution{}, err
	}
	res.Invalid = append(res.Invalid, invalid...)
	for _, d := range project {
		claimed[d.Name] = d
		res.Declarations = append(res.Declarations, d)
	}

	for _, root := range s.pluginRoots {
		origin := Origin{Plugin: pluginName(root), Root: root}
		shipped, shippedInvalid, err := s.loadFrom(pluginGuardrailsDir(root), origin, reg)
		if err != nil {
			return Resolution{}, err
		}

		// A shipped declaration that could not be loaded is shadowed by an
		// overriding rule of the same name, exactly as a sound one would be.
		//
		// This half is easy to leave out and its absence is severe. A broken
		// declaration refuses every action it was bound to — and an UNPARSEABLE
		// one refuses every action outright, since nothing can be said about what
		// it guarded. So without this, a project that has already overridden
		// `dup` with a rule of its own is still blocked at every tool call by the
		// plugin's broken `dup`, and the override it correctly made buys it
		// nothing.
		//
		// Overriding a name has to mean overriding it whatever state the
		// displaced declaration is in. The project's rule is the one in force for
		// that name; the plugin's is not consulted, so its being unreadable is
		// not a fact about anything the project is relying on. Reported as a
		// shadow rather than dropped silently, for the reason every shadow is.
		for _, iv := range shippedInvalid {
			if prior, taken := claimed[iv.Name]; taken {
				res.Shadowed = append(res.Shadowed, Shadow{
					Name:         iv.Name,
					Plugin:       origin.Plugin,
					PluginDir:    filepath.Join(pluginGuardrailsDir(root), iv.Name),
					WinnerDir:    prior.Dir,
					WinnerPlugin: prior.Origin.Plugin,
				})
				continue
			}
			res.Invalid = append(res.Invalid, iv)
		}

		for _, d := range shipped {
			if prior, taken := claimed[d.Name]; taken {
				// Reported whether the rule that won is the project's or another
				// plugin's, and for one reason either way: the consumer put both
				// of them there and cannot otherwise tell that one went quiet.
				// Narrowing this to project-over-plugin would leave the case a
				// consumer is LEAST able to diagnose — two installed plugins, one
				// of them silently not enforcing — as the only silent one.
				res.Shadowed = append(res.Shadowed, Shadow{
					Name:         d.Name,
					Plugin:       origin.Plugin,
					PluginDir:    d.Dir,
					WinnerDir:    prior.Dir,
					WinnerPlugin: prior.Origin.Plugin,
				})
				continue
			}
			claimed[d.Name] = d
			res.Declarations = append(res.Declarations, d)
		}
	}

	// The consumer's own switch-offs, applied to everything above.
	//
	// Filtered out of the list entirely rather than marked, so that a disabled
	// plugin rule is inert in the same way a disabled project rule is: its kinds
	// never enter the bound set, no module runs for it, and no hook of its is
	// ever dispatched. Marking it and checking later would leave the rule paying
	// for itself and, worse, would leave one more place that has to remember to
	// check.
	if len(cfg.Disabled) > 0 {
		kept := res.Declarations[:0]
		for _, d := range res.Declarations {
			if cfg.IsDisabled(d.Qualified()) {
				continue
			}
			kept = append(kept, d)
		}
		res.Declarations = kept

		// The INVALID ones too, and this half is the one that matters most.
		//
		// A declaration that cannot load refuses every action it was bound to —
		// deliberately, because a rule that cannot be checked must not read as
		// approval. When that rule is a plugin's, the consumer cannot fix the
		// file: it is in an install cache, they did not write it, and an edit
		// there is undone by the next upgrade. Without this, a plugin shipping
		// one broken guardrail would wedge every consuming project with no way
		// out but uninstalling the plugin.
		//
		// So the disable list has to reach declarations that never parsed, which
		// is exactly why it is keyed on the FOLDER name — available for a file
		// whose contents are unreadable — rather than on anything inside the
		// declaration.
		keptInvalid := res.Invalid[:0]
		for _, iv := range res.Invalid {
			if cfg.IsDisabled(iv.Qualified()) {
				continue
			}
			keptInvalid = append(keptInvalid, iv)
		}
		res.Invalid = keptInvalid
	}

	sort.Slice(res.Declarations, func(i, j int) bool {
		return res.Declarations[i].Qualified() < res.Declarations[j].Qualified()
	})
	sort.Slice(res.Invalid, func(i, j int) bool {
		return res.Invalid[i].Qualified() < res.Invalid[j].Qualified()
	})
	sort.Slice(res.Shadowed, func(i, j int) bool { return res.Shadowed[i].Name < res.Shadowed[j].Name })
	return res, nil
}

// loadFrom reads every declaration under one guardrails directory, tagging each
// with where it came from.
//
// A missing directory is not an error at either origin. A project that has
// adopted no guardrails has no folder, and a plugin that ships none has none
// either — the overwhelmingly common case, since most plugins are skills and
// hooks. Treating either as a fault would make the engine refuse on its own
// behalf for the ordinary state of things.
func (s *Store) loadFrom(dir string, origin Origin, reg *module.Registry) ([]Declaration, []Invalid, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("guardrail: read %s: %w", dir, err)
	}

	var (
		decls   []Declaration
		invalid []Invalid
	)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		d, problems := loadOne(dir, e.Name(), origin)
		if len(problems) == 0 && reg != nil {
			problems = Validate(d, reg)
		}

		disabling, warnings := Partition(problems)
		if len(disabling) > 0 {
			// Everything found is reported, warnings included: an author fixing
			// the declaration should see the chmod they also owe.
			iv := newInvalid(e.Name(), problems...)
			iv.Origin = origin
			invalid = append(invalid, iv)
			continue
		}

		// Environment faults alone. The rule loads and carries its complaint,
		// so it can still refuse while the machine is wrong.
		d.Warnings = warnings
		decls = append(decls, d)
	}
	return decls, invalid, nil
}

// loadOne reads and parses one declaration, returning the problems that stopped
// it rather than an error, so every failure carries a Kind a caller can branch
// on. An empty slice means the declaration parsed.
//
// A package function rather than a method, because it is now called for two
// different roots within one load and the directory is therefore an argument
// rather than a property of the store.
func loadOne(guardrailsDir, name string, origin Origin) (Declaration, []Problem) {
	data, err := os.ReadFile(declarationPath(guardrailsDir, name))
	if err != nil {
		return Declaration{}, []Problem{malformed("read: %v", err)}
	}

	front, body, err := splitFrontmatter(data)
	if err != nil {
		return Declaration{}, []Problem{malformed("%v", err)}
	}

	// Before unmarshalling, because unmarshalling stops at the first duplicate
	// and says it in the library's words rather than the declaration's. See
	// duplicateKeys.
	dups, err := duplicateKeys(front)
	if err != nil {
		return Declaration{}, []Problem{malformed("%v", err)}
	}
	if len(dups) > 0 {
		return Declaration{}, dups
	}

	var d Declaration
	if err := yaml.Unmarshal(front, &d); err != nil {
		return Declaration{}, []Problem{malformed("parse frontmatter: %v", err)}
	}

	d.Name = name
	d.Body = string(body)
	d.Dir = filepath.Join(guardrailsDir, name)
	d.Origin = origin
	return d, nil
}

// malformed builds a problem for a declaration that could not be read at all,
// which is not about any one event or binding.
func malformed(format string, args ...any) Problem {
	return Problem{
		Kind:    ErrMalformed,
		Fault:   FaultDeclaration,
		Binding: -1,
		Hook:    -1,
		Detail:  fmt.Sprintf(format, args...),
	}
}

var fence = []byte("---")

// isFence reports whether a line is a frontmatter fence: exactly `---` once
// surrounding whitespace is gone.
//
// One predicate, used for both the opening and the closing fence. They were
// matched differently — HasPrefix opening, Equal closing — so `----` and
// `---yaml` opened a block that only a bare `---` could close. That asymmetry
// is not a tolerance anyone chose; it is two spellings of the same idea drifting
// apart, and the way to keep them from drifting again is for there to be one.
//
// Exact rather than prefix, because a prefix match cannot tell a fence from a
// line that starts like one. `---yaml` is a person reaching for the fenced-code
// spelling of frontmatter, and `----` is a typo or a horizontal rule; reading
// either as a fence means parsing the file as something its author did not
// write. Refusing is what puts the mistake in front of them.
func isFence(line []byte) bool {
	return bytes.Equal(bytes.TrimSpace(line), fence)
}

// SplitFrontmatter is splitFrontmatter, exported for callers outside this
// package — `sr-file validate` splits a .md the same way, and a second splitter
// written beside this one is a second answer to "where does the frontmatter
// end", free to disagree with the first about `---yaml` or `----`. There is one
// implementation so there is one answer.
//
// The leading YAML is returned with its lines joined as they appeared, so a
// caller that reports positions inside it counts from the fence, not from the
// top of the file — line N of `front` is line N+1 of the file.
func SplitFrontmatter(data []byte) (front, body []byte, err error) {
	return splitFrontmatter(data)
}

// splitFrontmatter separates the leading YAML document from the prose beneath
// it. The prose is returned untouched: it is documentation and rubric at once,
// and normalising it would change what a judge is judging against.
func splitFrontmatter(data []byte) (front, body []byte, err error) {
	lines := bytes.SplitAfter(data, []byte("\n"))
	if len(lines) == 0 || !isFence(lines[0]) {
		return nil, nil, fmt.Errorf("no frontmatter: a declaration begins with a --- fence")
	}

	for i := 1; i < len(lines); i++ {
		if isFence(lines[i]) {
			front = bytes.Join(lines[1:i], nil)
			body = bytes.Join(lines[i+1:], nil)
			return front, body, nil
		}
	}
	return nil, nil, fmt.Errorf("unterminated frontmatter: no closing --- fence")
}
