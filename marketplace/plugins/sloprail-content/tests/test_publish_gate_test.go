package e2e

import (
	"strings"
	"testing"
)

// unit-publish-approved is a PREVENTIVE file-guard over UNIT.md only.
//
//   - A write that moves a unit INTO status: published (from another status,
//     or by creating it published) must carry a citation of the user's own
//     words approving it, ON THE ACTION: `sr-file edit|write ... --cite:user
//     '<quote>'`. The session resolves the quote against its own record, so
//     a quote the user never said — or the agent's own output — is no
//     citation. The unit stores no approval text and no transcript link (an
//     earlier draft's `approved:` field, then a body link, are both gone).
//   - A unit at status: published must carry published_urls: (frontmatter, a
//     LIST — a unit may ship on more than one channel).
//   - A write that is not a transition into published needs no citation.

// publishPrompt is the user's own message in every publish scenario;
// approvalQuote is the words of it a grounded publish cites.
const (
	publishPrompt = "The launch announcement looks good, ship it."
	approvalQuote = "ship it"
)

const (
	draftingUnit  = "---\ncreated: 2026-09-25\ntype: post\nstatus: drafting\n---\n\nAnnouncing the launch.\n"
	publishedUnit = "---\ncreated: 2026-09-25\ntype: post\nstatus: published\npublished_urls: [\"https://x.com/nikita/status/1\"]\n---\n\nAnnouncing the launch.\n"
)

// installPublishProject stands up a project with the plugin installed and a
// PASS judge stub (unit-satisfies-rules judges every settled unit at Stop,
// even with no rule configured). seed, when non-empty, is a UNIT.md already
// on disk, committed into the session baseline with the plugin tree.
func installPublishProject(t *testing.T, seed string) (*Env, string) {
	t.Helper()
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	if seed != "" {
		e.WriteFile(proj, unitPath, seed)
	}
	installPluginTree(t, proj)
	installPluginStructure(t, e, proj)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)
	return e, proj
}

// publishTransition is the sr-file edit that moves the seeded drafting unit
// to published, recording where it went out, citing quotes (none: uncited).
func publishTransition(urls string, quotes ...string) string {
	newStatus := "status: published"
	if urls != "" {
		newStatus += "\npublished_urls: " + urls
	}
	return srFileEdit(unitPath, "status: drafting", newStatus, quotes...)
}

// TestPublish_UncitedCreateRefused: the Write tool creates a unit already at
// status: published. It carries no citation, so it is refused before it
// lands, and the refusal says exactly how to comply (sr-file ... --cite:user).
func TestPublish_UncitedCreateRefused(t *testing.T) {
	e, proj := installPublishProject(t, "")

	res := e.Run(proj, "s-publish-uncited-create", publishPrompt, Turns("done",
		Write("w1", unitPath, publishedUnit),
	))
	if !res.Refused() {
		t.Fatalf("creating a published unit with no citation was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, unitPath) {
		t.Errorf("the preventive guard let an unapproved publish land on disk")
	}
	if !res.Saw("must cite the user's own words (--cite:user)") || !res.Saw("sr-file write "+unitPath) || !res.Saw("--cite:user") {
		t.Errorf("the refusal does not say how to publish with the user's cited approval:\n%s", res.Output)
	}
}

// TestPublish_UncitedTransitionRefused: an sr-file edit moving a drafting
// unit to published, with published_urls but NO citation, is refused; the
// unit stays at drafting, and the refusal hands back the cited edit to make.
func TestPublish_UncitedTransitionRefused(t *testing.T) {
	e, proj := installPublishProject(t, draftingUnit)

	res := e.Run(proj, "s-publish-uncited-edit", publishPrompt, Turns("done",
		Bash("b1", publishTransition(`["https://x.com/nikita/status/1"]`)),
	))
	if !res.Refused() {
		t.Fatalf("an uncited transition into published was not refused:\n%s", res.Output)
	}
	if body := readProj(t, proj, unitPath); !strings.Contains(body, "status: drafting") {
		t.Errorf("the refused transition landed:\n%s", body)
	}
	if !res.Saw("Only the user publishes") || !res.Saw("--old-string 'status: drafting' --new-string 'status: published") {
		t.Errorf("the refusal does not carry the rule's hint:\n%s", res.Output)
	}
	if !res.Saw("must cite the user's own words (--cite:user)") || !res.Saw("sr-file edit "+unitPath) {
		t.Errorf("the refusal does not hand back the cited edit to make:\n%s", res.Output)
	}
}

// TestPublish_UnresolvedApprovalRefused: the transition cites words the user
// never said. The quote resolves to nothing, so the change carries no
// citation — the same outcome as the agent citing its own output, which is
// not in the user pool — and it is refused.
func TestPublish_UnresolvedApprovalRefused(t *testing.T) {
	e, proj := installPublishProject(t, draftingUnit)

	res := e.Run(proj, "s-publish-unresolved", publishPrompt, Turns("done",
		Bash("b1", publishTransition(`["https://x.com/nikita/status/1"]`, "nobody ever actually said this")),
	))
	if !res.Refused() {
		t.Fatalf("a transition citing words the user never said was not refused:\n%s", res.Output)
	}
	if body := readProj(t, proj, unitPath); !strings.Contains(body, "status: drafting") {
		t.Errorf("the ungrounded transition landed:\n%s", body)
	}
}

// TestPublish_CitedTransitionPasses: the transition cites the user's own
// approval and records published_urls:. It lands, and the unit holds no
// approval text or transcript link — the approval rode on the command.
func TestPublish_CitedTransitionPasses(t *testing.T) {
	e, proj := installPublishProject(t, draftingUnit)

	res := e.Run(proj, "s-publish-ok", publishPrompt, Turns("done",
		Bash("b1", publishTransition(`["https://x.com/nikita/status/1"]`, approvalQuote)),
	))
	if res.Refused() {
		t.Fatalf("a cited transition into published was refused:\n%s", res.Output)
	}
	body := readProj(t, proj, unitPath)
	if !strings.Contains(body, "status: published") {
		t.Fatalf("the cited transition did not land:\n%s", body)
	}
	if strings.Contains(body, approvalQuote) || strings.Contains(body, ".jsonl") {
		t.Errorf("the unit stores the approval; it belongs on the command only:\n%s", body)
	}
	if blocks := e.BlockingErrorsFrom(proj, "s-publish-ok", "Stop"); len(blocks) != 0 {
		t.Errorf("the Stop after-check refused a cited publish (its recorded citation was lost?): %v", blocks)
	}
}

// TestPublish_CitedCreateMultipleURLsPass: a unit created straight at
// published with sr-file, citing the approval, and distributed across two
// channels (two published_urls: entries — the list shape) lands.
func TestPublish_CitedCreateMultipleURLsPass(t *testing.T) {
	e, proj := installPublishProject(t, "")

	unit := "---\ncreated: 2026-09-25\ntype: post\nstatus: published\npublished_urls: [\"https://x.com/nikita/status/1\", \"https://reddit.com/r/x/comments/1\"]\n---\n\nAnnouncing the launch.\n"
	res := e.Run(proj, "s-publish-multi", publishPrompt, Turns("done",
		Bash("b1", srFileWrite(unitPath, unit, approvalQuote)),
	))
	if res.Refused() {
		t.Fatalf("a cited publish with two published_urls was refused:\n%s", res.Output)
	}
	if !e.Exists(proj, unitPath) {
		t.Errorf("an admitted multi-channel publish did not land on disk")
	}
}

// TestPublish_MissingPublishedURLsRefused: the transition is cited, but
// published_urls: is absent — still refused, proving both are mandatory.
func TestPublish_MissingPublishedURLsRefused(t *testing.T) {
	e, proj := installPublishProject(t, draftingUnit)

	res := e.Run(proj, "s-publish-nourl", publishPrompt, Turns("done",
		Bash("b1", publishTransition("", approvalQuote)),
	))
	if !res.Refused() {
		t.Fatalf("a cited publish with no published_urls: was not refused:\n%s", res.Output)
	}
	if !res.Saw("published_urls") {
		t.Errorf("the refusal does not name the missing published_urls:\n%s", res.Output)
	}
}

// TestPublish_NonPublishedStatusUnaffected: creating a drafting unit with the
// Write tool — no citation, no published_urls: — is not refused: the gate is
// specific to entering published, not a blanket citation requirement.
func TestPublish_NonPublishedStatusUnaffected(t *testing.T) {
	e, proj := installPublishProject(t, "")

	res := e.Run(proj, "s-publish-notyet", publishPrompt, Turns("done",
		Write("w1", unitPath, draftingUnit),
	))
	if res.Refused() {
		t.Fatalf("a non-published unit was refused by the publish gate:\n%s", res.Output)
	}
	if !e.Exists(proj, unitPath) {
		t.Errorf("a permitted drafting-status write did not land on disk")
	}
}

// TestPublish_UncitedDraftEditPermitted: an uncited edit of a drafting unit
// (the Write tool, status unchanged) is permitted.
func TestPublish_UncitedDraftEditPermitted(t *testing.T) {
	e, proj := installPublishProject(t, draftingUnit)

	edited := strings.Replace(draftingUnit, "Announcing the launch.", "Announcing the launch, today.", 1)
	res := e.Run(proj, "s-publish-draft-edit", publishPrompt, Turns("done",
		Write("w1", unitPath, edited),
	))
	if res.Refused() {
		t.Fatalf("an uncited edit of a drafting unit was refused:\n%s", res.Output)
	}
	if body := readProj(t, proj, unitPath); !strings.Contains(body, "today") {
		t.Errorf("the permitted draft edit did not land:\n%s", body)
	}
}

// TestPublish_UncitedEditOfPublishedUnitPermitted: a unit already published
// in the baseline is edited without a citation (the status stays published).
// Not a transition, so no approval is needed; published_urls: is kept.
func TestPublish_UncitedEditOfPublishedUnitPermitted(t *testing.T) {
	e, proj := installPublishProject(t, publishedUnit)

	edited := strings.Replace(publishedUnit, "Announcing the launch.", "Announcing the launch (typo fixed).", 1)
	res := e.Run(proj, "s-publish-published-edit", publishPrompt, Turns("done",
		Write("w1", unitPath, edited),
	))
	if res.Refused() {
		t.Fatalf("an uncited edit of an already-published unit was refused:\n%s", res.Output)
	}
	if blocks := e.BlockingErrorsFrom(proj, "s-publish-published-edit", "Stop"); len(blocks) != 0 {
		t.Errorf("the Stop after-check refused an edit that is not a transition: %v", blocks)
	}
}

// TestPublish_UncitedTransitionWithInvalidFrontmatterRefused: an uncited
// sr-file edit moves a drafting unit to status: published AND breaks the
// schema elsewhere (type: article is not a unit type). Breaking the schema
// must not hide the publish: the status is read from the frontmatter as
// written, so the transition still needs the user's cited approval, and it
// is refused before it lands.
func TestPublish_UncitedTransitionWithInvalidFrontmatterRefused(t *testing.T) {
	e, proj := installPublishProject(t, draftingUnit)

	res := e.Run(proj, "s-publish-invalid-uncited", publishPrompt, Turns("done",
		Bash("b1", srFileEdit(unitPath, "type: post\nstatus: drafting",
			"type: article\nstatus: published\npublished_urls: [\"https://x.com/nikita/status/1\"]")),
	))
	if !res.Refused() {
		t.Fatalf("an uncited transition into published with schema-invalid frontmatter was not refused:\n%s", res.Output)
	}
	if body := readProj(t, proj, unitPath); !strings.Contains(body, "status: drafting") {
		t.Errorf("the uncited publish landed:\n%s", body)
	}
	if !res.Saw("must cite the user's own words (--cite:user)") {
		t.Errorf("the refusal is not the missing publish approval:\n%s", res.Output)
	}
}

// TestPublish_CitedTransitionWithInvalidFrontmatterRefused: even with the
// user's approval cited, a unit may not sit at status: published with
// frontmatter that breaks unit.cue — check-publish refuses it and names the
// schema problem, rather than reading an invalid document as "no status".
func TestPublish_CitedTransitionWithInvalidFrontmatterRefused(t *testing.T) {
	e, proj := installPublishProject(t, draftingUnit)

	res := e.Run(proj, "s-publish-invalid-cited", publishPrompt, Turns("done",
		Bash("b1", srFileEdit(unitPath, "type: post\nstatus: drafting",
			"type: article\nstatus: published\npublished_urls: [\"https://x.com/nikita/status/1\"]", approvalQuote)),
	))
	if !res.Refused() {
		t.Fatalf("a published unit with schema-invalid frontmatter was not refused:\n%s", res.Output)
	}
	if body := readProj(t, proj, unitPath); !strings.Contains(body, "status: drafting") {
		t.Errorf("the invalid published unit landed:\n%s", body)
	}
	if !res.Saw("unit.cue") || !res.Saw("type") {
		t.Errorf("the refusal does not name the schema problem:\n%s", res.Output)
	}
}
