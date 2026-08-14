package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/guardrail"
	"github.com/sloprail/sloprail/internal/harness"
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
	store, unresolved, err := guardrailStore(p.Cwd)
	if err != nil {
		// Discovery itself failed — a settings file that exists and would not
		// read or parse, so which plugins are in force is unknown.
		//
		// Reported and permitted, matching what the unlistable store below now
		// does and for the same reason: this is a fault in a file the AGENT did
		// not write and often cannot repair, and refusing every action would
		// leave nobody able to fix it — including the person trying to correct
		// the very JSON at fault. An invalid guardrail blocks nothing, and an
		// unreadable statement about which guardrails exist is that same case
		// one level up.
		//
		// The honesty is carried by being loud about the actual consequence: not
		// "something went wrong" but that nothing from any plugin is guarding.
		fmt.Fprintf(cmd.ErrOrStderr(),
			"sloprail: the plugins this project has enabled could not be determined, so NO plugin's "+
				"guardrails are in force: %v. Fix the JSON in .claude/settings.json or "+
				".claude/settings.local.json.\n",
			err)
		return nil
	}
	// Every enabled plugin that could not be found, named. This is what keeps a
	// moved cache layout or a bumped manifest schema from silently disabling
	// every shipped guardrail — see harness.Unresolved.
	reportUnresolved(cmd, unresolved)

	res, err := store.Resolve(reg)
	decls, invalid := res.Declarations, res.Invalid
	if err != nil {
		// The STORE itself could not be read — the folder holding every
		// declaration would not list. One level above any fault the loader
		// reports: there is no declaration to call Invalid, because nothing
		// could be enumerated.
		//
		// This used to print and carry on with nothing loaded, which fell into
		// the "nothing declared, or nothing readable" branch below — and those
		// are not the same thing. It is refuseForUnreadable's argument one level
		// up, and it inverts the same way: "no evidence of what was guarded" is
		// not "evidence nothing was guarded". A project whose .sloprail folder
		// has bad permissions, or sits on a mount that has gone away, still
		// holds every file it believes is a guardrail.
		//
		// A project that has adopted NO guardrails is untouched, and that is
		// what keeps this from breaking every repository that does not use
		// sloprail: a missing directory is os.IsNotExist, which LoadWith answers
		// with (nil, nil, nil) and never reaches here.
		// Reported and permitted, for the same reason a single unparseable
		// declaration is: this is the guardrail author's fault, the agent cannot
		// repair a directory it cannot list, and refusing every action leaves
		// nobody able to fix anything. A store that will not list is louder than
		// one broken file, not different in kind.
		fmt.Fprintf(cmd.ErrOrStderr(),
			"sloprail: the guardrails in this project could not be read at all, so NOTHING is being guarded: %v. "+
				"Fix the permissions on the guardrails directory, or remove it if the project has no guardrails.\n",
			err)
		return nil
	}

	// Written to stderr for a person tailing logs. Note this alone does NOT reach
	// the agent: this process exits 0 whenever it permits, and at exit 0 nothing
	// on either stream is forwarded. A diagnostic printed beside a permitted
	// action is swallowed. That is why it is not the whole answer — see
	// refuseForBroken, which documents the measured channel table.
	reportInvalid(cmd, invalid)
	reportShadowed(cmd, res.Shadowed)

	if len(decls) == 0 && len(invalid) == 0 {
		// Nothing declared, or nothing readable. Either way there is no rule to
		// enforce, and an engine that refused here would be refusing on its own
		// behalf rather than on any project's.
		return nil
	}

	// A declaration that could not be PARSED needs no handling of its own here,
	// and this comment says why rather than leaving the absence to look like an
	// oversight — because for one revision it was one.
	//
	// The old refusal had to sit at this point in the function. It could never
	// ride on events: the kind-scoped path below is reached when an event of a
	// kind the declaration bound to occurs, and an unparseable file binds
	// nothing — there are no bindings to read off a file that did not parse — so
	// it contributes no kinds, no module is asked to extract for it, and the
	// event loop never sees it. That is why refuseForUnreadable existed as a
	// separate pre-extraction step, and why deleting the refusal left an
	// apparent hole exactly here.
	//
	// The hole is apparent rather than real. reportInvalid above is not scoped to
	// kinds at all — it walks the whole invalid list and prints every fault on it,
	// and a malformed declaration is on that list carrying its parse error as its
	// reason. Verified by running this function against a declaration with no
	// frontmatter fence: stderr carries `guardrail "unreadable" not loaded:` and
	// the parse diagnostic beneath it, with no refusal on stdout. The malformed
	// case is therefore reported on exactly the same channel, in the same words,
	// and on the same dispatch as every other invalid declaration.
	//
	// So nothing is added here, and the omission is the correct code. A
	// reportUnreadable alongside reportInvalid would print one fault twice per
	// dispatch — the thing reportInvalid's own comment warns about, one file
	// over: two reports of one fault is how a person comes to believe they have
	// two faults.
	//
	// What is NOT claimed: that the malformed case is reported as LOUDLY as it
	// once was. reportBroken below names the action a rule has stopped guarding;
	// reportInvalid can only name the rule, because a file that did not parse
	// says nothing about what it would have guarded. That is a property of the
	// fault, not a gap in the reporting.

	// Only the modules something actually binds to. Producing an event nobody
	// asked for is work done to be discarded.
	//
	// The broken declarations are included. Their bindings are exactly what has
	// stopped being enforced, and the events they named are the ones whose
	// occurrence has to be noticed in order to say so — leaving them out would
	// mean the one case that must be reported is the one case no event is
	// produced for.
	bound := boundKinds(decls, invalid)

	// Who this session is, resolved ONCE for the whole dispatch and used for both
	// things that need it: the store of what has already been judged, and the
	// environment every hook is given. Two calls to stableID would be two
	// derivations free to drift, which is the thing this codebase has already had
	// to converge more than once.
	//
	// A session that cannot be identified is not a reason to refuse. The rules
	// that need no memory still work, and blocking every action because the
	// transcript could not be read would be the engine refusing on its own
	// behalf. It does mean nothing can be exempted — an engine that could not
	// find its record must re-judge, never skip — and that is what the empty id
	// below yields, since openRevalidation is not called without one.
	scope := hookScope{Workspace: p.Cwd}
	if id, err := stableID(p); err == nil {
		scope.SessionID = id
	} else {
		fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: %v\n", err)
	}
	// The record itself, for a rule that reads the trajectory rather than the
	// pending call. From the same p.record() stableID is built on, so the id and
	// the path cannot name different files.
	if path, err := p.record(); err == nil {
		scope.Transcript = path
	}

	// What this session has already judged. Opened once for the whole
	// dispatch, and left nil when the session cannot be identified: an engine
	// that could not find its record must re-judge, never exempt.
	var rev *revalidation
	if scope.SessionID != "" {
		var err error
		if rev, err = openRevalidation(scope.SessionID, p.Cwd); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: session state unavailable, judging everything afresh: %v\n", err)
		}
	}
	defer rev.Close()

	in := module.Input{
		module.InputPhase:   module.PhasePre,
		module.InputPayload: p,
	}

	var events []event.Event
	for _, m := range reg.Needed(bound) {
		evs, err := m.Extract(in)
		// The events are taken FIRST, and the error reported after.
		//
		// module.Module's contract says a module may return events ALONGSIDE a
		// non-nil error and that "a caller must not discard" them — and this
		// caller did: it printed and `continue`d past the whole slice. The Post
		// side has always been right about this, and postEvents' own comment
		// contrasts itself against this loop.
		//
		// Not a tidiness point. `rm a.md b.md` is ONE tool call producing TWO
		// targets, and one path that will not stat makes filemod return a
		// problem for that path together with a perfectly good PreFileDelete for
		// the other. Dropping the slice meant the rule guarding a.md never ran
		// and the deletion proceeded — with the only trace on a stream that at
		// exit 0 reaches no agent. One unreadable path silenced every rule about
		// every other file the same command touched.
		//
		// Reporting the error and keeping the events is not fail-open: nothing
		// here decides a verdict. The events that WERE produced go on to be
		// matched and judged exactly as they would have been, and the paths the
		// module could not classify produce no event because there is nothing
		// honest to say about them — which is the module's own judgement, made
		// where the evidence is.
		events = append(events, evs...)
		if err != nil {
			// One input a module could not make sense of is not the project's
			// rule failing. Say so, and carry on with what it and the others
			// did find.
			fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: module %q: %v\n", m.Name(), err)
		}
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

		// A declaration that WOULD have guarded this event but could not be
		// loaded is reported, not refused. This action is one the project
		// believes is guarded and is not — and saying so by halting the agent
		// punishes the party that did not write the broken file. See the block
		// comment below refuseForBroken for the full argument.
		reportBroken(cmd, invalid, e.Kind)

		// What content this event is about, asked once for the event because the
		// content is the event's fact, not any rule's. Whether it has ALREADY
		// been judged is asked per guardrail below — one stream of events serves
		// every rule, and a file one rule has passed is a file another may never
		// have seen.
		//
		// Reusing one answer across the loop is safe for a reason worth stating,
		// because a guardrail's hook is an arbitrary script and may rewrite the
		// very file this event is about: the only kind that can produce a subject
		// here is PreFileCreate, whose subject comes from the EVENT and reads no
		// disk. So nothing a hook does between one rule and the next can move it.
		// That purity is pinned in revalidation_test.go, not assumed here.
		//
		// Which makes the hoist this path's own, not a policy the Post side may
		// copy. A Post subject is fingerprinted FROM disk, so hoisting there
		// would record rule B's verdict against rule A's fingerprint — the Post
		// dispatch resolves per guardrail for exactly that reason. Subject is a
		// pure function of the event and the cwd, so it serves either policy;
		// this file's choice binds only this file.
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
						"guardrail %s has a matcher that will not compile against %s: %v. "+
							"The action was refused because a rule that cannot be checked must not be read as approval.",
						d.Attribution(), e.Kind, err))
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
						"guardrail %s could not decide whether it applies to this %s: %v. "+
							"The action was refused because a matcher that cannot be evaluated is not the same as a rule that was satisfied. "+
							"Fix the matcher, or disable the guardrail with `enabled: false` if it is not ready.",
						d.Attribution(), e.Kind, err))
				}
				if !admitted {
					continue
				}

				// This guardrail has already seen this exact content and let it
				// through. Asking again is not merely waste: a judge hook is a
				// model call rather than a function, so a second look can return
				// a different answer and block the agent for work it already
				// fixed and can no longer reach.
				//
				// Asked per guardrail, against the one subject resolved above for
				// the whole event. The content is the same whoever is judging it,
				// but whether it has ALREADY been judged is each rule's own fact —
				// a file one rule has passed is a file another may never have seen.
				if fingerprinted {
					skip, err := rev.Skip(d.Name, subj)
					if err != nil {
						// Reported, never acted on. The false is already the safe
						// answer; saying so is what keeps a session that has
						// silently lost its record from looking like one that
						// simply has nothing settled.
						fmt.Fprintln(cmd.ErrOrStderr(), err)
					}
					if skip {
						continue
					}
				}

				v, err := runHooks(d, b, e, scope)
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
						"guardrail %s could not run its hook for this %s: %v. "+
							"The action was refused because a guardrail that cannot run must not be read as approval.",
						d.Attribution(), e.Kind, err))
				}

				if fingerprinted {
					// Recorded whichever way it went, and the refusal is the half
					// that is easy to lose: it is written BEFORE the deny below
					// returns, so the early exit cannot skip past it.
					//
					// Dropping it would not look like a bug from here — a refusal
					// denies immediately either way, so this cycle is identical.
					// The cost lands a cycle later: with no row, the content is
					// unjudged rather than refused, and the FIRST thing that
					// records a pass for it is believed. Recording it is what
					// makes the row fail the passing half of the exemption for as
					// long as the content stays as it is.
					if err := rev.Record(d.Name, subj, !v.Refused); err != nil {
						fmt.Fprintln(cmd.ErrOrStderr(), err)
					}
				}

				if v.Refused {
					// The rule's own words, then which rule said them — and,
					// when it is not this project's rule, which plugin it came
					// from.
					//
					// The plugin half is not decoration. A refusal names a
					// guardrail so the reader can go and look at it, and for a
					// shipped rule the name alone points at
					// .sloprail/guardrails/<name>, where there is nothing: the
					// file is inside an install cache the project never wrote to.
					// Naming the plugin is what turns "a rule I have never heard
					// of blocked me" into "this came from sloprail, and I can
					// switch it off or go and read it".
					return deny(cmd, fmt.Sprintf("%s (%s)", v.Reason, d.Attribution()))
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
		fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: guardrail %s not loaded:\n", iv.Attribution())
		for _, reason := range iv.Reasons {
			fmt.Fprintf(cmd.ErrOrStderr(), "  - %s\n", reason)
		}
	}
}

// reportShadowed says which plugin rules a project's own rules displaced.
//
// Reported at every hook point that loads, for the same reason reportInvalid is:
// the wording an author meets at a write is the wording they met at session
// start, and a fact announced once at start is a fact nobody saw.
//
// Stderr is a weak channel here — at exit 0 it reaches no agent, per the table
// in refuseForBroken — and that is accepted for this one case rather than
// escalated to a refusal. Shadowing is not a broken rule: both declarations are
// well-formed, the project's is enforcing, and the project deliberately named it.
// Refusing every action because a project overrode a rule would make overriding
// impossible, which is the capability this mechanism exists to provide. The
// warning belongs where a person looking for it will find it, and the load check
// an author runs by hand prints the same thing.
func reportShadowed(cmd *cobra.Command, shadowed []guardrail.Shadow) {
	for _, sh := range shadowed {
		fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: %s\n", sh.Message())
	}
}

// reportUnresolved says which enabled plugins could not be located.
//
// This is the loud half of "isolated and loud", and the reason the engine is
// allowed to read a harness's configuration at all. Every path and schema
// assumption in internal/harness can go stale; what must not happen is that a
// stale assumption reads as "this project installed nothing", because that is
// indistinguishable from the truth and the guardrails just stop firing.
//
// So a project that SAYS it enabled a plugin, whose files cannot be found, gets
// a line naming the plugin and every path that was tried — at every hook point,
// so it is met on the next tool call rather than only at a session start nobody
// was watching.
//
// It warns rather than refuses, and harness.Unresolved carries the full
// argument. In short: a plugin that cannot be found is not a guardrail known to
// have failed — most plugins ship no guardrails at all — so blocking every
// action over one would usually block work for a rule that does not exist, and
// the only remedy would be uninstalling the plugin. A rule that EXISTS and
// cannot be checked still refuses; that is refuseForBroken, and it is unchanged.
func reportUnresolved(cmd *cobra.Command, unresolved []harness.Unresolved) {
	for _, u := range unresolved {
		fmt.Fprintf(cmd.ErrOrStderr(), "sloprail: %s\n", u.Message())
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
// here. It is not handled before extraction either, because there is no longer
// anything to handle: reportInvalid names it once per dispatch, and the comment
// at the old refuseForUnreadable call site records why that is sufficient.
//
// reportBroken announces, on stderr, that a rule bound to this kind is not
// running. It never refuses: an invalid guardrail blocks nothing.
//
// The refusal this replaces is argued at length above, and the whole of that
// argument survives except its conclusion. What changed is the weighting of the
// two failures. Refusing makes a declaration fault everyone's problem
// immediately; permitting makes it the author's problem eventually. Since the
// fault is ALWAYS the author's and never the agent's, and since the agent is
// usually not in a position to repair somebody else's declaration, the second is
// where the cost belongs.
//
// Per event kind, so the message names what has stopped being guarded rather
// than saying a rule is broken in the abstract.
func reportBroken(cmd *cobra.Command, invalid []guardrail.Invalid, kind string) {
	for _, iv := range invalid {
		for _, k := range iv.AffectedKinds() {
			if k != kind {
				continue
			}
			// Every fault, not the first — the same reason Validate reports them
			// together. An author fixing this should need one pass.
			//
			// Attributed and given an origin-appropriate remedy: a shipped rule
			// is not the consumer's to fix, and telling them to edit a file
			// inside an install cache sends them to do something the next
			// reinstall undoes. See remedy.
			fmt.Fprintf(cmd.ErrOrStderr(),
				"sloprail: guardrail %s is bound to %s but could not be loaded, so it is NOT guarding this action: %s. %s\n",
				iv.Attribution(), kind, iv.Reason, remedy(iv))
		}
	}
}

// remedy is what to tell someone whose rule could not load, and it differs by
// WHO OWNS THE FILE.
//
// For a project's own rule the advice has always been "fix the declaration, or
// disable it" and both halves are actionable: the file is in the tree, the
// author wrote it, and `enabled: false` is one line away.
//
// For a plugin's rule that advice is a trap. The declaration is in an install
// cache the consumer did not write and must not edit — an edit there is silently
// undone by the next reinstall, so an author who followed the instruction would
// fix the refusal, ship, and have it come back on upgrade with no explanation.
// And `enabled: false` lives inside the very file they should not be editing.
//
// So a plugin's rule gets the mechanism that is actually theirs: the disable
// list in their own config, named with the qualified name, which survives
// upgrades because it is on their side of the boundary. The message quotes the
// exact line to write, because advice the reader has to go and look up is advice
// they will skip.
//
// The two forms differ again by WHAT WENT WRONG, and that second axis is not
// cosmetic. A declaration that merely failed validation is a guardrail with a
// mistake in it, so the advice is to fix or disable it. A declaration that could
// not be PARSED might not be a guardrail at all — a stray file, a note someone
// left in the folder — and for that one "remove that folder if it is not a
// guardrail" is a real way out that the other case does not have.
//
// This text now accompanies a REPORT rather than a refusal: an invalid guardrail
// blocks nothing. That makes the wording matter more, not less. The only thing
// standing between a rule that silently stopped enforcing and a person who fixes
// it is this sentence, so it has to name a remedy the reader can actually
// perform.
func remedy(iv guardrail.Invalid) string {
	unreadable := iv.Has(guardrail.ErrMalformed)

	if !iv.Origin.FromPlugin() {
		if unreadable {
			return fmt.Sprintf(
				"fix the declaration in %s, or remove that folder if it is not a guardrail.", iv.Name)
		}
		return fmt.Sprintf(
			"fix the declaration in %s, or disable it with `enabled: false` if it is not ready.", iv.Name)
	}

	// A plugin's rule, where neither of the above is available: the consumer
	// cannot fix the file and must not remove a folder inside an install cache,
	// since the next reinstall would put it back. The disable list is the one
	// remedy that is genuinely theirs, so it is the one quoted — for both the
	// unreadable and the merely-invalid case.
	return fmt.Sprintf(
		"this rule is not yours to fix — it ships inside plugin %q, at %s. "+
			"Report it to that plugin, or switch it off for this project by adding `disabled: [%s]` to %s/%s.",
		iv.Origin.Plugin, iv.Origin.Root, iv.Qualified(), DotDirName, guardrail.ConfigFile)
}

// refuseForUnreadable is GONE, and this comment is kept as the record of what it
// argued and why that argument was overruled.
//
// # The rule now
//
// An invalid guardrail blocks nothing. Not the unparseable declaration this
// function was about, and not the kind-scoped faults refuseForBroken handles.
// A rule that will not load is reported at every opportunity and enforces
// nothing, and the session proceeds.
//
// # What the old argument got right
//
// The measured channel table below is still true, and it is still the strongest
// point against this change: at exit 0 nothing on either stream reaches the
// agent, so "warn and proceed" warns a log rather than a reader. That cost is
// real and is accepted rather than argued away — the diagnostic goes to stderr
// and to session start, and an agent mid-session is not told.
//
// # Why it is overruled anyway
//
// The old reasoning treated "the engine cannot tell what this file guarded" as
// equivalent to "this file might have guarded THIS action", and refused
// everything on the strength of it. Two things are wrong with that.
//
// First, the blast radius is not proportional to the evidence. A declaration
// fault is the GUARDRAIL AUTHOR's mistake, and the party it stops is the agent,
// which cannot have caused it and frequently cannot fix it. Every action in the
// session is refused because one file in a directory has a typo. A rule that
// cannot load has judged nothing; refusing on its behalf is not enforcement, it
// is an outage wearing enforcement's clothes.
//
// Second — and this is what settled it — the claim that the author is left a way
// out was FALSE, and was measured false in the session that produced this
// change. T013_08 asserts the refusal "does not lock the project out of fixing
// it", reasoning that an unreadable rule is bound to nothing and so cannot
// refuse the write that would repair it. That is not how the refusal was wired:
// it fired before extraction, on every action of every kind, so writing the
// missing GUARDRAIL.md was refused, removing the folder was refused, and `echo
// hello` was refused. The remedies the refusal text itself named were both
// unreachable from inside the session. A refusal an author cannot clear is a
// trap, by this file's own standard, and this one was one.
//
// The trade is therefore not "silence versus enforcement". It is "a silent
// unenforced rule" against "a session that cannot do anything at all, including
// fix the rule". The first fails in the direction of work continuing; the second
// fails in the direction of nothing continuing, and neither enforces the rule.
//
// # What carries the honesty instead
//
// Loudness at the moment the author can act. Session start already reports every
// invalid declaration, which is where a person who just edited one is looking,
// and reportInvalid repeats it on every dispatch for anyone tailing stderr. The
// rule that will not load is named every time rather than once.
//
// What is NOT claimed: that this is as safe as refusing. A project whose rule
// silently stopped loading is unguarded and its agent will not be told. That is
// the accepted cost, and the mitigation is that a guardrail is proven by causing
// it to fire — see the authoring skill, which says loading is not firing.
//
// # The measured channel table, kept because it remains true
//
// There is no channel out of a PreToolUse hook that delivers text to the agent
// without ALSO refusing the action. One channel per run, checking both whether
// the text reached the agent's stream and whether the action went through:
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
// Both delivering channels refuse. That is why the old code refused: it was the
// only way to be heard. The answer now is that being heard is not worth halting
// the session for, when what is being announced is that somebody's rule has a
// typo in it.

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

// hookTimeout bounds how long ONE hook may take before it is killed and read as
// a refusal.
//
// Per hook rather than per dispatch. A per-dispatch budget makes one slow rule
// starve the rules after it, so which guardrail refuses depends on declaration
// order and on how long its neighbours happened to take — a verdict that is not
// a function of the action being judged is not a verdict. Per hook costs the
// multiplication (a binding with four hooks may take four times this) and that
// is the right trade: each rule is answerable for its own time, and a slow
// dispatch is visible as several slow hooks rather than one arbitrary casualty.
//
// The number is generous because a hook may legitimately be a model call — the
// sr-agent path is exactly that, and a judge cut off mid-answer is a rule that
// works on a fast machine and refuses on a loaded one. It is also well under
// the harness's own deadline, and that ordering is the point: whichever bound
// fires first decides what the user sees, and only this one can produce a
// verdict naming the rule. If the harness killed the engine first, the engine
// would render no answer at all and the action would proceed unjudged.
const hookTimeout = 30 * time.Second

// hookKillGrace caps how long Wait may keep waiting once the process group has
// been killed. SIGKILL cannot be caught, so this is only reached by a
// descendant wedged in an uninterruptible syscall; without it, one such process
// restores the unbounded hang this whole mechanism exists to remove.
const hookKillGrace = 2 * time.Second

// runHooks runs a binding's hooks against one event, stopping at the first
// refusal.
//
// The governing rule: a non-zero exit is NEVER consent. A hook that exits
// non-zero has refused, whatever it did or did not write, and whether or not it
// managed to run at all. The opposite — treating a hook that could not run, or
// that wrote its reason somewhere unexpected, as approval — turns every failure
// of the mechanism into silent permission, which is the one failure mode a
// guardrail must not have.
func runHooks(d guardrail.Declaration, b guardrail.Binding, e event.Event, scope hookScope) (verdict, error) {
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
		ctx, cancel := context.WithTimeout(context.Background(), hookTimeout)
		c := exec.CommandContext(ctx, "sh", "-c", h.Command)
		c.Dir = d.Dir

		// A hook that never answers must stop being the session's problem.
		//
		// Every other failure here is observed as a finished process; this one
		// is the absence of an event, so nothing in the fail-open discipline
		// fires on its own. Without a deadline the engine waits as long as the
		// hook takes, which for a wedged hook is forever — and then the HARNESS
		// kills the engine, at which point sloprail has rendered no verdict and
		// the action proceeds unjudged. That is the fail-open, arriving by way
		// of the engine never getting to speak. The bound has to be here so the
		// answer is still sloprail's.
		//
		// The kill goes to the process GROUP, not the shell. A hook is a shell
		// line and the slow ones spawn something — sr-agent, a model call — and
		// those children both survive a kill aimed at the shell and hold the
		// inherited pipes open, so Wait goes on blocking and the deadline
		// achieves nothing. Setpgid gives the shell its own group for the
		// children to inherit; one signal to the negated pgid ends the tree.
		// Leaking a model-calling subprocess per guarded action is its own
		// defect, quite apart from the hang.
		c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		c.Cancel = func() error {
			if err := syscall.Kill(-c.Process.Pid, syscall.SIGKILL); err != nil {
				if errors.Is(err, syscall.ESRCH) {
					return os.ErrProcessDone
				}
				return err
			}
			return nil
		}
		c.WaitDelay = hookKillGrace
		// Which guardrail is asking, which session and tree its state is keyed
		// by, the record to read, and the provenance this hook passes on — all
		// from one place, because they are one exec's environment. The guardrail
		// travels here rather than in the argument vector: the hook is free to
		// pass it on, but it never has to name itself to reach its own entries,
		// and a rule that never names one cannot reach another's by accident.
		//
		// The provenance in particular is inherited by everything the hook
		// spawns — including, when the hook runs sr-agent, the harness it execs
		// and that agent's own hooks. That inheritance across the exec is the
		// entire mechanism: it is what lets the engine one level down know which
		// rule it is running underneath.
		c.Env = scope.env(d.Name)
		c.Stdin = strings.NewReader(string(payload))
		c.Stdout = &stdout
		c.Stderr = &stderr

		err := c.Run()
		expired := ctx.Err() != nil
		// Released here rather than by defer: this is a loop, and a deferred
		// cancel would hold every hook's context until the whole binding is
		// done.
		cancel()

		if err == nil {
			continue // permitted: exit zero, and silence is consent
		}

		// Out of time. Checked BEFORE the ExitError branch below, which would
		// otherwise report this as a refusal at "exit -1" — Go's sentinel for
		// "died by signal", not a status any process can return, and a number
		// that sends the author to debug an exit path never taken.
		//
		// It refuses, for the same reason a hook that cannot run refuses: the
		// rule never answered, and a mechanism that failed must not read as
		// approval. The objection to that is real — a rule that wedges on some
		// input now blocks that input until someone removes the rule — but the
		// alternative is an engine that permits precisely what it could not
		// judge, which is the one failure a guardrail may not have. The refusal
		// says which rule and how long, because a refusal nobody can diagnose is
		// how the wedged rule stays wedged.
		if expired {
			return verdict{
				Refused: true,
				Reason: fmt.Sprintf("guardrail hook %q was killed after %s without answering, and the action was refused because a guardrail that did not answer must not be read as approval. Make the hook decide within the deadline, or take the rule out.%s",
					h.Command, hookTimeout, quoted(stderr.Bytes())),
			}, nil
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

	// Killed by a signal. Checked BEFORE the prose branches, because on one of
	// the two ways this is reported the shell's own obituary is sitting in
	// stderr and would otherwise be read as the rule speaking.
	//
	// A hook is run as `sh -c <command>`, so there are two shapes depending on
	// whether the signal reached the shell itself or only the command it was
	// waiting on, and the shell in question differs by platform:
	//
	//   - The direct child died by signal. Go reports ExitCode() == -1, a
	//     sentinel for "no exit status" rather than a status any process can
	//     return. This is what macOS produces, where /bin/sh is bash and bash
	//     re-raises the signal so the death propagates.
	//
	//   - The shell OUTLIVED its child and reported on it. It exits 128+N — 137
	//     for SIGKILL — and writes a job-control line such as "Killed" to
	//     stderr. This is what Linux produces, where /bin/sh is dash. Measured
	//     on ubuntu-24.04: dash gives code=137, stderr="Killed\n", while bash
	//     on the same machine gives code=-1 and stderr empty.
	//
	// Both are the same fact about the world — the hook did not exit, it was
	// killed — and the author must be told that either way. Reading the second
	// shape as prose reported "Killed" to the agent as though the rule had
	// decided it, which is precisely the confusion this branch exists to
	// prevent, and it made the answer depend on which shell /bin/sh happens to
	// be rather than on what happened to the hook.
	//
	// 128+N is the POSIX convention for "terminated by signal N" and is what
	// every shell uses, so this is the portable spelling of the same question
	// rather than a special case for one of them. Only the signals that mean a
	// violent death are read this way; see killedBySignal.
	if code < 0 || killedBySignal(code) {
		return fmt.Sprintf("guardrail hook %q was killed before it answered (no exit status: a crash, an out-of-memory kill, or a signal). The action was refused because a guardrail that did not answer must not be read as approval.%s",
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

// killedBySignal reports whether a shell's exit status is its way of saying the
// command it ran was terminated by a signal.
//
// 128+N, the POSIX convention every shell follows. Deliberately NOT every code
// above 128: that range is a real exit status a script may return on its own
// account, and reading all of it as a death would relabel a hook's own refusal
// as a crash — the opposite error, and the more dangerous one, since it would
// tell an author their rule crashed when it had in fact decided.
//
// So only the signals that mean a process was killed rather than that it chose
// to stop are listed. SIGINT and SIGTERM are included because an operator's
// ^C or a supervisor's shutdown leaves a hook that did not answer, which is the
// same fact for the purposes of this message. SIGPIPE (141) is excluded: a hook
// whose stdout closed early is a plumbing accident this engine causes by
// capturing streams, and it has usually already said what it meant to say.
func killedBySignal(code int) bool {
	switch code {
	case 128 + 2, // SIGINT
		128 + 6,  // SIGABRT — an assertion or a panic in a compiled hook
		128 + 9,  // SIGKILL — a `kill -9`, or the OOM killer
		128 + 11, // SIGSEGV — a crash
		128 + 15: // SIGTERM
		return true
	}
	return false
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

// boundKinds is every event kind something in this project actually asks about.
//
// A function rather than a loop at the call site because its `enabled` half is
// otherwise untestable. Removing that half leaves the whole repository's suite
// green: a disabled guardrail's kinds would be collected, its module would run,
// and only the dispatch-time enabled check would stop the hook — so every
// observable effect is identical and the cost is the only difference. Cost is
// exactly what `extractor_runs_bound` exists to prevent, and exactly what no
// end-to-end test can see.
//
// The broken declarations are included, and NOT filtered on enabled, because
// there is no `enabled` to read off a declaration that did not parse. Their
// bindings are what has stopped being enforced, and the events they named are
// the ones whose occurrence has to be noticed in order to say so.
func boundKinds(decls []guardrail.Declaration, invalid []guardrail.Invalid) []string {
	var bound []string
	for _, d := range decls {
		if d.IsEnabled() {
			bound = append(bound, d.BoundKinds()...)
		}
	}
	for _, iv := range invalid {
		bound = append(bound, iv.AffectedKinds()...)
	}
	return bound
}
