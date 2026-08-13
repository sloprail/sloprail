package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/guardrail"
	"github.com/sloprail/sloprail/internal/module"
)

// newSessionPreToolCmd is the hook point that fires before a tool call runs.
//
// It is the only one that can refuse an action before it happens, which makes
// it where rules belong whose value is preventing work rather than auditing it.
func newSessionPreToolCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pre-tool",
		Short: "Before a tool call: run the guardrails bound to what it would do",
		Args:  cobra.NoArgs,
		RunE:  runSessionPreTool,
	}
}

func runSessionPreTool(cmd *cobra.Command, _ []string) error {
	p := readPayload(cmd)

	decls, _, err := guardrail.New(dotDir(p.Cwd)).Load()
	if err != nil || len(decls) == 0 {
		// Nothing declared, or nothing readable. Either way there is no rule to
		// enforce, and an engine that refused here would be refusing on its own
		// behalf rather than on any project's.
		return nil
	}

	reg, err := registry()
	if err != nil {
		return nil
	}

	// Only the modules something actually binds to. Producing an event nobody
	// asked for is work done to be discarded.
	var bound []string
	for _, d := range decls {
		if d.IsEnabled() {
			bound = append(bound, d.BoundKinds()...)
		}
	}

	in := module.Input{
		module.InputPhase:   module.PhasePre,
		module.InputPayload: p,
	}

	var events []event.Event
	for _, m := range reg.Needed(bound) {
		evs, err := m.Extract(in)
		if err != nil {
			// One module failing is not the project's rule failing. Say so and
			// carry on with what the others found.
			fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: module %q: %v\n", m.Name(), err)
			continue
		}
		events = append(events, evs...)
	}

	for _, e := range events {
		for _, d := range decls {
			if !d.IsEnabled() {
				continue
			}
			for _, b := range d.Hooks[e.Kind] {
				m, err := guardrail.CompileMatcher(b.Matcher)
				if err != nil {
					// A matcher that will not compile disables its binding and
					// says so. Treating it as "matches everything" would turn a
					// typo into a rule that refuses all work; treating it as
					// "matches nothing" would turn one into a rule that quietly
					// went away.
					fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: guardrail %q: %v\n", d.Name, err)
					continue
				}
				admitted, err := m.Match(e)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: guardrail %q: %v\n", d.Name, err)
					continue
				}
				if !admitted {
					continue
				}

				refusal, err := runHooks(d, b, e)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: guardrail %q: %v\n", d.Name, err)
					continue
				}
				if refusal != "" {
					return deny(cmd, fmt.Sprintf("%s (%s)", refusal, d.Name))
				}
			}
		}
	}
	return nil
}

// runHooks runs a binding's hooks against one event, returning the first
// refusal. An empty string means every hook permitted the work.
func runHooks(d guardrail.Declaration, b guardrail.Binding, e event.Event) (string, error) {
	payload, err := json.Marshal(map[string]any{
		"event":        e,
		"guardrailDir": d.Dir,
	})
	if err != nil {
		return "", err
	}

	for _, h := range b.Hooks {
		if h.Type != guardrail.HookCommand {
			return "", fmt.Errorf("hook type %q not understood", h.Type)
		}

		c := exec.Command("sh", "-c", h.Command)
		c.Dir = d.Dir
		c.Stdin = strings.NewReader(string(payload))
		out, err := c.Output()
		if err == nil {
			continue // permitted
		}
		if _, isExit := err.(*exec.ExitError); !isExit {
			return "", fmt.Errorf("run %q: %w", h.Command, err)
		}

		// A non-zero exit is a refusal. What it wrote says why.
		var res struct {
			Reason string `json:"reason"`
		}
		if jsonErr := json.Unmarshal(out, &res); jsonErr == nil && res.Reason != "" {
			return res.Reason, nil
		}
		return strings.TrimSpace(string(out)), nil
	}
	return "", nil
}
