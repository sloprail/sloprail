package e2e

import (
	"strings"
	"testing"
)

// This file proves the WORKING pieces of completeness-artifact-on-trigger: the
// no-tag refusal (the tag-required gate, which needs no registry read), the
// context's accumulation of tag/artifact entries, and the structure.yaml path
// allowlist. The verify-artifact-produced gate is broken — pinned in
// test_040_02.

// noTagReason is tag-required's own wording.
const noTagReason = "This turn declared no tag (#update, #decision, or #skip)"

// T040_01: a turn that declares NO tag is REFUSED at Stop, and the gate's own
// words reach the agent.
//
// The core of the "completeness" unit's live example: a turn must declare an
// intent (#update/#decision/#skip) before it can end. The tag-required gate reads
// the tag-declared context's ABSENCE directly (`match: not
// context["tag-declared"].active`) — no registry read, so this half works. With no
// tag, the context never activates, the match holds, and refuse.sh blocks.
func TestT040_01_NoTagRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	e.CommitAll(proj, "install")

	sess := "s-040-01"
	res := e.Run(proj, sess, "do work without declaring a tag", Turns("done",
		Say("m1", "I did the work but never declared a tag."),
	))

	// The context did NOT activate (no tag), which is what makes the gate's
	// `not active` match hold.
	if active, _ := e.ContextState(proj, sess, "tag-declared"); active {
		t.Errorf("the context activated with no tag declared")
	}
	blocks := e.BlockingErrorsFrom(proj, sess, "Stop")
	if len(blocks) == 0 {
		t.Fatalf("the tag-required gate did not refuse a turn with no tag:\n%s", res.Output)
	}
	joined := strings.Join(blocks, "\n")
	if !strings.Contains(joined, noTagReason) {
		t.Errorf("the no-tag refusal reason did not reach the agent:\n%s", joined)
	}
	if !strings.Contains(joined, "tag-required") {
		t.Errorf("the refusal did not name the gate:\n%s", joined)
	}
}

// T040_02: the context accumulates BOTH a tag entry and an artifact entry in one
// turn — the tag and the artifact can arrive in different tool calls, and the
// registry keeps both.
//
// The accumulation property the unit stresses (tag and artifact may land in
// separate tool calls; sr-session state accumulates across occurrences). Here a
// single turn declares #update AND writes an update artifact; the context logs
// tag:update and artifact:memories/updates/note.md. (That the gate meant to READ
// this cannot is T040_02's concern; the logging itself is real and load-bearing.)
func TestT040_02_ContextAccumulatesTagAndArtifact(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	e.CommitAll(proj, "install")

	sess := "s-040-02"
	// #update declared atomically with the artifact write (a combined turn — a
	// pure-text tag turn would be terminal and the write would never run).
	e.Run(proj, sess, "record an update", Turns("done",
		SayWrite("w1", "Recording this. #update", "memories/updates/note.md", "# An update\n"),
	))

	reg := e.GuardrailState(proj, sess, "tag-declared", "")
	if _, ok := reg["tag:update"]; !ok {
		t.Errorf("the context did not log the #update tag; registry=%v", reg)
	}
	if _, ok := reg["artifact:memories/updates/note.md"]; !ok {
		t.Errorf("the context did not log the artifact; registry=%v", reg)
	}
}

// T040_03: the structure.yaml path allowlist ADMITS an in-allowlist write and
// REFUSES an out-of-allowlist write, before it lands.
//
// structure.yaml is a real, working file-guard: an update artifact must be
// memories/updates/*.md and a decision a memories/decisions/<YYYYMMDD_slug>/*.md.
// A write inside the allowlist lands; a write outside it is refused before it lands
// (the cheapest "may you write here at all" question). Two sessions so the refusal
// of one does not mask the admit of the other.
func TestT040_03_StructureAllowlist(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installExampleTree(t, proj, exampleName)
	e.CommitAll(proj, "install")

	// In-allowlist: an update artifact. The WRITE is admitted (it lands), even
	// though the Stop gate will separately object — structure runs at PreToolUse.
	e.Run(proj, "s-040-03a", "write an allowed artifact", Turns("done",
		SayWrite("w1", "note #skip", "memories/updates/ok.md", "# ok\n"),
	))
	if !e.Exists(proj, "memories/updates/ok.md") {
		t.Errorf("the structure allowlist refused an in-allowlist write (memories/updates/ok.md)")
	}

	// Out-of-allowlist: a path the allowlist does not permit. Refused before it
	// lands, the file never lands.
	res := e.Run(proj, "s-040-03b", "write a disallowed path", Turns("done",
		SayWrite("w1", "note #skip", "src/random.md", "x"),
	))
	if !res.Refused() {
		t.Errorf("the structure allowlist did not refuse an out-of-allowlist write:\n%s", res.Output)
	}
	if e.Exists(proj, "src/random.md") {
		t.Errorf("an out-of-allowlist write LANDED despite the structure guard")
	}
}
