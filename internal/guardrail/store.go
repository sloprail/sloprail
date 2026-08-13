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

// Store reads the guardrails a project declares, under its dot-directory.
type Store struct {
	root string
}

// New returns a store rooted at a project's dot-directory.
func New(root string) *Store { return &Store{root: root} }

// guardrailsDir is where declarations live.
func (s *Store) guardrailsDir() string { return filepath.Join(s.root, "guardrails") }

// declarationPath returns the path of one guardrail's declaration.
func (s *Store) declarationPath(name string) string {
	return filepath.Join(s.guardrailsDir(), name, "GUARDRAIL.md")
}

// Invalid is a declaration that could not be read or could not do what it says,
// and why.
//
// Reported rather than fatal: one malformed guardrail must not stop the others
// loading, or a single typo would silently disarm a whole project. The engine
// loads every declaration that is sound and refuses only the one that is not.
type Invalid struct {
	Name string

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
	entries, err := os.ReadDir(s.guardrailsDir())
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("guardrail: read %s: %w", s.guardrailsDir(), err)
	}

	var (
		decls   []Declaration
		invalid []Invalid
	)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		d, problems := s.loadOne(e.Name())
		if len(problems) == 0 && reg != nil {
			problems = Validate(d, reg)
		}

		disabling, warnings := Partition(problems)
		if len(disabling) > 0 {
			// Everything found is reported, warnings included: an author fixing
			// the declaration should see the chmod they also owe.
			invalid = append(invalid, newInvalid(e.Name(), problems...))
			continue
		}

		// Environment faults alone. The rule loads and carries its complaint,
		// so it can still refuse while the machine is wrong.
		d.Warnings = warnings
		decls = append(decls, d)
	}

	sort.Slice(decls, func(i, j int) bool { return decls[i].Name < decls[j].Name })
	sort.Slice(invalid, func(i, j int) bool { return invalid[i].Name < invalid[j].Name })
	return decls, invalid, nil
}

// loadOne reads and parses one declaration, returning the problems that stopped
// it rather than an error, so every failure carries a Kind a caller can branch
// on. An empty slice means the declaration parsed.
func (s *Store) loadOne(name string) (Declaration, []Problem) {
	data, err := os.ReadFile(s.declarationPath(name))
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
	d.Dir = filepath.Join(s.guardrailsDir(), name)
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

// splitFrontmatter separates the leading YAML document from the prose beneath
// it. The prose is returned untouched: it is documentation and rubric at once,
// and normalising it would change what a judge is judging against.
func splitFrontmatter(data []byte) (front, body []byte, err error) {
	lines := bytes.SplitAfter(data, []byte("\n"))
	if len(lines) == 0 || !bytes.HasPrefix(bytes.TrimSpace(lines[0]), fence) {
		return nil, nil, fmt.Errorf("no frontmatter: a declaration begins with a --- fence")
	}

	for i := 1; i < len(lines); i++ {
		if bytes.Equal(bytes.TrimSpace(lines[i]), fence) {
			front = bytes.Join(lines[1:i], nil)
			body = bytes.Join(lines[i+1:], nil)
			return front, body, nil
		}
	}
	return nil, nil, fmt.Errorf("unterminated frontmatter: no closing --- fence")
}
