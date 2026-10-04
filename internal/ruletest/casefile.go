// Package ruletest runs a project's own rules against canned scenarios, with no
// model: `sr-checks test` and `sr-checks doctor`.
//
// A case lives beside the rule it proves:
//
//	.sloprail/<nature>/<name>/tests/<case>/
//	    case.yaml         what the case expects (see Case)
//	    setup.sh          pure bash + git that builds the repository
//	    trajectory.yaml   optional: an ordered list of NORMALIZED events (see Step)
//
// The case never speaks any harness's payload format. A trajectory is written in
// the engine's own event vocabulary (the flat event model of events.md), so one
// test is valid whichever harness the project runs on: the adapters from a
// harness's payloads to these events are proved once per harness, in the engine's
// own tests, and every rule is proved once, here.
package ruletest

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Expect is a verdict a case (or one step of its trajectory) expects.
type Expect string

const (
	ExpectRefuse Expect = "refuse"
	ExpectPermit Expect = "permit"
)

// JudgeStub is the canned answer a case gives one judge.
type JudgeStub struct {
	// Pass is the verdict the stubbed judge returns; Reasoning is the sentence a
	// refusal carries (required when Pass is false: a judge that refuses with no
	// reason is not a verdict the engine ever produces).
	Pass      bool   `yaml:"pass" json:"pass"`
	Reasoning string `yaml:"reasoning" json:"reasoning"`
	// PromptContains are substrings the rendered prompt must hold: what the model
	// would be shown. Proves the template renders the change, and the context a
	// `prepare` assembled, into the question.
	PromptContains []string `yaml:"prompt_contains" json:"promptContains,omitempty"`
	// Optional marks a stub the case does not require to be reached. By default a
	// stubbed judge that is never asked fails the case: the rule stopped reaching
	// it, which is the regression the stub exists to catch.
	Optional bool `yaml:"optional" json:"optional,omitempty"`
}

// Case is one scenario for a rule: case.yaml.
type Case struct {
	// Name is the case's folder name. Dir is the folder.
	Name string `yaml:"-"`
	Dir  string `yaml:"-"`

	// Description says what the case proves, for the report.
	Description string `yaml:"description"`

	// Expect is the verdict the case expects: for a case without a trajectory, the
	// verdict of the file-guards over base..HEAD; for one with a trajectory, the
	// verdict of its LAST event step when that step names none of its own.
	Expect Expect `yaml:"expect"`

	// ReasonContains are substrings the refusal must hold (a string or a list).
	// Skipped under --live-judges, where a judge's wording is the model's.
	ReasonContains StringList `yaml:"reason_contains"`

	// Base is the revision a file-guard case's range starts from. Default: the ref
	// `base` when setup.sh made one (`git tag base`), else HEAD~1.
	Base string `yaml:"base"`

	// With names other rules of the same `.sloprail` to load alongside the rule
	// under test (`context/tag-declared`): the project as the composition under
	// test. Only the rule under test and these are loaded; everything else is out.
	With []string `yaml:"with"`

	// Judges maps a judge file to its canned answer. A bare name (`judge.md.j2`)
	// is the rule under test's own; another rule's is `<nature>/<name>/<file>`.
	Judges map[string]JudgeStub `yaml:"judges"`

	// UserSays are the user's own messages in the session record, so a citation
	// (`Sloprail-Cites-User: <quote>`, `require: citation`) can resolve.
	UserSays []string `yaml:"user_says"`

	// Contexts are the states the contexts must be in once the trajectory has run:
	// `active` or `inactive`, by context name.
	Contexts map[string]string `yaml:"contexts"`

	// Trajectory is the parsed trajectory.yaml, nil for a file-guard-range case.
	Trajectory []Step `yaml:"-"`
}

// StringList is a YAML string or list of strings.
type StringList []string

// UnmarshalYAML accepts `key: text` and `key: [a, b]`.
func (s *StringList) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		*s = StringList{n.Value}
		return nil
	case yaml.SequenceNode:
		var l []string
		if err := n.Decode(&l); err != nil {
			return err
		}
		*s = l
		return nil
	}
	return fmt.Errorf("line %d: expected a string or a list of strings", n.Line)
}

// LoadCases reads every case under a rule folder's tests/ directory, sorted by
// name. A rule with no tests directory has no cases. A case that cannot be read is
// returned with its error in the error list, never skipped: a broken case must not
// read as a rule with fewer tests.
func LoadCases(ruleDir string) ([]Case, []error) {
	root := filepath.Join(ruleDir, "tests")
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, []error{fmt.Errorf("%s: %w", root, err)}
	}
	var cases []Case
	var errs []error
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		c, err := loadCase(filepath.Join(root, e.Name()))
		if err != nil {
			errs = append(errs, fmt.Errorf("case %s: %w", e.Name(), err))
			continue
		}
		cases = append(cases, c)
	}
	sort.Slice(cases, func(i, j int) bool { return cases[i].Name < cases[j].Name })
	return cases, errs
}

func loadCase(dir string) (Case, error) {
	var c Case
	data, err := os.ReadFile(filepath.Join(dir, "case.yaml"))
	if err != nil {
		return c, fmt.Errorf("no case.yaml: %w", err)
	}
	// Strict: a mistyped key (`exepct:`) is a case that asserts less than its
	// author believes, the silence this whole feature exists to remove.
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return c, fmt.Errorf("case.yaml: %w", err)
	}
	c.Name = filepath.Base(dir)
	c.Dir = dir
	if _, err := os.Stat(filepath.Join(dir, "setup.sh")); err != nil {
		return c, fmt.Errorf("no setup.sh: %w", err)
	}
	if tdata, err := os.ReadFile(filepath.Join(dir, "trajectory.yaml")); err == nil {
		steps, err := parseTrajectory(tdata)
		if err != nil {
			return c, fmt.Errorf("trajectory.yaml: %w", err)
		}
		c.Trajectory = steps
	} else if !errors.Is(err, os.ErrNotExist) {
		return c, err
	}
	if err := c.validate(); err != nil {
		return c, err
	}
	return c, nil
}

func (c Case) validate() error {
	if c.Expect != "" && c.Expect != ExpectRefuse && c.Expect != ExpectPermit {
		return fmt.Errorf("case.yaml: expect is %q, want refuse or permit", c.Expect)
	}
	if c.Expect == "" && !c.hasStepExpectations() && len(c.Contexts) == 0 {
		return errors.New("case.yaml: the case expects nothing: set `expect: refuse|permit` (or `contexts:` / per-step `expect:` in the trajectory)")
	}
	for file, j := range c.Judges {
		if !j.Pass && strings.TrimSpace(j.Reasoning) == "" {
			return fmt.Errorf("case.yaml: judge %s stubs pass: false with no reasoning", file)
		}
	}
	for name, st := range c.Contexts {
		if st != "active" && st != "inactive" {
			return fmt.Errorf("case.yaml: contexts.%s is %q, want active or inactive", name, st)
		}
	}
	if len(c.Trajectory) == 0 && len(c.Contexts) > 0 {
		return errors.New("case.yaml: `contexts:` is read after a trajectory, and this case has none")
	}
	if len(c.Trajectory) == 0 && c.Expect == "" {
		return errors.New("case.yaml: a case without a trajectory judges a commit range and needs `expect: refuse|permit`")
	}
	return nil
}

func (c Case) hasStepExpectations() bool {
	for _, s := range c.Trajectory {
		if s.Expect != "" || len(s.Contexts) > 0 {
			return true
		}
	}
	return false
}

// JudgeKey is the stub table's key for a judge file of a rule: `<nature>/<name>/<file>`.
func JudgeKey(nature, name, template string) string {
	return nature + "/" + name + "/" + filepath.ToSlash(filepath.Clean(template))
}
