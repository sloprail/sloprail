package e2e

import (
	"strings"
	"testing"
)

// unit-publish-approved is a PreFileWrite gate (refuses before the write lands) plus a file-guard (judging the committed changeset at Stop), both over UNIT.md only.
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
		// The seed is history before the rules exist: a rule's range starts at the
		// parent of the commit that installs it, so a seed committed together with
		// the rules would be judged as this cycle's work.
		e.CommitAll(proj, "the seeded unit")
	}
	installPluginTree(t, proj)
	installPluginStructure(t, e, proj)
	e.CommitAll(proj, "install the rules")
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
		t.Errorf("the gate let an unapproved publish land on disk")
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
	).ThenCommit("Publish the unit", CitesUser(approvalQuote)))
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
	).ThenCommit("Publish the unit", CitesUser(approvalQuote)))
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
	).ThenCommit("Draft the unit"))
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
	).ThenCommit("Edit the draft"))
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
	).ThenCommit("Fix a typo"))
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
	if !res.Saw("PUBLISH INVALID") || !res.Saw("type: conflicting values") {
		t.Errorf("the refusal does not name the schema problem (PUBLISH INVALID, and the CUE error for type):\n%s", res.Output)
	}
}

// brokenPublish is a UNIT.md that opens a frontmatter fence but does not parse
// (an unclosed flow sequence), and whose lines set status to published. No
// reader can say what status it holds, so it is not "no status": it is
// undecidable, and a publish guard must treat it as a claim to publish.
const brokenPublish = "---\ncreated: 2026-09-25\ntype: [\nstatus:\n  published\npublished_urls: [\"https://x.com/nikita/status/1\"]\n---\n\nAnnouncing the launch.\n"

// TestPublish_UncitedEditToUnparseableFrontmatterRefused: an uncited sr-file
// edit turns a drafting unit's frontmatter into one that does not parse while
// its lines claim published. Unparseable is undecidable, so the approval is
// required: refused, and UNIT.md stays drafting.
func TestPublish_UncitedEditToUnparseableFrontmatterRefused(t *testing.T) {
	e, proj := installPublishProject(t, draftingUnit)

	res := e.Run(proj, "s-publish-unparseable-uncited", publishPrompt, Turns("done",
		Bash("b1", srFileEdit(unitPath, "type: post\nstatus: drafting",
			"type: [\nstatus:\n  published\npublished_urls: [\"https://x.com/nikita/status/1\"]")),
	))
	if !res.Refused() {
		t.Fatalf("an uncited edit to unparseable frontmatter claiming published was not refused:\n%s", res.Output)
	}
	if body := readProj(t, proj, unitPath); !strings.Contains(body, "status: drafting") {
		t.Errorf("the uncited edit landed:\n%s", body)
	}
	if !res.Saw("must cite the user's own words (--cite:user)") {
		t.Errorf("the refusal is not the missing publish approval:\n%s", res.Output)
	}
}

// TestPublish_CitedUnparseableFrontmatterRefused: with the approval cited, a
// unit whose frontmatter does not parse still cannot land — check-publish
// cannot tell what status it holds, and refuses rather than reading it as
// "not published".
func TestPublish_CitedUnparseableFrontmatterRefused(t *testing.T) {
	e, proj := installPublishProject(t, "")

	res := e.Run(proj, "s-publish-unparseable-cited", publishPrompt, Turns("done",
		Bash("b1", srFileWrite(unitPath, brokenPublish, approvalQuote)),
	))
	if !res.Refused() {
		t.Fatalf("a unit with unparseable frontmatter was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, unitPath) {
		t.Errorf("the unparseable unit landed on disk")
	}
	if !res.Saw("does not parse") {
		t.Errorf("the refusal does not say the frontmatter does not parse:\n%s", res.Output)
	}
}

// TestPublish_UncitedPublishSpellingsRefused: every way of writing a publish
// that a YAML reader reads as status: published — or that no reader can read
// at all — needs the approval. Each is an uncited Write over a drafting unit.
func TestPublish_UncitedPublishSpellingsRefused(t *testing.T) {
	for name, unit := range map[string]string{
		"value on the next line":                           "---\ntype: article\nstatus:\n  published\npublished_urls: [\"u\"]\n---\n",
		"explicit key":                                     "---\n? status\n: published\npublished_urls: [\"u\"]\n---\n",
		"escaped key and value":                            "---\n\"stat\\x75s\": \"pub\\x6cished\"\ntype: article\npublished_urls: [\"u\"]\n---\n",
		"indented fence":                                   " ---\ntype: [\nstatus: published\n---\n",
		"fence with a trailing space":                      "--- \ntype: [\nstatus: published\n---\n",
		"byte order mark":                                  "\ufeff---\nstatus: published\npublished_urls: [\"u\"]\n---\n",
		"fence trailed by a no-break space":                "---\u00a0\ntype: [\nstatus: published\n---\n",
		"fence never closed":                               "---\ntype: post\nstatus: published\npublished_urls: [\"https://x\"]\n",
		"fence never closed, then a body":                  "---\ntype: post\nstatus: published\npublished_urls: [\"https://x\"]\n\nAnnouncing the launch.\n",
		"fence never closed, CRLF":                         "---\r\ntype: post\r\nstatus: published\r\npublished_urls: [\"https://x\"]\r\n",
		"fence never closed, ... ended":                    "---\ntype: post\nstatus: published\npublished_urls: [\"https://x\"]\n...\n",
		"fence never closed, blank line before status":     "---\ntype: post\n\nstatus: published\npublished_urls: [\"https://x\"]\n",
		"fence never closed, CRLF blank line":              "---\r\ntype: post\r\n\r\nstatus: published\r\npublished_urls: [\"https://x\"]\r\n",
		"fence never closed, whitespace-only line":         "---\ntype: post\n \t\nstatus: published\npublished_urls: [\"https://x\"]\n",
		"fence never closed, blank line in a block scalar": "---\nnote: |\n  line one\n\n  line two\nstatus: published\npublished_urls: [\"https://x\"]\n",
		"fence never closed, comment then status":          "---\n# a comment\n\nstatus: published\npublished_urls: [\"https://x\"]\n",
		"a second YAML document":                           "---\nstatus: drafting\n...\nstatus: published\npublished_urls: [\"u\"]\n---\n",
		"two merge keys":                                   "---\n<<: {status: drafting}\n<<: {status: published}\npublished_urls: [\"u\"]\n---\n",
		"capitalised":                                      "---\ntype: post\nstatus: Published\npublished_urls: [\"u\"]\n---\n",
	} {
		t.Run(name, func(t *testing.T) {
			e, proj := installPublishProject(t, draftingUnit)
			res := e.Run(proj, "s-publish-spelling", publishPrompt, Turns("done",
				Write("w1", unitPath, unit),
			))
			if !res.Refused() {
				t.Fatalf("an uncited publish spelled %q was not refused:\n%s", name, res.Output)
			}
			if !res.Saw("must cite the user's own words (--cite:user)") {
				t.Errorf("the publish spelled %q was not refused for its missing approval:\n%s", name, res.Output)
			}
			if body := readProj(t, proj, unitPath); body != draftingUnit {
				t.Errorf("the uncited publish landed:\n%s", body)
			}
		})
	}
}

// TestPublish_InvalidNonPublishedUnitPermitted: no over-refusal. A unit that
// breaks unit.cue but parses to a status other than published is not this
// guard's business — an uncited write of it lands.
func TestPublish_InvalidNonPublishedUnitPermitted(t *testing.T) {
	for name, unit := range map[string]string{
		"invalid type, drafting":                  "---\ncreated: 2026-09-25\ntype: article\nstatus: drafting\n---\n\nAnnouncing the launch.\n",
		"status unpublished":                      "---\ntype: post\nstatus: unpublished\n---\n",
		"comment naming publish":                  "---\ntype: post\nstatus: drafting # not published yet\n---\n",
		"no frontmatter at all":                   "Announcing the launch; status: published later.\n",
		"integer key":                             "---\ntype: post\nstatus: drafting\n1: x\n---\n",
		"null key and custom tag":                 "---\nnull: x\nx: !custom foo\nstatus: drafting\n---\n",
		"complex key":                             "---\n? [a, b]\n: c\nstatus: drafting\n---\n",
		"inf, nan and a huge int":                 "---\na: .inf\nb: .nan\nn: 123456789012345678901234567890\nstatus: drafting\n---\n",
		"horizontal rule, no frontmatter":         "---\n\nAnnouncing the launch.\n",
		"drafting after a byte-order mark":        "\ufeff---\nstatus: drafting\n---\n",
		"drafting after a blank line":             "\n---\nstatus: drafting\n---\n",
		"drafting, fence never closed":            "---\ntype: post\nstatus: drafting\n",
		"prose under a rule, colons":              "---\n\nWhy it matters: speed. Also: cost.\n",
		"prose under a rule, heading":             "---\n\n# Launch post\n\nNote: this ships Monday: be ready.\n",
		"prose under a rule, list":                "---\n\n- item one\nplain text after list\n",
		"prose under a rule, code and link":       "---\n\n`code`: and [link](http://x) {braces}\n",
		"prose under a rule, quote":               "---\n\nJust a paragraph.\n\n> quote: here\n",
		"prose under a rule, mention":             "---\n\n@mention starts a line\n",
		"prose under a rule, a later status line": "---\n\nRelease notes below.\n\nstatus: shipped to everyone, published today\n",
	} {
		t.Run(name, func(t *testing.T) {
			e, proj := installPublishProject(t, draftingUnit)
			res := e.Run(proj, "s-publish-invalid-draft", publishPrompt, Turns("done",
				Write("w1", unitPath, unit),
			).ThenCommit("Edit the unit"))
			if res.Refused() {
				t.Fatalf("a unit that is not published (%s) was refused:\n%s", name, res.Output)
			}
			if body := readProj(t, proj, unitPath); body != unit {
				t.Errorf("the permitted write did not land:\n%s", body)
			}
		})
	}
}

// TestPublish_EditLeavingPublishedUnitInvalidRefused: a unit already published
// needs no new approval to be edited, but an edit that leaves it at published
// with frontmatter that breaks unit.cue is refused — a published unit must stay
// valid, not only one entering published.
func TestPublish_EditLeavingPublishedUnitInvalidRefused(t *testing.T) {
	e, proj := installPublishProject(t, publishedUnit)

	edited := strings.Replace(publishedUnit, "type: post", "type: article", 1)
	res := e.Run(proj, "s-publish-published-invalid", publishPrompt, Turns("done",
		Write("w1", unitPath, edited),
	))
	if !res.Refused() {
		t.Fatalf("an edit leaving a published unit schema-invalid was not refused:\n%s", res.Output)
	}
	if !res.Saw("PUBLISH INVALID") || !res.Saw("type: conflicting values") {
		t.Errorf("the refusal does not name the schema problem:\n%s", res.Output)
	}
	if body := readProj(t, proj, unitPath); body != publishedUnit {
		t.Errorf("the invalid edit landed:\n%s", body)
	}
	if res.Saw("must cite the user's own words") {
		t.Errorf("an edit to an already-published unit asked for a new approval:\n%s", res.Output)
	}
}

// TestPublish_OlderSRFileAsksForTheUpgrade: against an sloprail binary set whose
// sr-file has no `field` command (older than this plugin needs), a plain
// drafting edit is refused — nothing can be read — and the refusal names the
// upgrade, not a frontmatter to fix nor a publish to approve. The hook runs
// checks with the sr-file beside sr-session, so the stale set is both: an
// sr-session that forwards to the real engine, beside an sr-file that lacks
// the command.
func TestPublish_OlderSRFileAsksForTheUpgrade(t *testing.T) {
	e, proj := installPublishProject(t, draftingUnit)
	e.InstallShim("sr-session", "#!/bin/sh\nexec "+shq(e.BinPath("sr-session"))+` "$@"
`)
	e.InstallShim("sr-file", `#!/bin/sh
case "$1" in
  --version) echo "sr-file version 0.2.1"; exit 0 ;;
  field) echo 'Error: unknown command "field" for "sr-file"' >&2; exit 1 ;;
esac
exec `+shq(e.BinPath("sr-file"))+` "$@"
`)

	edited := strings.Replace(draftingUnit, "Announcing the launch.", "Announcing the launch, today.", 1)
	res := e.Run(proj, "s-publish-old-srfile", publishPrompt, Turns("done",
		Write("w1", unitPath, edited),
	))
	if !res.Refused() {
		t.Fatalf("with no way to read the status, a unit write was not refused:\n%s", res.Output)
	}
	if !res.Saw("sloprail-content needs `sr-file field`") || !res.Saw("upgrade the sloprail binaries") {
		t.Errorf("the refusal does not name the upgrade:\n%s", res.Output)
	}
	// The minimum is what the code needs — a release NEWER than 0.2.1 — and the
	// refusal names what is installed, so a user on 0.2.1 is never told to
	// upgrade to "0.2.1 or newer", the release they already have.
	if !res.Saw("newer than 0.2.1") || !res.Saw("installed sr-file is 0.2.1") {
		t.Errorf("the refusal does not ask for a release newer than 0.2.1 and name the installed 0.2.1:\n%s", res.Output)
	}
	if res.Saw("or newer") {
		t.Errorf("the refusal asks for a minimum the installed release already meets:\n%s", res.Output)
	}
	if res.Saw("fix the frontmatter") || res.Saw("Only the user publishes") {
		t.Errorf("the refusal blames the frontmatter or asks for an approval instead of the upgrade:\n%s", res.Output)
	}
}
