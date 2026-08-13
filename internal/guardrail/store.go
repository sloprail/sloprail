package guardrail

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
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

// Invalid is a declaration that could not be read, and why.
//
// Reported rather than fatal: one malformed guardrail must not stop the others
// loading, or a single typo would silently disarm a whole project.
type Invalid struct {
	Name   string
	Reason string
}

// Load reads every declaration, returning those that parsed and those that did
// not. A project with no dot-directory has no guardrails, which is not an
// error — it is the ordinary state of a project that has not adopted any.
func (s *Store) Load() ([]Declaration, []Invalid, error) {
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
		d, err := s.loadOne(e.Name())
		if err != nil {
			invalid = append(invalid, Invalid{Name: e.Name(), Reason: err.Error()})
			continue
		}
		decls = append(decls, d)
	}

	sort.Slice(decls, func(i, j int) bool { return decls[i].Name < decls[j].Name })
	sort.Slice(invalid, func(i, j int) bool { return invalid[i].Name < invalid[j].Name })
	return decls, invalid, nil
}

func (s *Store) loadOne(name string) (Declaration, error) {
	data, err := os.ReadFile(s.declarationPath(name))
	if err != nil {
		return Declaration{}, fmt.Errorf("read: %w", err)
	}

	front, body, err := splitFrontmatter(data)
	if err != nil {
		return Declaration{}, err
	}

	var d Declaration
	if err := yaml.Unmarshal(front, &d); err != nil {
		return Declaration{}, fmt.Errorf("parse frontmatter: %w", err)
	}

	d.Name = name
	d.Body = string(body)
	d.Dir = filepath.Join(s.guardrailsDir(), name)
	return d, nil
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
