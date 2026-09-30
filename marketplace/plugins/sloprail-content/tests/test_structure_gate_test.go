package e2e

import "testing"

// This plugin ships its own structure-gate piece
// (.sloprail/file-guard/structure.yaml), scoped to memories/topics/ and
// .sloprail/content-rules/ — see that file's own comments for the shapes it
// allows. These tests install it as a genuine PLUGIN structure
// (installPluginStructure, which reads the real file off disk and registers
// it via the harness's EnablePluginShippingStructure — never copied flat
// into the project's own .sloprail/, which would make it a PROJECT structure
// and be refused at load for declaring `scope`), alongside this plugin's
// other guardrails (installPluginTree, which now excludes structure.yaml
// from what it copies — see that function's own comment).
//
// These prove: a unit file in the shape this plugin's own guards match
// (UNIT.md at the document-topic path) is allowed by the structure gate and
// lands; a stray file under memories/topics/ that is NOT one of this
// plugin's declared shapes (no CONSTRAINT.md, no UNIT.md, no numbered draft)
// is refused by the structure gate, naming this plugin and its scope; and a
// write OUTSIDE the plugin's scope (memories/topics/, .sloprail/content-rules/)
// is entirely unaffected by it — this plugin's structure has no say there.

// TestStructure_AllowedUnitShapePasses: a UNIT.md at the exact shape this
// plugin's structure.yaml allows (and unit-satisfies-rules/unit-publish-
// approved already match) is permitted and lands. No applicable writing
// rule is configured, so nothing else blocks it either — isolating the
// structure gate's own permit.
func TestStructure_AllowedUnitShapePasses(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, proj)
	installPluginStructure(t, e, proj)
	// unit-satisfies-rules's judge always runs (even on the "NONE" sentinel
	// with no applicable rule) — stub it PASS so only the structure gate's
	// own decision is under test.
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	sess := "s-structure-allowed"
	body := unitFrontmatter(
		"transcript_path: /abs/s.jsonl\ncreated: 2026-09-25\ntype: post\nstatus: raw\n",
		"Nothing to check here.")

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", unitPath, body),
	).ThenCommit("Add the unit"))
	if res.Refused() {
		t.Fatalf("a unit at the plugin's own allowed shape was refused:\n%s", res.Output)
	}
	if !e.Exists(proj, unitPath) {
		t.Errorf("an admitted unit write did not land on disk")
	}
}

// TestStructure_StrayFileUnderTopicsRefused: a file under memories/topics/
// that is none of the shapes this plugin declares (not TOPIC.md, UNIT.md,
// a numbered draft, CONSTRAINT.md, or a distribution file) is refused by
// THIS PLUGIN's own structure gate, before it lands — the structure gate is
// a gate by nature (it gates the write itself, at Pre).
func TestStructure_StrayFileUnderTopicsRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, proj)
	installPluginStructure(t, e, proj)

	stray := "memories/topics/20260925_x/STYLE.md"
	sess := "s-structure-stray"

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", stray, "# style notes, not a unit\n"),
	))
	if !res.Refused() {
		t.Fatalf("a stray file under memories/topics/ (not one of this plugin's declared shapes) was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, stray) {
		t.Errorf("the structure gate let a stray topics/ file land on disk")
	}
	// The refusal's quotes arrive backslash-escaped inside the mock's JSON
	// stream (nested JSON-in-JSON), so match on the plugin name and the
	// surrounding words rather than the exact quoting.
	if !containsStr(res.Output, "sloprail-content") || !containsStr(res.Output, "structure gate") {
		t.Errorf("the refusal did not name this plugin's structure gate as the decider:\n%s", res.Output)
	}
	if !res.Saw("memories/topics/") {
		t.Errorf("the refusal did not name the owned scope:\n%s", res.Output)
	}
}

// TestStructure_WriteOutsideScopeUnaffected: a write entirely outside this
// plugin's scope (neither memories/topics/ nor .sloprail/content-rules/) is
// not decided by this plugin's structure gate at all — with no project
// structure installed in this test, and this plugin owning only its two
// declared folders, the write is permitted (no structure at all governs it).
func TestStructure_WriteOutsideScopeUnaffected(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, proj)
	installPluginStructure(t, e, proj)

	outside := "docs/notes.md"
	sess := "s-structure-outside"

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", outside, "# unrelated project docs\n"),
	))
	if res.Refused() {
		t.Fatalf("a write outside this plugin's structure scope was refused:\n%s", res.Output)
	}
	if !e.Exists(proj, outside) {
		t.Errorf("a write outside the plugin's scope, with no structure to govern it, did not land")
	}
}
