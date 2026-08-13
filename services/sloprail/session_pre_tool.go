package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
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
	// the agent: this process exits 0 whenever it permits, and at exit 0 nothing
	// on either stream is forwarded. A diagnostic printed beside a permitted
	// action is swallowed. That is why it is not the whole answer — see
	// refuseForBroken, which documents the measured channel table.
	reportInvalid(cmd, invalid)

	if len(decls) == 0 && len(invalid) == 0 {
		// Nothing declared, or nothing readable. Either way there is no rule to
		// enforce, and an engine that refused here would be refusing on its own
		// behalf rather than on any project's.
		return nil
	}

	// A declaration that could not be PARSED is decided before any event is
	// extracted, because it can never be decided after one.
	//
	// The kind-scoped refusal below rides on events: a broken rule is noticed
	// when an event of a kind it bound to occurs. An unparseable file binds
	// nothing — there are no bindings to read off a file that did not parse — so
	// it contributes no kinds, no module is asked to extract for it, and the
	// event loop it would be caught in never runs. Leaving it there made the
	// most broken declaration in the project the only silent one. See
	// refuseForBroken for why the answer is to refuse everything rather than to
	// warn.
	if reason, broken := refuseForUnreadable(invalid); broken {
		return deny(cmd, reason)
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

	// What this session has already judged. Opened once for the whole
	// dispatch, and left nil when the session cannot be identified: an engine
	// that could not find its record must re-judge, never exempt.
	rev, err := openRevalidation(p)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: session state unavailable, judging everything afresh: %v\n", err)
	}
	defer rev.Close()

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

		// What content this event is about, asked once for the event because
		// the content is the same whoever is about to judge it. Whether it has
		// ALREADY been judged is asked per guardrail below — one stream of
		// events serves every rule, and a file one rule has passed is a file
		// another may never have seen.
		//
		// Hoisting is correct HERE and is not a policy the Post side may copy.
		// A guardrail's hook is an arbitrary script and may rewrite the very
		// file this event is about, so an answer reused across the loop is only
		// safe while nothing a hook does can move it. That holds on this path
		// for one reason: the only Pre kind that can produce a subject is
		// PreFileCreate, whose content comes off the EVENT and reads no disk.
		// A Post subject is fingerprinted FROM disk, so the same hoist there
		// would record rule B's verdict against rule A's fingerprint — which is
		// why the Post dispatch resolves per guardrail instead. Subject is a
		// pure function of the event and the cwd, so it is free to be called
		// either way; this file's choice binds only this file.
		subj, fingerprinted := rev.Subject(e, p.Cwd)

		for _, d := range decls {
			if !d.IsEnabled() {
				continue
			}

			// This session is running underneath THIS rule's own hook: the rule
			// launched an agent, and that agent is now doing the work it was
			// launched to do. Enforcing here is the rule re-entering itself,
			// which is the recursion — measured to depth 8 with nothing in the
			// engine ending it.
			//
			// Scoped to this one declaration, and that scoping is the design.
			// Every OTHER rule in this loop still runs against this agent's
			// writes, because a judging agent that edits files is still an agent
			// editing the project's files. Skipping the whole loop instead would
			// be simpler and would turn the launched agent — the one nobody is
			// watching — into the only unguarded actor in the project.
			//
			// Placed before the matcher compiles rather than after, so a rule
			// that does not apply here costs nothing and cannot refuse on a
			// matcher fault it was never going to be asked about.
			if isLaunchedBy(os.Getenv, d.Name) {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"sloprail: guardrail %q not enforced here — this session was launched by its own hook (%s)\n",
					d.Name, LaunchedByEnv)
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
					// indistinguishable, and did it on a channel nobody reads:
					// beside a PERMITTED action neither stream reaches the agent
					// or the transcript, because permitting means exiting 0 and
					// no channel delivers at exit 0. See refuseForBroken for the
					// measured table.
					//
					// Two things reach here. A matcher whose expression errors at
					// run time — an unchecked predicate body meeting a nil, which
					// commandmod's element-less `invocations` still permits. And
					// a declared field the producer carried at the WRONG TYPE,
					// which guardrail.fill refuses to answer for rather than
					// zeroing: `path` sent as a number has no truth value against
					// `startsWith`, and inventing one in either direction is the
					// engine deciding enforcement on its own account.
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

				// This guardrail has already seen this exact content and let it
				// through. Asking again is not merely waste: a judge hook is a
				// model call rather than a function, so a second look can return
				// a different answer and block the agent for work it already
				// fixed and can no longer reach.
				if fingerprinted && rev.Skip(d.Name, subj) {
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
					// This branch IS reachable, and an earlier version of this
					// comment claimed it was not — on the reasoning that `sh -c`
					// starts even when the command inside it does not, so a
					// missing or non-executable script comes back as 126 or 127,
					// an ExitError and a refusal rather than this. That reasoning
					// is sound and incomplete: it accounts for the command INSIDE
					// the shell failing, and says nothing about the exec of the
					// shell itself failing. Two ordinary inputs do exactly that,
					// and neither is caught at load:
					//
					//   - A NUL byte in the command. `command: "echo hi\x00there"`
					//     is a valid double-quoted YAML scalar, and checkExecutable
					//     does not judge it because `echo` is not a path reference,
					//     so the declaration loads SOUND. exec then fails with
					//     `fork/exec /bin/sh: invalid argument`, an *fs.PathError.
					//   - A guardrail directory that has gone away between load and
					//     dispatch, which fails as `chdir ...: no such file or
					//     directory`. Also not an ExitError.
					//
					// Covered by T014_06, which drives the first end to end. Before
					// that test the mutation "make cannot-start permit" survived
					// the whole suite: the one branch the comment excused from
					// coverage was the one branch with none.
					//
					// Nothing is recorded on this path, and that is the same
					// judgement from the revalidation side: the hook reached no
					// verdict, so writing a pass would exempt content nobody
					// judged, and writing a refusal would blame the rule for the
					// machine.
					fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: guardrail %q: %v\n", d.Name, err)
					return deny(cmd, fmt.Sprintf(
						"guardrail %q could not run its hook for this %s: %v. "+
							"The action was refused because a guardrail that cannot run must not be read as approval.",
						d.Name, e.Kind, err))
				}

				if fingerprinted {
					// Recorded whichever way it went. The pass is what lets the
					// next cycle skip; the refusal is what makes the violation
					// resurface every cycle until the content changes or the
					// hook permits it.
					if err := rev.Record(d.Name, subj, !v.Refused); err != nil {
						fmt.Fprintln(cmd.ErrOrStderr(), err)
					}
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
// rule: there is no channel out of a PreToolUse hook that delivers text to the
// agent without ALSO refusing the action.
//
// Measured through the harness this repo's e2e suite drives, one channel per
// run, checking both whether the text reached the agent's stream and whether the
// action still went through:
//
//	channel                              reaches agent   refuses
//	stdout, exit 0                       no              no
//	stderr, exit 0                       no              no
//	stderr, exit 1                       no              no
//	stderr, exit 2                       YES             YES
//	stderr, exit 3 / 126 / 127           no              no
//	stdout, exit 2                       no              YES
//	systemMessage, exit 0                no              no
//	additionalContext, exit 0            no              no
//	permissionDecision "deny", exit 0    YES             YES
//	permissionDecision "ask", exit 0     no              no
//
// So the earlier wording here — "forwards stderr when the hook exits non-zero" —
// was wrong twice. It is exit 2 specifically, not any non-zero status: exits 1,
// 3, 126 and 127 are swallowed exactly like exit 0. And it omitted the second
// delivering channel entirely, the JSON `permissionDecision`, which is the one
// this engine actually uses — see deny in hookio.go.
//
// The conclusion is unchanged, and is what matters: both delivering channels
// refuse. A diagnostic printed alongside a PERMITTED action reaches a log nobody
// is reading and reaches the agent not at all. "Warn and proceed" is therefore
// not a gentler enforcement — it is the silence, with a line of code that looks
// like it addressed the problem. Refusing is not a choice made in preference to
// warning; it is the only way the warning is delivered at all.
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
// A declaration that could not be PARSED names no kinds and so never matches
// here. It is handled before extraction instead — see refuseForUnreadable.
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

// refuseForUnreadable decides what to do about a declaration that could not be
// parsed at all, and what to say about it. Unlike refuseForBroken this is not
// scoped to a kind: it refuses every action, and the rest of this comment is why.
//
// Such a declaration names no kinds. AffectedKinds reads the bindings off the
// problems, and a file that failed to parse has no bindings to read — correctly
// so, and deliberately: it cannot invent a scope it has no evidence for.
//
// An earlier version of this engine concluded from that it "stops nothing", and
// called it "the one case where this is genuinely a warning" and "the right one
// to be lenient about". Given the channel table in refuseForBroken, that was not
// leniency. It was silence. No warning channel reaches the agent, so a
// declaration too broken to read was announced once at session start and then
// said nothing at any action for the rest of the session — exactly the failure
// mode this file was corrected for, preserved for the case where the file is
// MOST broken. The more broken the file, the quieter the engine got.
//
// The scoping argument that justifies narrowing elsewhere does not merely fail
// to apply here; it inverts:
//
//   - Elsewhere the scope is EVIDENCE. A rule bound to PreFileDelete tells us it
//     was about deletions, so refusing writes would block work no rule was ever
//     written about, and the author keeps a way out that is not deleting their
//     rule.
//   - Here there is no evidence, because there is no readable file. "No evidence
//     of what it guarded" is not "evidence it guarded nothing", and treating the
//     first as the second is the fail-open in its purest form: the project keeps
//     a file it believes is a guardrail, and the engine decides on its own that
//     an unreadable file guards nothing.
//
// The cost of being wrong runs one way. Refusing too broadly is loud, immediate,
// names the file, and is cleared by fixing the frontmatter or removing the
// folder — the refusal says both, because a refusal an author cannot clear is a
// trap. Permitting too broadly is silent, and is discovered when something that
// should have been guarded was not.
//
// Note the agent is not locked out of the fix: editing the broken GUARDRAIL.md
// is itself a write, and a write is refused by a rule that is bound to it —
// which this one, being unreadable, is not bound to anything. T013_08 pins that.
func refuseForUnreadable(invalid []guardrail.Invalid) (string, bool) {
	for _, iv := range invalid {
		if !iv.Has(guardrail.ErrMalformed) {
			continue
		}
		return fmt.Sprintf(
			"guardrail %q could not be read at all, so there is no way to know what it was guarding: %s. "+
				"Every action is refused while it cannot be parsed, because a file the project keeps as a guardrail "+
				"must not be read as approval merely for being unreadable — "+
				"fix the declaration in %s, or remove that folder if it is not a guardrail.",
			iv.Name, iv.Reason, iv.Name), true
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
		// The provenance this hook passes on. Everything the hook spawns
		// inherits it — including, when the hook runs sr-agent, the harness it
		// execs and that agent's own hooks. That inheritance across the exec is
		// the entire mechanism: it is what lets the engine one level down know
		// which rule it is running underneath.
		c.Env = hookEnv(d.Name)
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
