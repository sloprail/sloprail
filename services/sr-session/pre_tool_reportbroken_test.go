package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// What a broken declaration is ANNOUNCED against, now that it is announced
// rather than enforced.
//
// This file exists because deleting the refusal deleted the only control on a
// property the refusal did not own. T013_05b was the positive half of the
// e2e pair — the same fault moved onto the kind the action produces, proving the
// permit in T013_05 came from kind-scoping rather than from broken declarations
// having stopped mattering. It was correctly deleted: its assertion was that a
// broken rule on the write's own kind REFUSES, which is exactly what the change
// removed.
//
// But scoping was never a fact about refusing. reportBroken still walks
// AffectedKinds and prints only for the kind in hand, so a broken rule on
// PreFileDelete stays silent for a write while a broken rule on PreFileCreate
// names itself. That is the same positive/negative pair, against the mechanism
// that replaced the refusal, and after T013_05b it had no control anywhere.
//
// # Why this is a unit test and not an e2e
//
// It was measured at the e2e layer first, not assumed. Driven through the mock
// with the fault on PreFileCreate, Result.Output carried neither the rule's name
// nor any word of reportBroken's message. The engine exits 0 when it permits and
// the mock does not forward a permitting hook's stderr — which is the measured
// channel table in session_pre_tool.go holding exactly as written: "stderr,
// exit 0 → reaches agent: no".
//
// So the e2e harness cannot observe this property at all, and a test placed
// there would assert on a channel the text never travels. Here cmd.SetErr reads
// the stream directly. The trade is stated rather than hidden: this drives the
// real runSessionPreTool over a real project and a real declaration, but it is
// not a session, and it cannot show that a session behaves this way. For a
// property that is observable ONLY on a channel no session delivers, that is the
// available choice — this test or none, and none is what the suite had.

// A declaration whose matcher misspells `path`, bound to file CREATION. The
// fault disqualifies it at load, and the kind it names is the one an ordinary
// Write produces.
const brokenOnCreateDecl = `---
hooks:
  PreFileCreate:
    - matcher: paht startsWith "guarded/"
      hooks:
        - type: command
          command: ./refuse.sh
---

# Refuses writes under guarded/, except it says paht
`

// The identical fault bound to file DELETION instead. The only difference from
// the declaration above is the kind, which is what makes the pair a measurement
// of scoping rather than of two unrelated declarations.
const brokenOnDeleteDecl = `---
hooks:
  PreFileDelete:
    - matcher: paht startsWith "guarded/"
      hooks:
        - type: command
          command: ./refuse.sh
---

# Refuses deletions under guarded/, except it says paht
`

// TestPreTool_ABrokenRuleIsReportedOnlyForItsOwnKind.
//
// Both halves in one test, deliberately. Run apart, the negative half is the
// trivially-passing assertion that sank T013_05 — "no report appeared" is
// satisfied by an engine that reports nothing ever, by a fixture that never
// loaded, and by a typo in the substring being searched for. Run together
// against the same substring and the same declaration text, the positive half is
// what rules all three out: the words are proven to appear when the kind
// matches, so their absence when it does not is scoping and not silence.
func TestPreTool_ABrokenRuleIsReportedOnlyForItsOwnKind(t *testing.T) {
	// The words reportBroken prints, and the words nothing else prints.
	// reportInvalid also names the rule on every dispatch — it is unscoped, and
	// naming the rule alone would therefore match on both halves and measure
	// nothing. This phrase belongs to reportBroken only.
	const scopedReport = "NOT guarding this action"

	// Positive: the fault is on the kind the write produces, so the report fires
	// and names both the rule and the action it has stopped guarding.
	t.Run("bound to the kind in hand", func(t *testing.T) {
		proj := initRepo(t)
		require.NoError(t, os.WriteFile(filepath.Join(proj, "seed.md"), []byte("s"), 0o644))
		guardrailDir(t, proj, "create-typo", brokenOnCreateDecl,
			map[string]string{"refuse.sh": alwaysRefuse})
		t.Chdir(proj)

		stdout, stderr := preToolIn(t, proj, "Write",
			`{"file_path":`+jsonString(filepath.Join(proj, "new.md"))+`,"content":"hi"}`)

		assert.Contains(t, stderr, scopedReport,
			"a rule bound to PreFileCreate that could not load must be named against a write")
		assert.Contains(t, stderr, "create-typo",
			"the report must name which rule stopped guarding, or nobody can find it")
		assert.Contains(t, stderr, "PreFileCreate",
			"the report must name the kind, so it says what stopped being guarded rather than that a rule is broken in the abstract")

		// The other half of "an invalid guardrail blocks nothing": it is reported
		// AND it does not refuse. Asserted here rather than left to T013_01,
		// because a reportBroken that regained a deny would satisfy every
		// assertion above.
		assert.NotContains(t, stdout, `"permissionDecision":"deny"`,
			"reporting a broken rule must not refuse the action")
	})

	// Negative: the identical fault on a kind this action does not produce. The
	// declaration is equally broken and equally loaded-as-invalid — reportInvalid
	// still names it — but reportBroken says nothing, because nothing this write
	// does was in that rule's scope.
	t.Run("bound to another kind", func(t *testing.T) {
		proj := initRepo(t)
		require.NoError(t, os.WriteFile(filepath.Join(proj, "seed.md"), []byte("s"), 0o644))
		guardrailDir(t, proj, "delete-typo", brokenOnDeleteDecl,
			map[string]string{"refuse.sh": alwaysRefuse})
		t.Chdir(proj)

		stdout, stderr := preToolIn(t, proj, "Write",
			`{"file_path":`+jsonString(filepath.Join(proj, "new.md"))+`,"content":"hi"}`)

		assert.NotContains(t, stderr, scopedReport,
			"a rule bound to PreFileDelete must not be reported against a write it was never about")
		assert.NotContains(t, stdout, `"permissionDecision":"deny"`,
			"a broken rule about deletions must not refuse a write")

		// The rule is still invalid and still said so — by reportInvalid, which is
		// unscoped by design. This is what keeps the assertion above from being
		// read as "a broken rule on another kind goes unmentioned": it is
		// mentioned, once, as a declaration that did not load. What it is not is
		// mentioned as a thing that failed to guard THIS action.
		assert.Contains(t, stderr, "not loaded",
			"the broken declaration must still be reported as unloaded, or the negative half above is just a fixture that never loaded")
	})
}
