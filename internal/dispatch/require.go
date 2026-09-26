package dispatch

import (
	"encoding/json"
	"fmt"

	"github.com/sloprail/sloprail/internal/commandmod"
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

// checkSkill refuses unless the session's own trajectory holds either a real
// Skill tool_use for this skill, or evidence that its own SKILL.md was read
// directly (a Read tool_use on the file, or a file-reading Bash command) — see
// skillLoadedInTrajectory for both halves.
//
// "In the session's own trajectory" excludes sub-agents, which is what
// skillLoaded does — a skill loaded (or its file read) inside a delegated
// sub-agent was not loaded on the line of work doing the writing, the same
// default the deprecated require-skill.sh took by excluding sidechains.
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

	loaded, err := r.skillLoaded(req.TranscriptPath, req.Workspace, skill)
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
// skill" is not what is checked. The Skill-tool-use wording is carried
// word-for-word from require-skill.sh's own refusal, which had been tuned
// against real agents; the second sentence names the read-detection half
// (skillLoadedInTrajectory) so a refusal does not describe only one of the two
// ways to clear it.
func skillRemedy(skill string) string {
	return fmt.Sprintf(
		"SKILL REQUIRED: this action requires the %q skill to have been loaded first, and this session's record holds no Skill tool_use naming it, "+
			"and no Read or file-reading Bash command (cat, head, …) on its SKILL.md either. "+
			"Invoke the Skill tool with skill %q, then retry — or read the skill's own SKILL.md file directly. "+
			"Stating that you have read it is not what is checked — the session's own record is.",
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
// own record — the record for the SESSION now doing the writing, be that the
// root or a sub-agent — and reports whether a Skill tool_use naming the skill was
// made on that record's own line of work.
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
//
// # Whose entries count as "the writing line of work"
//
// A root session's Stop reads its OWN transcript, and IsSidechain there marks
// entries belonging to a DELEGATED sub-agent — a different line of work than the
// one now writing, so those are excluded. A sub-agent's SubagentStop reads a
// DIFFERENT file: its OWN transcript (HookPayload.record() resolves to
// AgentTranscriptPath for that call), and every entry a harness writes there
// carries IsSidechain true — that is simply how a harness marks "this file is a
// sub-agent's", not a marker that some entries in it belong to a further-delegated
// child. Measured in transcript.SubagentTranscriptPath's own survey: "every
// record carried isSidechain true" for the 331 sub-agent files sampled, with none
// carrying a LogicalParentUUID that would identify a nested grandchild.
//
// So IsSidechain cannot be read the same way in both files. Excluding it
// unconditionally emptied a sub-agent's OWN Skill tool_use from its OWN
// trajectory — the record a SubagentStop check is actually asked about — which
// made `require: [{skill: ...}]` unsatisfiable there no matter how many times or
// how recently the sub-agent invoked the Skill tool. The fix is to key the
// exclusion on the FILE, not the flag: skip sidechain entries only when reading a
// trajectory that is not itself a sub-agent's own record (transcript.
// IsSubagentTranscript decides which file this is, once, before the walk). Inside
// a sub-agent's own file every entry already belongs to the line of work now
// writing, so nothing is excluded there.
//
// The record is read whole via transcript.Read, which already skips the harness's
// bookkeeping lines. An unreadable record is an error the caller turns into a
// fail-closed refusal, not a false — "could not read" and "was not loaded" are
// different facts and only the second is an answer.
//
// # The second way to satisfy this: reading the skill's own file directly
//
// A Skill tool_use is not the only trajectory evidence that a skill was
// genuinely consulted. An agent that opened the skill's own SKILL.md — with the
// Read tool, or with a Bash command that reads it whole (`cat`, `head`, `less`,
// …, see commandmod.ReadsFile) — has looked at exactly the same content loading
// the skill would have shown it, through a channel the record can verify just as
// concretely as a Skill tool_use: the file path is either named on a Read
// tool_use's `file_path`, or found by commandmod's own command-line parser
// inside a Bash tool_use's `command`. Neither is a claim — both are the kind of
// native, record-grounded fact `{skill}` exists to check instead of prose.
//
// This is why the walk below checks BOTH per entry in one pass, rather than two
// separate trajectory reads: same entries, same sidechain-exclusion rule, same
// tool-call extraction, so the two ways to satisfy the prerequisite cannot
// disagree about which entries are "this line of work".
//
// workspace resolves the skill NAME to the file path(s) it could be loaded
// from (SkillFilePaths) — a Read or Bash tool_use names a PATH, not a skill
// name, so the connection has to be made before any entry is read. An empty
// workspace (no project root resolvable) yields no candidate paths, and the
// read-file half of this check then finds nothing to match — degrading to the
// Skill-tool-use check alone, never a refusal of its own: a workspace that
// could not be resolved is a fact about the session, already surfaced
// elsewhere, and is not this function's fact to refuse a second time.
func skillLoadedInTrajectory(transcriptPath, workspace, skill string) (bool, error) {
	entries, err := transcript.Read(transcriptPath)
	if err != nil {
		return false, err
	}
	skillPaths := SkillFilePaths(workspace, skill)

	// Decided once, from the FILE, not per entry: a sub-agent's own transcript
	// marks every entry IsSidechain, so treating that flag as "belongs to a
	// delegated child" inside that very file would exclude everything in it. See
	// the note above.
	excludeSidechain := !transcript.IsSubagentTranscript(transcriptPath)
	for _, e := range entries {
		if e.Type != transcript.EntryAssistant {
			continue
		}
		// The main line of work only, WITHIN this trajectory. A skill loaded
		// inside a sub-agent's own record, when THIS trajectory is a root's, was
		// not loaded on the writing line of work; when THIS trajectory IS that
		// sub-agent's own record, its entries are exactly that line of work.
		if excludeSidechain && e.IsSidechain {
			continue
		}
		for _, call := range transcript.ToolCalls(e) {
			switch call.Name {
			case "Skill":
				if skillNameOf(call) == skill {
					return true, nil
				}
			case "Read":
				if len(skillPaths) > 0 && pathReadByReadTool(call, skillPaths) {
					return true, nil
				}
			default:
				if len(skillPaths) > 0 && commandReadsAnyOf(call, skillPaths) {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

// pathReadByReadTool reports whether a Read tool_use's `file_path` is exactly
// one of the candidate skill paths.
//
// `file_path` is the same key filemod's own write-tool reading uses
// (pendingshape.go's FilePath) — one field name, one convention, across every
// tool that names a file directly rather than through a command line.
func pathReadByReadTool(call transcript.ToolCall, skillPaths []string) bool {
	var in struct {
		FilePath string `json:"file_path"`
	}
	if json.Unmarshal(call.Input, &in) != nil || in.FilePath == "" {
		return false
	}
	for _, p := range skillPaths {
		if in.FilePath == p {
			return true
		}
	}
	return false
}

// commandReadsAnyOf reports whether a tool_use carrying a shell command
// (`command`, the shape any Bash-like tool uses — see commandmod's own Pending)
// reads any of the candidate skill paths whole.
//
// Read the same way commandmod's own module reads a pending command's
// arguments: decode `command` and hand it to the parser. Not gated on the tool
// NAME the way commandmod's Extract is (HarnessCommandTools) — a name allowlist
// exists there to bound which tools produce a PreCommandInvoke event system-wide,
// a much bigger surface than this one prerequisite check. Here a tool_use naming
// no `command` field simply contributes nothing, which is the same "not a
// shape this reads" answer arrived at without a name gate at all — no drift risk
// results, because the failure mode of skipping the gate is a wasted parse on an
// unrelated tool's input, not a wrongly-satisfied prerequisite.
func commandReadsAnyOf(call transcript.ToolCall, skillPaths []string) bool {
	var in struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(call.Input, &in) != nil || in.Command == "" {
		return false
	}
	for _, p := range skillPaths {
		if commandmod.ReadsFile(in.Command, p) {
			return true
		}
	}
	return false
}

// skillLoaded is the production Runner.skillLoaded: skillLoadedInTrajectory
// under the name Runner.withDefaults assigns to the zero-value Runner.
//
// A thin wrapper rather than assigning skillLoadedInTrajectory directly so the
// two names read distinctly at each call site — skillLoaded is what a Runner
// calls, skillLoadedInTrajectory is what does the work — the same split
// dispatch.go's own doc comment on the Runner field describes.
func skillLoaded(transcriptPath, workspace, skill string) (bool, error) {
	return skillLoadedInTrajectory(transcriptPath, workspace, skill)
}
