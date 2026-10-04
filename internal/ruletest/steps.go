package ruletest

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// StepType is what a trajectory step does.
type StepType string

const (
	// StepEvent dispatches one normalized event through the engine.
	StepEvent StepType = "event"
	// StepRun changes the repository between events (`run: <bash>`).
	StepRun StepType = "run"
	// StepSubagent starts or stops a sub-agent (`subagent: start|stop`, `id:`).
	StepSubagent StepType = "subagent"
	// StepChecksRun judges the file-guards over the session's commits, as the
	// agent's `sr-checks run` does before a Stop verifies them.
	StepChecksRun StepType = "checks_run"
	// StepAssert only checks context states (`contexts: {name: active}`).
	StepAssert StepType = "assert"
)

// Step is one entry of trajectory.yaml.
//
// A step is a YAML map. The keys the runner owns are listed below; every other key
// of an event step is a field of the event itself and is checked against the event
// kind's declared fields (a mistyped field is an error, never an ignored key).
//
//   - kind: PreFileCreate        # an event: kind plus its fields (events.md)
//     path: memories/a.md
//     newContent: "..."
//     agent: reviewer            # optional: the event comes from this sub-agent
//     expect: refuse             # optional: the verdict this event must get
//     reason_contains: "..."     # optional
//     contexts: {research: active}   # optional: states right after this step
//   - run: git commit -am change    # bash, in the repository
//   - subagent: start               # or stop
//     id: reviewer
//   - checks_run: {}                # judge the file-guards over the session's commits
//   - contexts: {research: inactive}   # just an assertion
type Step struct {
	Type StepType

	// Line is where the step starts in trajectory.yaml, for messages.
	Line int

	// Event is the step's event fields (everything that is not a runner key),
	// Kind its kind.
	Kind  string
	Event map[string]any

	// Agent is the sub-agent an event comes from; ID is a subagent step's agent.
	Agent string
	ID    string

	// Run is a run step's bash. Action is a subagent step's `start` or `stop`.
	Run    string
	Action string

	// ChecksBase is a checks_run step's base revision (default: the engine's
	// default base).
	ChecksBase string

	Expect         Expect
	ReasonContains []string
	Contexts       map[string]string
}

var runnerKeys = map[string]bool{
	"agent": true, "expect": true, "reason_contains": true, "contexts": true,
	"run": true, "subagent": true, "id": true, "checks_run": true,
}

func parseTrajectory(data []byte) ([]Step, error) {
	var root yaml.Node
	if err := yaml.NewDecoder(bytes.NewReader(data)).Decode(&root); err != nil {
		return nil, err
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) != 1 || root.Content[0].Kind != yaml.SequenceNode {
		return nil, errors.New("a trajectory is a list of steps")
	}
	seq := root.Content[0]
	if len(seq.Content) == 0 {
		return nil, errors.New("the trajectory has no steps")
	}
	var steps []Step
	for i, n := range seq.Content {
		s, err := parseStep(n)
		if err != nil {
			return nil, fmt.Errorf("step %d (line %d): %w", i+1, n.Line, err)
		}
		steps = append(steps, s)
	}
	return steps, nil
}

func parseStep(n *yaml.Node) (Step, error) {
	s := Step{Line: n.Line}
	if n.Kind != yaml.MappingNode {
		return s, errors.New("a step is a map")
	}
	var m map[string]any
	if err := n.Decode(&m); err != nil {
		return s, err
	}
	str := func(k string) (string, error) {
		v, ok := m[k]
		if !ok {
			return "", nil
		}
		t, ok := v.(string)
		if !ok {
			return "", fmt.Errorf("%s must be a string", k)
		}
		return t, nil
	}
	var err error
	if s.Agent, err = str("agent"); err != nil {
		return s, err
	}
	if s.ID, err = str("id"); err != nil {
		return s, err
	}
	exp, err := str("expect")
	if err != nil {
		return s, err
	}
	s.Expect = Expect(exp)
	if s.Expect != "" && s.Expect != ExpectRefuse && s.Expect != ExpectPermit {
		return s, fmt.Errorf("expect is %q, want refuse or permit", exp)
	}
	switch v := m["reason_contains"].(type) {
	case nil:
	case string:
		s.ReasonContains = []string{v}
	case []any:
		for _, x := range v {
			t, ok := x.(string)
			if !ok {
				return s, errors.New("reason_contains must be strings")
			}
			s.ReasonContains = append(s.ReasonContains, t)
		}
	default:
		return s, errors.New("reason_contains must be a string or a list of strings")
	}
	if c, ok := m["contexts"]; ok {
		cm, ok := c.(map[string]any)
		if !ok {
			return s, errors.New("contexts must be a map of context name to active|inactive")
		}
		s.Contexts = map[string]string{}
		for k, v := range cm {
			t, _ := v.(string)
			if t != "active" && t != "inactive" {
				return s, fmt.Errorf("contexts.%s is %v, want active or inactive", k, v)
			}
			s.Contexts[k] = t
		}
	}

	_, hasKind := m["kind"]
	_, hasRun := m["run"]
	_, hasSub := m["subagent"]
	_, hasChecks := m["checks_run"]
	count := 0
	for _, b := range []bool{hasKind, hasRun, hasSub, hasChecks} {
		if b {
			count++
		}
	}
	if count > 1 {
		return s, errors.New("a step is one of: an event (`kind:`), `run:`, `subagent:`, `checks_run:` — not several")
	}
	switch {
	case hasKind:
		s.Type = StepEvent
		if s.Kind, err = str("kind"); err != nil || s.Kind == "" {
			return s, errors.New("kind must be an event kind such as PreFileCreate")
		}
		s.Event = map[string]any{}
		for k, v := range m {
			if k == "kind" || runnerKeys[k] {
				continue
			}
			s.Event[k] = v
		}
	case hasRun:
		s.Type = StepRun
		if s.Run, err = str("run"); err != nil || strings.TrimSpace(s.Run) == "" {
			return s, errors.New("run must be a bash script")
		}
	case hasSub:
		s.Type = StepSubagent
		if s.Action, err = str("subagent"); err != nil || (s.Action != "start" && s.Action != "stop") {
			return s, errors.New("subagent must be start or stop")
		}
		if s.ID == "" {
			return s, errors.New("a subagent step needs an `id:`")
		}
	case hasChecks:
		s.Type = StepChecksRun
		if cm, ok := m["checks_run"].(map[string]any); ok {
			for k, v := range cm {
				if k != "base" {
					return s, fmt.Errorf("checks_run has no key %q (only base)", k)
				}
				s.ChecksBase, _ = v.(string)
			}
		} else if b, ok := m["checks_run"].(bool); !(ok && b) && m["checks_run"] != nil {
			return s, errors.New("checks_run is `true`, empty, or {base: <rev>}")
		}
	default:
		if len(s.Contexts) == 0 {
			return s, errors.New("a step needs `kind:`, `run:`, `subagent:`, `checks_run:` or `contexts:`")
		}
		s.Type = StepAssert
	}
	if s.Type != StepEvent && s.Type != StepSubagent && s.Agent != "" {
		return s, errors.New("`agent:` belongs on an event step")
	}
	if s.Type != StepEvent && s.Type != StepSubagent && s.Type != StepChecksRun && s.Expect != "" {
		return s, errors.New("`expect:` belongs on an event, a subagent stop or a checks_run step")
	}
	if s.Type == StepSubagent && s.Action == "start" && s.Expect != "" {
		return s, errors.New("a sub-agent start cannot be refused: `expect:` belongs on `subagent: stop`")
	}
	for k := range m {
		if !runnerKeys[k] && k != "kind" && s.Type != StepEvent {
			return s, fmt.Errorf("unknown key %q", k)
		}
	}
	return s, nil
}

// keys returns a map's keys sorted, for stable messages.
func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
