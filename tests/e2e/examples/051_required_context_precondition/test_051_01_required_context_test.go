package e2e

import (
	"testing"
)

// This file drives the SHIPPED required-context-precondition example end to end —
// BOTH gates installed verbatim, exactly what a user lifts. The gate ENGINE
// mechanic (a pure-require skill gate that blocks a PreFileWrite until the skill
// was loaded, and admits once it was) is already pinned by
// tests/e2e/gate/032_gate_dispatch (T032_03/T032_04) with an INLINE gate; what
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
	guardedTopic    = "memories/topics/no-slop/TOPIC.md"
	guardedDecision = "memories/decisions/pricing/DECISION.md"
	unguarded       = "memories/updates/2026-08-13_daily-checkin.md"

	topicSkill    = "document-topic"
	decisionSkill = "document-strategy"

	// The gate names, from the folders the example ships them under.
	topicGate    = "require-skill-topics"
	decisionGate = "require-skill-decisions"
)

// T051_01: writing under memories/topics/ WITHOUT document-topic loaded is refused,
// and the refusal names document-topic.
//
// The block-without-skill half for the topics gate. No Skill turn precedes the
// write, so the session's record holds no Skill tool_use for document-topic, the
// require fails, and the write is denied before it lands.
func TestT051_01_TopicsWriteWithoutSkillIsRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	installExampleTree(t, proj)

	res := e.Run(proj, "s-051-01", "start a topic without the skill", Turns("done",
		Write("w1", guardedTopic, "# no-slop"),
	))

	if !res.Refused() {
		t.Fatalf("a write under memories/topics/ without document-topic loaded was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, guardedTopic) {
		t.Fatalf("the guarded topics write LANDED despite the unmet skill require")
	}
	// The refusal names the skill the prefix demands — a refusal that did not say
	// which skill leaves the agent with nothing to act on. Asserted on the deny
	// message itself, and that it came from the topics gate.
	reason := denyReason(res.Output)
	if !containsStr(reason, topicSkill) {
		t.Fatalf("the topics refusal did not name the required skill %q:\n%s", topicSkill, res.Output)
	}
	if !containsStr(reason, topicGate) {
		t.Fatalf("the refusal did not come from the topics gate %q:\n%s", topicGate, reason)
	}
}

// T051_02: loading document-topic THEN writing under memories/topics/ is admitted.
//
// The admit-with-skill half for the topics gate, and the control proving T051_01
// blocks for the missing skill rather than for some unrelated reason: same path,
// same gate, opposite verdict, the only difference being the Skill turn first.
func TestT051_02_TopicsWriteWithSkillIsAdmitted(t *testing.T) {
	e := New(t)
	proj := e.Project()
	installExampleTree(t, proj)

	res := e.Run(proj, "s-051-02", "start a topic properly", Turns("done",
		Skill("s1", topicSkill),
		Write("w1", guardedTopic, "# no-slop"),
	))

	if res.Refused() {
		t.Fatalf("a topics write was refused though document-topic was loaded first:\n%s", res.Output)
	}
	if !e.Exists(proj, guardedTopic) {
		t.Fatalf("the topics write did not land though the required skill was loaded")
	}
}

// T051_03: writing under memories/decisions/ WITHOUT document-strategy loaded is
// refused, and the refusal names document-strategy.
//
// The block-without-skill half for the SECOND gate — its own prefix, its own
// skill. Without this the topics gate alone could pass every test while the
// decisions gate did nothing.
func TestT051_03_DecisionsWriteWithoutSkillIsRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	installExampleTree(t, proj)

	res := e.Run(proj, "s-051-03", "record a decision without the skill", Turns("done",
		Write("w1", guardedDecision, "# pricing"),
	))

	if !res.Refused() {
		t.Fatalf("a write under memories/decisions/ without document-strategy loaded was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, guardedDecision) {
		t.Fatalf("the guarded decisions write LANDED despite the unmet skill require")
	}
	reason := denyReason(res.Output)
	if !containsStr(reason, decisionSkill) {
		t.Fatalf("the decisions refusal did not name the required skill %q:\n%s", decisionSkill, res.Output)
	}
	if !containsStr(reason, decisionGate) {
		t.Fatalf("the refusal did not come from the decisions gate %q:\n%s", decisionGate, reason)
	}
}

// T051_04: loading document-strategy THEN writing under memories/decisions/ is
// admitted.
//
// The admit-with-skill half for the decisions gate.
func TestT051_04_DecisionsWriteWithSkillIsAdmitted(t *testing.T) {
	e := New(t)
	proj := e.Project()
	installExampleTree(t, proj)

	res := e.Run(proj, "s-051-04", "record a decision properly", Turns("done",
		Skill("s1", decisionSkill),
		Write("w1", guardedDecision, "# pricing"),
	))

	if res.Refused() {
		t.Fatalf("a decisions write was refused though document-strategy was loaded first:\n%s", res.Output)
	}
	if !e.Exists(proj, guardedDecision) {
		t.Fatalf("the decisions write did not land though the required skill was loaded")
	}
}

// T051_05: a write OUTSIDE both guarded prefixes is left alone — no skill needed.
//
// The control that separates a rule with a scope from a rule that blocks all work.
// The example refuses every guarded write with no skill loaded, so without this a
// pair of gates that refused EVERYTHING would pass T051_01/03 and look correct.
// memories/updates/ is under neither gate's prefix, so neither fires.
func TestT051_05_WriteOutsideBothPrefixesIsPermitted(t *testing.T) {
	e := New(t)
	proj := e.Project()
	installExampleTree(t, proj)

	res := e.Run(proj, "s-051-05", "write a check-in", Turns("done",
		Write("w1", unguarded, "# 2026-08-13"),
	))

	if res.Refused() {
		t.Fatalf("a path outside both guarded prefixes was refused:\n%s", res.Output)
	}
	if !e.Exists(proj, unguarded) {
		t.Fatalf("the unguarded write did not land though nothing should have blocked it")
	}
	// Neither gate's skill is named — the gates did not judge this write at all.
	if res.Saw(topicSkill) || res.Saw(decisionSkill) {
		t.Fatalf("a write outside both prefixes was judged by a gate (a skill was named):\n%s", res.Output)
	}
}

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
