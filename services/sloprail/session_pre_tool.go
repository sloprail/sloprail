package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/guardrail"
	"github.com/sloprail/sloprail/internal/module"
	"github.com/sloprail/sloprail/internal/module/modules"
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

	reg, err := modules.Registry()
	if err != nil {
		return nil
	}

	// LoadWith, so a declaration that cannot do what it says never reaches
	// enforcement. Without it a matcher naming a field its kind does not carry
	// would be compiled here and quietly admit nothing.
	decls, _, err := guardrail.New(dotDir(p.Cwd)).LoadWith(reg)
	if err != nil || len(decls) == 0 {
		// Nothing declared, or nothing readable. Either way there is no rule to
		// enforce, and an engine that refused here would be refusing on its own
		// behalf rather than on any project's.
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
		// The kind is in hand here, so every matcher below is compiled against
		// the fields it will actually see — the same check the declaration
		// already passed at load. Compiling without it would leave enforcement
		// running an expression nobody had checked.
		kindDecl, known := reg.KindDeclFor(e.Kind)
		if !known {
			// An event from a module the registry does not own cannot be
			// matched against anything. It should not be reachable, since the
			// events came from this registry's own modules.
			continue
		}

		for _, d := range decls {
			if !d.IsEnabled() {
				continue
			}

			for _, b := range d.Hooks[e.Kind] {
				m, err := guardrail.CompileMatcherFor(b.Matcher, kindDecl)
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

				v, err := runHooks(d, b, e)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: guardrail %q: %v\n", d.Name, err)
					continue
				}
				if v.Refused {
					return deny(cmd, fmt.Sprintf("%s (%s)", v.Reason, d.Name))
				}
			}
		}
	}
	return nil
}

// verdict is what one binding's hooks concluded.
//
// Refusal is a field rather than a non-empty reason string. Encoding "refused"
// as "said something" is what made a refusal with nothing to say read as
// consent, and no wording of the reason can distinguish the two — only a
// separate field can.
type verdict struct {
	// Refused reports whether the work must not proceed.
	Refused bool

	// Reason is what to tell the agent. Never empty when Refused: a hook that
	// gives no usable reason gets one synthesised, because the alternative is
	// refusing with nothing to act on.
	Reason string
}

// Exit statuses a shell uses to say it could not run the command at all, as
// opposed to the command running and choosing to fail. A person debugging the
// first needs to hear about their file; a person debugging the second needs to
// hear from their hook.
const (
	exitNotExecutable = 126
	exitNotFound      = 127
)

// runHooks runs a binding's hooks against one event, stopping at the first
// refusal.
//
// The governing rule: a non-zero exit is NEVER consent. A hook that exits
// non-zero has refused, whatever it did or did not write, and whether or not it
// managed to run at all. The opposite — treating a hook that could not run, or
// that wrote its reason somewhere unexpected, as approval — turns every failure
// of the mechanism into silent permission, which is the one failure mode a
// guardrail must not have.
func runHooks(d guardrail.Declaration, b guardrail.Binding, e event.Event) (verdict, error) {
	payload, err := json.Marshal(map[string]any{
		"event":        e,
		"guardrailDir": d.Dir,
	})
	if err != nil {
		return verdict{}, err
	}

	for _, h := range b.Hooks {
		if h.Type != guardrail.HookCommand {
			return verdict{}, fmt.Errorf("hook type %q not understood", h.Type)
		}

		// Both streams are captured. A hook writing its refusal to stderr is
		// using an ordinary shell idiom, not making a mistake, and reading only
		// stdout threw those refusals away.
		var stdout, stderr bytes.Buffer
		c := exec.Command("sh", "-c", h.Command)
		c.Dir = d.Dir
		c.Stdin = strings.NewReader(string(payload))
		c.Stdout = &stdout
		c.Stderr = &stderr

		err := c.Run()
		if err == nil {
			continue // permitted: exit zero, and silence is consent
		}

		exitErr, isExit := err.(*exec.ExitError)
		if !isExit {
			// The process could not be started at all. Not a refusal by the
			// rule, but not something to proceed through either.
			return verdict{}, fmt.Errorf("run %q: %w", h.Command, err)
		}

		return verdict{
			Refused: true,
			Reason:  refusalReason(h.Command, exitErr.ExitCode(), stdout.Bytes(), stderr.Bytes()),
		}, nil
	}
	return verdict{}, nil
}

// refusalReason works out what to tell the agent about a hook that exited
// non-zero. There is always something to say: this never returns "".
//
// The hook's own words win wherever it managed to say any. Failing that, the
// two statuses meaning the shell could not run the command at all get a
// diagnosis naming what to fix, since the shell's own message is accurate and
// useless. Failing even that, the exit status itself is reported — a refusal
// nobody explained still has to read as a refusal.
func refusalReason(command string, code int, stdout, stderr []byte) string {
	// What the hook meant to say, structured. Checked before the could-not-run
	// diagnoses below, because a hook is free to exit 126 or 127 on its own
	// account, and when it has stated a reason that reason is the rule speaking.
	var res struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(stdout, &res); err == nil && strings.TrimSpace(res.Reason) != "" {
		return strings.TrimSpace(res.Reason)
	}

	// The shell could not run the command at all. Its exit status is the
	// shell's, not the rule's, and its message ("Permission denied") is true
	// but tells nobody what to do — so state the diagnosis outright.
	switch code {
	case exitNotExecutable:
		return fmt.Sprintf("guardrail hook %q could not be run: it is not executable (chmod +x it, and check its interpreter line). The action was refused because a guardrail that cannot run must not be read as approval.%s",
			command, quoted(stderr))
	case exitNotFound:
		return fmt.Sprintf("guardrail hook %q was not found. The command is resolved relative to the guardrail's own folder. The action was refused because a guardrail that cannot run must not be read as approval.%s",
			command, quoted(stderr))
	}

	// Plain prose on either stream. Stdout first, because a hook that writes
	// there is answering; stderr next, because a hook that refuses with
	// `echo ... >&2; exit 1` is using an ordinary idiom and means it.
	//
	// Parseable JSON carrying no reason is not prose — printing it raw would
	// hand the agent an implementation detail instead of an instruction.
	if text := plainText(stdout); text != "" {
		return text
	}
	if text := plainText(stderr); text != "" {
		return text
	}

	// It said nothing usable. Refuse anyway, and say enough that whoever wrote
	// the hook can find it — the alternative is letting the work through.
	return fmt.Sprintf("guardrail hook %q refused (exit %d) but gave no reason", command, code)
}

// quoted appends what the shell said, when it said anything, so the underlying
// message is not lost behind the diagnosis.
func quoted(stderr []byte) string {
	text := strings.TrimSpace(string(stderr))
	if text == "" {
		return ""
	}
	return " (" + text + ")"
}

// plainText returns trimmed output that is meant to be read by a person, or ""
// if there is nothing usable there. JSON is excluded: it is either a structured
// verdict already handled, or a detail the agent should not be shown raw.
func plainText(b []byte) string {
	text := strings.TrimSpace(string(b))
	if text == "" || json.Valid([]byte(text)) {
		return ""
	}
	return text
}
