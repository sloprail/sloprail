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
	decls, invalid, err := guardrail.New(dotDir(p.Cwd)).LoadWith(reg)
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), "sloprail:", err)
	}

	// Written to stderr for a person tailing logs. Note this alone does NOT reach
	// the agent: at this hook point a harness forwards stderr only when the hook
	// exits non-zero, so a diagnostic printed beside a permitted action is
	// swallowed. That is why it is not the whole answer — see refuseForBroken.
	reportInvalid(cmd, invalid)

	if len(decls) == 0 && len(invalid) == 0 {
		// Nothing declared, or nothing readable. Either way there is no rule to
		// enforce, and an engine that refused here would be refusing on its own
		// behalf rather than on any project's.
		return nil
	}

	// Only the modules something actually binds to. Producing an event nobody
	// asked for is work done to be discarded.
	//
	// The broken declarations are included. Their bindings are exactly what has
	// stopped being enforced, and the events they named are the ones whose
	// occurrence has to be noticed in order to say so — leaving them out would
	// mean the one case that must be reported is the one case no event is
	// produced for.
	var bound []string
	for _, d := range decls {
		if d.IsEnabled() {
			bound = append(bound, d.BoundKinds()...)
		}
	}
	for _, iv := range invalid {
		bound = append(bound, iv.AffectedKinds()...)
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

		// Before any rule is consulted: if a declaration that WOULD have guarded
		// this event could not be loaded, this action is one the project believes
		// is guarded and is not. Say so by refusing it.
		if reason, broken := refuseForBroken(invalid, e.Kind); broken {
			return deny(cmd, reason)
		}

		for _, d := range decls {
			if !d.IsEnabled() {
				continue
			}

			for _, b := range d.Hooks[e.Kind] {
				m, err := guardrail.CompileMatcherFor(b.Matcher, kindDecl)
				if err != nil {
					// Not reachable for a declaration in `decls`: the identical
					// compile runs at load, and a matcher that fails it makes the
					// whole declaration Invalid, which refuseForBroken above has
					// already turned into a refusal. Kept because this is the
					// place the compile actually happens, and a second opinion
					// that disagreed with the load check would otherwise decide
					// enforcement silently.
					//
					// Refuses rather than skipping, so that if the two ever do
					// disagree the answer is a stopped action and a diagnostic,
					// not a rule that quietly went away — which is exactly the
					// defect this whole file was corrected for.
					fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: guardrail %q: %v\n", d.Name, err)
					return deny(cmd, fmt.Sprintf(
						"guardrail %q has a matcher that will not compile against %s: %v. "+
							"The action was refused because a rule that cannot be checked must not be read as approval.",
						d.Name, e.Kind, err))
				}
				admitted, err := m.Match(e)
				if err != nil {
					// A matcher that cannot be evaluated is the engine unable to
					// ANSWER whether this rule applies — not the rule being
					// satisfied. Skipping the binding here made the two
					// indistinguishable, and did it on a channel nobody reads: at
					// this hook point stderr beside a permitted action reaches
					// neither the agent nor the transcript.
					//
					// The same rule the hook side already follows. A hook that
					// cannot run refuses; a matcher that cannot be evaluated is
					// the identical failure one step earlier in the same
					// mechanism, and a guardrail must not have a path where the
					// machinery breaking reads as consent.
					//
					// Scoped to this binding's event, like every other refusal
					// here — an unanswerable expression about commands must not
					// halt a write no rule was written about.
					fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: guardrail %q: %v\n", d.Name, err)
					return deny(cmd, fmt.Sprintf(
						"guardrail %q could not decide whether it applies to this %s: %v. "+
							"The action was refused because a matcher that cannot be evaluated is not the same as a rule that was satisfied. "+
							"Fix the matcher, or disable the guardrail with `enabled: false` if it is not ready.",
						d.Name, e.Kind, err))
				}
				if !admitted {
					continue
				}

				v, err := runHooks(d, b, e)
				if err != nil {
					// The third member of the same family, and the one whose own
					// comment already said so: runHooks returns an error when the
					// hook could not be STARTED, noting that is "not something to
					// proceed through either" — and then the caller proceeded
					// through it. A hook that exits 126 refuses (see 004); a hook
					// the OS would not launch at all is a strictly earlier failure
					// of the same mechanism and cannot mean less.
					//
					// Also covers a hook type this engine does not understand,
					// which reaches here for the same reason and with the same
					// consequence: nothing was asked, so nothing approved.
					//
					// DO NOT DELETE THIS BRANCH ON THE STRENGTH OF ITS COVERAGE.
					// No e2e reaches it, and that is a fact about the load check
					// rather than about this code: an unknown hook type is refused
					// at load, and `sh -c` starts even when the command inside it
					// does not — a missing or non-executable script comes back as
					// 127 or 126, which is an ExitError and a refusal, not this.
					// It is what stands here if either of those stops holding.
					fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: guardrail %q: %v\n", d.Name, err)
					return deny(cmd, fmt.Sprintf(
						"guardrail %q could not run its hook for this %s: %v. "+
							"The action was refused because a guardrail that cannot run must not be read as approval.",
						d.Name, e.Kind, err))
				}
				if v.Refused {
					return deny(cmd, fmt.Sprintf("%s (%s)", v.Reason, d.Name))
				}
			}
		}
	}
	return nil
}

// reportInvalid says which declarations were not loaded and why, one line per
// fault.
//
// Shared by every hook point that loads, so the wording an author sees at a
// write is the wording they saw at session start. Two copies of this reported
// the same fault differently once, which is how a person comes to believe they
// are two faults.
//
// One line per fault rather than all of them joined: validation reports
// everything at once so an author can fix a declaration in one pass, and running
// them together undoes that.
func reportInvalid(cmd *cobra.Command, invalid []guardrail.Invalid) {
	for _, iv := range invalid {
		fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: guardrail %q not loaded:\n", iv.Name)
		for _, reason := range iv.Reasons {
			fmt.Fprintf(cmd.ErrOrStderr(), "  - %s\n", reason)
		}
	}
}

// refuseForBroken decides whether a declaration that failed to load should stop
// this particular event, and what to say about it.
//
// # Why this refuses rather than merely warning
//
// A declaration fault means the rule could never fire for anyone, so there is no
// hook to dispatch and nothing to ask. The tempting reading is that this leaves
// nothing to refuse ON BEHALF OF, and that a diagnostic is therefore enough. It
// is not, for a reason that is a property of the hook point rather than of the
// rule: at PreToolUse a harness forwards a hook's stderr to the agent only when
// the hook exits non-zero. A diagnostic printed alongside a permitted action
// reaches a log nobody is reading and reaches the agent not at all. "Warn and
// proceed" is therefore not a gentler enforcement — it is the silence, with a
// line of code that looks like it addressed the problem.
//
// So the project's own governing rule applies one level up. A HOOK that cannot
// run is a refusal, because the mechanism failing must not read as approval. A
// RULE that cannot load is the same failure earlier in the same mechanism, and
// an author who wrote `paht` believes their writes are guarded. Letting the
// write through tells them they were right.
//
// # Why it is scoped to the affected kinds
//
// Refusing everything would be the mistake guardrail.Fault warns about, one door
// along: a typo in a rule about commands would block a write no rule was ever
// written about, and the only way out would be deleting the rule. So a broken
// declaration stops exactly the events it bound to and nothing else. A project
// whose only broken rule is about commands writes files freely.
//
// A declaration too malformed to parse names no kinds, and so stops nothing.
// That is deliberate: see Invalid.AffectedKinds. It is also the one case where
// this is genuinely a warning, and it is the right one to be lenient about —
// there is no evidence of what it was guarding, and guessing would mean blocking
// every action in the project on the strength of an unreadable file.
func refuseForBroken(invalid []guardrail.Invalid, kind string) (string, bool) {
	for _, iv := range invalid {
		for _, k := range iv.AffectedKinds() {
			if k != kind {
				continue
			}
			// Every fault, not the first — the same reason Validate reports them
			// together. An author fixing this should need one pass, not one
			// refused write per mistake.
			return fmt.Sprintf(
				"guardrail %q is bound to %s but could not be loaded, so it is not guarding this action: %s. "+
					"The action was refused because a guardrail that cannot load must not be read as approval — "+
					"fix the declaration in %s, or disable it with `enabled: false` if it is not ready.",
				iv.Name, kind, iv.Reason, iv.Name), true
		}
	}
	return "", false
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
