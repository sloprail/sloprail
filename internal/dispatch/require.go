package dispatch

import (
	"fmt"

	"github.com/sloprail/sloprail/internal/declaration"
	"github.com/sloprail/sloprail/internal/transcript"
)

// This file is the `require` half of the check-runner: the two Prerequisite
// kinds the spec defines (dot-dir-file-store/main.tsp Prerequisite), evaluated at
// the moment a rule's check would run.
//
// The two differ not in shape but in WHO establishes them and when a violation is
// discovered — which is exactly why they are evaluated differently here:
//
//   - `{skill}` is a fact about the TRAJECTORY: was a real Skill tool_use for that
//     skill made earlier in this session. Read natively from the record, the same
//     thing the deprecated require-skill.sh established through ~100 lines of jq
//     plumbing, absorbed here so a gate declaring `require: [{skill}]` gets it for
//     free.
//   - `{context}` is an ENGINE ORDERING guarantee: the named context must have run
//     its own enter this cycle before this rule's check. For THIS slice a
//     `{context}` prerequisite reads whatever context state exists in the map the
//     caller threaded in — the full context lifecycle is the next slice, which
//     populates that map. So the check here is "is the named context active in the
//     state we were handed"; ordering is the next slice's to guarantee.

// checkRequire evaluates every prerequisite in order and refuses on the first
// that does not hold.
//
// In order, and first-failure-ends-it, so the remedy a caller sees names the
// nearest unmet precondition rather than a pile of them — the same
// one-refusal-at-a-time shape the checks use. A prerequisite that sets neither
// field is impossible in a loaded rule (the validator refuses it), so it is
// treated as vacuously satisfied here rather than guarded against a second time.
func (r Runner) checkRequire(req Request) (Verdict, error) {
	for _, p := range req.Require {
		v, err := r.checkPrerequisite(req, p)
		if err != nil {
			return Verdict{}, err
		}
		if v.Refused {
			return v, nil
		}
	}
	return pass(), nil
}

// checkPrerequisite evaluates one prerequisite.
//
// Exactly one of Skill / Context is set on a loaded rule (the validator's
// exactly-one-of check), so this dispatches on which one is present. Skill is
// tried first only because it is the field listed first in the spec; the two are
// mutually exclusive so order is immaterial.
func (r Runner) checkPrerequisite(req Request, p declaration.Prerequisite) (Verdict, error) {
	if p.Skill != "" {
		return r.checkSkill(req, p.Skill)
	}
	if p.Context != "" {
		return r.checkContext(req, p.Context), nil
	}
	// Neither set: a loaded rule cannot reach here (the validator refuses an empty
	// prerequisite), so an empty one establishes nothing to fail and passes.
	return pass(), nil
}

// checkSkill refuses unless a real Skill tool_use for this skill is in the
// session's own trajectory.
//
// "In the session's own trajectory" excludes sub-agents, which is what
// skillLoaded does — a skill loaded inside a delegated sub-agent was not loaded
// on the line of work doing the writing, the same default the deprecated
// require-skill.sh took by excluding sidechains.
//
// A transcript that cannot be read, or that was never named (TranscriptPath ==
// ""), is a REFUSAL, not a permit — a precondition that could not be checked is
// not a precondition that passed. This is the fail-closed rule the deprecated
// script spelled out at length, kept here: turning a gap in the environment into
// consent is the one failure a guardrail must not have.
func (r Runner) checkSkill(req Request, skill string) (Verdict, error) {
	if req.TranscriptPath == "" {
		// No record to read, so whether the skill was loaded is unknowable. Refuse
		// with the remedy, naming why — the same reasoning require-skill.sh applied
		// to an unset SR_TRANSCRIPT.
		return refuse(fmt.Sprintf(
			"this rule requires the %q skill to have been loaded first, but this session's record could not be located, "+
				"so whether it was loaded is unknown. Refusing: a precondition that could not be checked is not a precondition that passed.",
			skill)), nil
	}

	loaded, err := r.skillLoaded(req.TranscriptPath, skill)
	if err != nil {
		// The record could not be read. Same fail-closed answer as an absent path:
		// a precondition that could not be checked has not passed.
		return refuse(fmt.Sprintf(
			"this rule requires the %q skill to have been loaded first, but this session's record could not be read (%v), "+
				"so whether it was loaded is unknown. Refusing: a precondition that could not be checked is not a precondition that passed.",
			skill, err)), nil
	}
	if loaded {
		return pass(), nil
	}
	return refuse(skillRemedy(skill)), nil
}

// skillRemedy is what a missing-skill refusal tells the agent — the one
// instruction that clears it.
//
// It names the skill and says the check reads the RECORD, not a claim, because
// the whole value of a native skill prerequisite over prose is that "I read the
// skill" is not what is checked. Carried word-for-word from require-skill.sh's own
// refusal, which had been tuned against real agents.
func skillRemedy(skill string) string {
	return fmt.Sprintf(
		"SKILL REQUIRED: this action requires the %q skill to have been loaded first, and this session's record holds no Skill tool_use naming it. "+
			"Invoke the Skill tool with skill %q, then retry. Stating that you have read it is not what is checked — the session's own record is.",
		skill, skill)
}

// checkContext refuses unless the named context is active in the state map the
// caller threaded in.
//
// For THIS slice a `{context}` prerequisite is satisfied when the named context's
// entry in the `context` map is active. The spec frames `{context}` as an ENGINE
// ORDERING guarantee — the named context must have run its enter this cycle
// before this rule — and the full lifecycle that makes that ordering happen is
// the next slice. Here the runner reads whatever context state it was given: the
// next slice populates the map, and this evaluation does not change when it does.
//
// A context named in `require` always resolves to a real declaration (the loader
// refuses an unknown context name), so an absent entry is not a typo — it is a
// context that has not entered. That refuses, with a remedy naming the context,
// because a rule that depends on a context having run must not admit the action
// when it has not.
func (r Runner) checkContext(req Request, name string) Verdict {
	st, ok := req.Context[name]
	if ok && st.Active {
		return pass()
	}
	return refuse(fmt.Sprintf(
		"this rule requires the %q context to be active first, and it is not. "+
			"That context enters on its own triggers; nothing to act on directly — if this is unexpected, the context did not recognise this cycle as one it applies to.",
		name))
}

// skillLoadedInTrajectory is the production skillLoaded: it reads the session's
// record and reports whether a Skill tool_use naming the skill was made on the
// main line of work.
//
// This is the native form of what the deprecated require-skill.sh did with
// `sr-session query` and jq. The mechanics it settled are kept:
//
//   - `type == "assistant"` entries only — a Skill call is a tool_use an assistant
//     turn makes.
//   - a tool_use block whose name is exactly "Skill".
//   - whose `input.skill` equals the required skill. The field is `skill`, read
//     outright: the spec declares SkillToolInput and the measurement behind it
//     (664 Skill calls, `skill` on every one) settles that there is no second
//     field name to hedge against.
//   - SUB-AGENT entries excluded (IsSidechain) — a skill loaded inside a delegated
//     sub-agent was not loaded on the writing line of work.
//
// The record is read whole via transcript.Read, which already skips the harness's
// bookkeeping lines. An unreadable record is an error the caller turns into a
// fail-closed refusal, not a false — "could not read" and "was not loaded" are
// different facts and only the second is an answer.
func skillLoadedInTrajectory(transcriptPath, skill string) (bool, error) {
	entries, err := transcript.Read(transcriptPath)
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if e.Type != transcript.EntryAssistant {
			continue
		}
		// The main line of work only. A skill loaded inside a sub-agent's own
		// record is not one the writing agent loaded.
		if e.IsSidechain {
			continue
		}
		for _, call := range transcript.ToolCalls(e) {
			if call.Name != "Skill" {
				continue
			}
			if skillNameOf(call) == skill {
				return true, nil
			}
		}
	}
	return false, nil
}
