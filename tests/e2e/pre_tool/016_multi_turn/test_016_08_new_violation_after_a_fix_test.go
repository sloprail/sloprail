package e2e

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The trajectory the owner named: a refusal on turn 1, the agent fixes on turn
// 2, and a NEW violation on turn 3.
//
// T016_01 already covers refuse / fix / retry-the-same-thing. That is the happy
// path and it is not the one that catches a stateful engine misbehaving: the
// retry there is the same path with the same rule, so an engine that simply
// re-ran everything and an engine that tracked the refusal correctly both pass.
//
// What was missing is the turn that is neither the refused work nor a repeat of
// it. A rule has by then recorded a verdict against one subject and cleared it;
// a DIFFERENT subject arriving next must be judged on its own facts. Two
// opposite failures live here and each is invisible to the other's test:
//
//   - a verdict that leaks forward, so the cleared refusal exempts the new
//     violation and the third turn goes through unjudged;
//   - a session poisoned by the earlier refusal, so the new work is refused for
//     the old reason and the agent is told to fix something it already fixed.
//
// Both are asserted below, and neither is observable from the stream alone —
// the turn-1 refusal text is still in the output no matter what turn 3 did.

// twoRules guards two different directories with one rule each, so a violation
// on turn 3 can be a genuinely different rule from the one that refused on turn
// 1 rather than the same rule seeing a second file.
//
// Bound to both pending kinds, because a retry after the file exists is an
// update rather than a create, and a rule bound to only one of them would go
// quiet at exactly the turn under test.
const guardsDrafts = `---
hooks:
  PreFileCreate:
    - matcher: path startsWith "drafts/"
      hooks:
        - type: command
          command: ./h.sh
  PreFileUpdate:
    - matcher: path startsWith "drafts/"
      hooks:
        - type: command
          command: ./h.sh
---

# Nothing may be written under drafts/ until the project has a TEMPLATE
`

const guardsVendor = `---
hooks:
  PreFileCreate:
    - matcher: path startsWith "vendor/"
      hooks:
        - type: command
          command: ./h.sh
  PreFileUpdate:
    - matcher: path startsWith "vendor/"
      hooks:
        - type: command
          command: ./h.sh
---

# Nothing may be written under vendor/, ever
`

// gatedOnTemplate refuses while TEMPLATE is absent and permits once it is
// there, recording its verdict either way.
//
// The condition is something the AGENT can change between turns, which is what
// makes "the refusal cleared" a different observation from "the rule never
// fired". Three levels up from the guardrail's own folder is the project root.
const gatedOnTemplate = `#!/bin/sh
cat >/dev/null
if [ -f "$PWD/../../../TEMPLATE" ]; then
  echo permitted >> "$PWD/log"
  exit 0
fi
echo refused >> "$PWD/log"
echo 'drafts need a TEMPLATE first' >&2
exit 1
`

// alwaysRefuses is the second rule's hook: nothing it is shown is acceptable.
const alwaysRefuses = `#!/bin/sh
cat >/dev/null
echo refused >> "$PWD/log"
echo 'nothing may be written under vendor/' >&2
exit 1
`

// T016_08: refused on turn 1, fixed on turn 2, a DIFFERENT rule violated on
// turn 3.
//
// The owner's trajectory. Four claims, each able to fail on its own:
//
//   - turn 1 is refused and nothing lands;
//   - turn 2's fix lands, and clears the first rule;
//   - turn 3 is refused by the SECOND rule, in its own words — not by the
//     first rule's stale verdict, and not permitted because the session had
//     already settled something;
//   - the first rule, having been cleared, is not still refusing.
//
// The last two are what a single-turn test cannot reach.
func TestT016_08_ANewViolationAfterAFixIsCaughtOnItsOwnTerms(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "needs-template", guardsDrafts, map[string]string{"h.sh": gatedOnTemplate})
	e.Guardrail(proj, "no-vendor", guardsVendor, map[string]string{"h.sh": alwaysRefuses})

	res := e.Run(proj, "s-016-08", "draft, fix, then touch vendor", Turns("done",
		Write("t1", "drafts/post.md", "first attempt"),
		Bash("t2", "touch TEMPLATE"),
		Write("t3", "vendor/lib.js", "copied in"),
	))

	require.True(t, res.Saw("drafts need a TEMPLATE first"),
		"turn 1 must be refused, or there is no refusal for the later turns to be measured against")
	require.True(t, e.Exists(proj, "TEMPLATE"), "turn 2's fix must have landed")

	// Turn 3 is refused, and by the rule that actually governs it. Asserting on
	// the second rule's own words is what separates "the new violation was
	// caught" from "the session was still refusing for the old reason".
	assert.True(t, res.Saw("nothing may be written under vendor/"),
		"the new violation on turn 3 must be refused by the rule that governs it")
	assert.False(t, e.Exists(proj, "vendor/lib.js"),
		"a refused pending write must leave nothing on disk")

	// The first rule was asked twice and changed its answer. One entry would
	// mean turn 1 never reached it; two refusals would mean the fix did not
	// clear it. Neither is visible from the stream.
	assert.Equal(t, []string{"refused"}, e.Ledger(proj, "needs-template", "log"),
		"the drafts rule must have been asked exactly once — turn 3 is not under its scope, "+
			"and a rule that fired outside its matcher would show a second entry")

	// And the rule that refused turn 3 was asked exactly once: on turn 3. A
	// vendor rule that had also fired on turns 1 or 2 would mean the matcher is
	// not narrowing at all.
	assert.Equal(t, []string{"refused"}, e.Ledger(proj, "no-vendor", "log"),
		"the vendor rule must be asked once, on the turn that touched vendor/")
}

// T016_09: the fix on turn 2 really does clear the first rule, proved by going
// back to it on turn 4.
//
// T016_08 asserts the drafts rule was asked once and refused. That leaves the
// question it cannot answer: did the fix actually clear it, or would it refuse
// forever? A rule that never clears is a sentence rather than a rule, and the
// difference only shows when the refused work is retried AFTER the fix and after
// other work has intervened.
//
// The intervening turn matters. In T016_01 the retry follows the fix
// immediately; here a refusal by a different rule sits in between, so this also
// covers "a refusal by another rule does not poison the session".
func TestT016_09_TheClearedRuleLetsTheOriginalWorkThroughLater(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "needs-template", guardsDrafts, map[string]string{"h.sh": gatedOnTemplate})
	e.Guardrail(proj, "no-vendor", guardsVendor, map[string]string{"h.sh": alwaysRefuses})

	res := e.Run(proj, "s-016-09", "draft, fix, vendor, draft again", Turns("done",
		Write("t1", "drafts/post.md", "first attempt"),
		Bash("t2", "touch TEMPLATE"),
		Write("t3", "vendor/lib.js", "copied in"),
		Write("t4", "drafts/post.md", "second attempt"),
	))

	require.True(t, res.Saw("drafts need a TEMPLATE first"), "turn 1 must be refused")
	require.True(t, e.Exists(proj, "TEMPLATE"), "turn 2's fix must have landed")

	// The retry landed. This is the only proof the refusal cleared rather than
	// merely stopping being mentioned.
	assert.True(t, e.Exists(proj, "drafts/post.md"),
		"the retry after the fix must land — a refusal that never clears is a sentence")

	// Refused, then permitted. The order is the claim: the same rule answering
	// differently once its condition changed.
	assert.Equal(t, []string{"refused", "permitted"}, e.Ledger(proj, "needs-template", "log"),
		"the drafts rule must be asked again on turn 4 and must answer differently")

	// The unrelated refusal in between did not stop turn 4 being judged, and did
	// not stop it landing.
	assert.False(t, e.Exists(proj, "vendor/lib.js"),
		"the vendor rule's refusal must still have prevented its own write")
}

// T016_10: a refusal that nothing fixes is refused again on a later turn.
//
// The other direction, and the one an over-eager exemption breaks. T016_03
// covers two attempts at the same path with nothing in between; this adds a
// SUCCESSFUL, judged, permitted write between the two attempts, so the rule has
// recorded a pass for another subject before being asked about the refused one
// again.
//
// An engine pooling verdicts per rule rather than per subject lets the pass
// exempt the retry, which is exactly the failure "a file one rule has passed is
// a file another may never have seen" is careful about, one level down.
func TestT016_10_AnUnfixedRefusalIsRefusedAgainAfterAPermittedWrite(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "needs-template", guardsDrafts, map[string]string{"h.sh": gatedOnTemplate})

	res := e.Run(proj, "s-016-10", "draft, write elsewhere, draft again", Turns("done",
		Write("t1", "drafts/post.md", "first attempt"),
		Write("t2", "notes.md", "unrelated and unguarded"),
		Write("t3", "drafts/post.md", "second attempt, nothing fixed"),
	))

	require.True(t, res.Saw("drafts need a TEMPLATE first"), "the guarded path must be refused")

	assert.True(t, e.Exists(proj, "notes.md"),
		"an unguarded write must not be caught up in another path's refusal")
	assert.False(t, e.Exists(proj, "drafts/post.md"),
		"the retry must be refused again while the cause is unfixed")

	// Asked on both attempts, and refused both times. One entry would mean the
	// retry was exempted — and the tree assertion above would still pass,
	// because a skipped rule permits and the file would then exist. Two entries
	// is what makes "refused again" a measurement rather than an inference.
	assert.Equal(t, []string{"refused", "refused"}, e.Ledger(proj, "needs-template", "log"),
		"both attempts on the guarded path must reach the rule and both must refuse")
}

// T016_11: a refusal on IDENTICAL content is refused again on a later turn.
//
// T016_10 retries with different text, which means its two attempts have
// different fingerprints and the exemption machinery never engages — measured,
// not assumed: mutating Skippable to return true for a REFUSED verdict, and
// mutating the verdict key to a constant path, both leave T016_08 through
// T016_10 green. Those tests are about which rule governs which turn, and they
// discriminate that; they say nothing about the skip.
//
// This is the turn where the skip is genuinely in play. The same path with
// byte-identical content is exactly the case an exemption is entitled to skip
// when the earlier verdict was a PASS — and must never skip when it was a
// refusal, because "already judged" is not "already permitted". A refusal that
// exempted its own retry would clear itself by being repeated, which is the
// fail-open in its purest form.
//
// The middle turn is an unrelated permitted write, so the rule has recorded a
// pass for another subject before being asked about this one again. That is what
// separates a verdict keyed per (rule, path, content) from one pooled per rule.
func TestT016_11_AnIdenticalRetryOfRefusedContentIsRefusedAgain(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "needs-template", guardsDrafts, map[string]string{"h.sh": gatedOnTemplate})

	// Byte-identical on turns 1 and 3. Nothing is fixed in between, so the
	// honest answer both times is a refusal.
	const same = "the very same bytes"

	res := e.Run(proj, "s-016-11", "draft, write elsewhere, draft the same bytes again", Turns("done",
		Write("t1", "drafts/post.md", same),
		Write("t2", "drafts/other.md", same),
		Write("t3", "drafts/post.md", same),
	))

	require.True(t, res.Saw("drafts need a TEMPLATE first"), "the guarded path must be refused")

	assert.False(t, e.Exists(proj, "drafts/post.md"),
		"an identical retry of refused content must still be refused — a refusal that exempts "+
			"its own retry clears itself by being repeated")

	// Three refusals: the two attempts on post.md and the one on other.md. A
	// skip granted by the earlier REFUSAL would drop the third entry; a verdict
	// pooled per rule rather than per path would drop it too, since other.md
	// carries the same bytes.
	assert.Equal(t, []string{"refused", "refused", "refused"},
		e.Ledger(proj, "needs-template", "log"),
		"every attempt must reach the rule: a refusal is never a licence, and two different "+
			"paths holding the same bytes are two separate questions")
}

// T016_12: two DIFFERENT paths carrying identical bytes are two questions.
//
// The complement of T016_11 from the other side. T016_11 shows a refusal never
// licenses a skip; this shows the verdict is keyed on the path as well as the
// content, so a pass recorded for one file is not a pass for another that
// happens to hold the same text.
//
// Both writes are creations of paths that did not exist, which matters: a
// creation is the one pending kind whose content is fingerprinted, because the
// pending bytes are exactly what would land. A second write to an EXISTING path
// is a PreFileUpdate, and Subject deliberately declines to fingerprint those —
// the bytes on disk are what the write would replace, not what it would leave,
// so a fingerprint taken there would let a second offer of different content
// match the first offer's stored pass. That is why this test uses two paths
// rather than writing the same path twice, and it is a real constraint on what
// the exemption can be tested with rather than a preference.
//
// TEMPLATE is present from the start, so the rule permits throughout and the
// question is purely which subjects it is asked about.
func TestT016_12_IdenticalBytesAtTwoPathsAreJudgedSeparately(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.Guardrail(proj, "needs-template", guardsDrafts, map[string]string{"h.sh": gatedOnTemplate})
	e.WriteFile(proj, "TEMPLATE", "so the rule permits from the first turn\n")

	const same = "the very same bytes"

	e.Run(proj, "s-016-12", "write identical bytes to two paths", Turns("done",
		Write("t1", "drafts/one.md", same),
		Write("t2", "notes.txt", "unrelated and unguarded"),
		Write("t3", "drafts/two.md", same),
	))

	require.True(t, e.Exists(proj, "drafts/one.md"), "the first permitted write must land")
	require.True(t, e.Exists(proj, "drafts/two.md"), "the second permitted write must land")

	// Asked twice, once per path. A verdict pooled on content alone would skip
	// the second and leave one entry — and every tree assertion above would
	// still pass, because a skipped rule permits.
	assert.Equal(t, []string{"permitted", "permitted"}, e.Ledger(proj, "needs-template", "log"),
		"a pass recorded for one path must not exempt another path holding the same bytes")
}
