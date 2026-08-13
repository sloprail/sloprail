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

// Init creates the directory a project keeps its guardrails in, and reports
// whether it had to.
//
// What it leaves behind is a place for declarations to go and nothing else. No
// example rule: a scaffolded guardrail is a rule nobody chose, sitting in a
// project as though someone had, and the first thing an author would have to do
// is work out whether it was theirs. Rules are written by an agent that has read
// how they work — `sloprail guardrail help` is what that agent reads.
//
// No config file either. Every question one could answer is already answered by
// the declarations themselves, and a file holding nothing but defaults is a file
// that has to be kept valid for no return.
//
// Running this twice is running it once. It creates what is missing and reads
// what is there, so a project that already has guardrails keeps them: an init
// that clobbered would be a rule-deleting command wearing a setup command's
// name.
func (s *Store) Init() (created bool, err error) {
	dir := s.guardrailsDir()
	switch info, statErr := os.Stat(dir); {
	case statErr == nil && info.IsDir():
		return false, nil
	case statErr == nil:
		// Something is there and it is not a directory, so Load's ReadDir will
		// fail and no guardrail can ever be declared. Reporting success here
		// would leave a project that setup called finished and that cannot work.
		return false, fmt.Errorf("guardrail: %s exists but is not a directory", dir)
	case !os.IsNotExist(statErr):
		return false, fmt.Errorf("guardrail: stat %s: %w", dir, statErr)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, fmt.Errorf("guardrail: create %s: %w", dir, err)
	}
	return true, nil
}

// GuardrailsDir is where declarations live. Exported so a command that has to
// name the path to a person prints the one the store will actually read, rather
// than rebuilding it and being able to disagree.
func (s *Store) GuardrailsDir() string { return s.guardrailsDir() }

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
// build can produce. A declaration that cannot do what it says is returned as
// Invalid rather than as a Declaration, so nothing downstream has to wonder
// whether what it is holding is enforceable.
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
		if len(problems) > 0 {
			invalid = append(invalid, newInvalid(e.Name(), problems...))
			continue
		}
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
