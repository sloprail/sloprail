package e2e

import (
	"testing"
)

// This file drives the SHIPPED required-context-precondition example end to end —
// BOTH gates installed verbatim, exactly what a user lifts. The gate ENGINE
// mechanic (a pure-require skill gate that blocks a PreFileWrite until the skill
// was loaded, and admits once it was) is already pinned by
// tests/e2e/harness/gate/032_gate_dispatch (T032_03/T032_04) with an INLINE gate; what
// these tests add is that the two SHIPPED gate.yaml files, as configured, each
// guard their own prefix with their own skill, independently:
//
//   - gate/require-skill-topics:    write under memories/topics/    ⇒ requires document-topic
//   - gate/require-skill-decisions: write under memories/decisions/ ⇒ requires document-strategy
//
// Both bind PreFileWrite, so they block BEFORE the write lands (a Post event would
// be too late). The refusal is a PreToolUse deny — Result.Refused reads it — and
// it must NAME the skill the prefix demands so the agent has something to act on.

const (
	// A write under each guarded prefix, and one outside both. The exact skill
	// names are the shipped gate.yaml's own (document-topic / document-strategy) —
	// read from the files, not guessed.
	guardedDecision = "memories/decisions/pricing/DECISION.md"

	topicSkill    = "document-topic"
	decisionSkill = "document-strategy"

	// The gate names, from the folders the example ships them under.
	decisionGate = "require-skill-decisions"
)

// T051_06: the two gates are INDEPENDENT — loading the topics skill does not
// satisfy the decisions gate.
//
// A pair of gates that shared one skill-loaded flag would admit a decisions write
// once ANY skill was loaded. Here the agent loads document-topic (the topics
// gate's skill) and then writes under memories/decisions/ — the decisions gate
// still refuses, because its require names document-strategy, which was not
// loaded. This is what binds each require to its OWN gate rather than to a shared
// "some skill was loaded" fact.
func TestT051_06_GatesAreIndependent(t *testing.T) {
	e := New(t)
	proj := e.Project()
	installExampleTree(t, proj)

	res := e.Run(proj, "s-051-06", "load the topics skill then write a decision", Turns("done",
		Skill("s1", topicSkill),
		Write("w1", guardedDecision, "# pricing"),
	))

	if !res.Refused() {
		t.Fatalf("the decisions gate admitted the write after only the TOPICS skill was loaded:\n%s", res.Output)
	}
	if e.Exists(proj, guardedDecision) {
		t.Fatalf("the decisions write LANDED after only document-topic was loaded")
	}
	// Assert on the REFUSAL's own words, not the whole stream: the stream carries
	// the agent's document-topic Skill call, so a whole-stream check for
	// "document-topic" would match the agent's action rather than the refusal. The
	// deny message must name the skill the decisions gate ACTUALLY requires
	// (document-strategy) and come FROM the decisions gate — and must NOT name the
	// topics skill, which is the loaded-but-wrong one.
	reason := denyReason(res.Output)
	if reason == "" {
		t.Fatalf("no PreToolUse deny reason found in the stream:\n%s", res.Output)
	}
	if !containsStr(reason, decisionSkill) {
		t.Fatalf("the decisions refusal did not name document-strategy:\n%s", reason)
	}
	if !containsStr(reason, decisionGate) {
		t.Fatalf("the refusal did not come from the decisions gate %q — the topics skill must not have satisfied a decisions write:\n%s", decisionGate, reason)
	}
	if containsStr(reason, topicSkill) {
		t.Fatalf("the decisions refusal named the topics skill %q — it should demand its own skill, not the one that was loaded:\n%s", topicSkill, reason)
	}
}
