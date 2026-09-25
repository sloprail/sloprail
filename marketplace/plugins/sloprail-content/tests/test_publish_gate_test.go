package e2e

import "testing"

// unit-publish-approved is a PREVENTIVE file-guard over UNIT.md only. A unit
// may not reach status: published without BOTH:
//   - a grounded approval quote CITED IN THE BODY (not frontmatter — an
//     earlier draft's `approved:` field moved here, so the citation lives
//     with the prose it grounds, the same way a task's ask citation does) —
//     a [quote](jsonl) link grounding to a REAL USER MESSAGE, via the
//     vendored cite-links.sh (the same cite --source-types user mechanism
//     sloprail-tasks's task body uses);
//   - published_urls: (frontmatter, a LIST — an earlier draft's singular
//     published_url: is gone, since a unit may ship on more than one
//     channel).
//
// These prove: status: published with no approval citation in the body at
// all is refused; a body citation whose quote does NOT ground to a real user
// message (a fabricated quote, standing in for "the agent's own output" —
// cite's exclusion of non-user pools is proven directly by sloprail-tasks's
// own suite, and this plugin reuses that exact mechanism verbatim) is
// refused; and a valid approval plus published_urls passes and lands.

// TestPublish_NoApprovalRefused: status: published with no approval citation
// in the body at all is refused before it lands.
func TestPublish_NoApprovalRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, proj)
	installPluginStructure(t, e, proj)

	sess := "s-publish-noapproval"
	body := unitFrontmatter(
		"transcript_path: /abs/s.jsonl\ncreated: 2026-09-25\ntype: post\nstatus: published\npublished_urls: [\"https://x.com/nikita/status/1\"]\n",
		"Announcing the launch.")

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", unitPath, body),
	))
	if !res.Refused() {
		t.Fatalf("status: published with no approval citation in the body was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, unitPath) {
		t.Errorf("the preventive guard let an unapproved publish land on disk")
	}
	if !res.Saw("PUBLISH NOT APPROVED") {
		t.Errorf("the refusal was not the publish-gate's own reason:\n%s", res.Output)
	}
}

// TestPublish_UngroundedApprovalRefused: the body carries a citation link
// whose quote does not resolve to any real user message in the transcript —
// a fabricated quote, the same shape a citation of "the agent's own output"
// takes (neither exists in the user pool, which is the property cite tests;
// sloprail-tasks's own suite proves cite's exclusion of the agent's turns and
// tool results directly — this test proves THIS plugin's check calls that
// same mechanism and refuses on its negative verdict).
func TestPublish_UngroundedApprovalRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, proj)
	installPluginStructure(t, e, proj)

	sess := "s-publish-ungrounded"
	tp := e.TranscriptPath(proj, sess)
	approval := cite("nobody ever actually said this", tp, 1)
	body := unitFrontmatter(
		"transcript_path: /abs/s.jsonl\ncreated: 2026-09-25\ntype: post\nstatus: published\npublished_urls: [\"https://x.com/nikita/status/1\"]\n",
		"Announcing the launch.\n\n## Approval\nThe user said: "+approval)

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", unitPath, body),
	))
	if !res.Refused() {
		t.Fatalf("a body approval citation that does not ground to a real user message was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, unitPath) {
		t.Errorf("the preventive guard let an ungrounded approval land on disk")
	}
}

// TestPublish_ValidApprovalPasses: the body cites a substring of the
// session's real line-1 user prompt (authPrompt) — grounds for real via cite
// — and published_urls: is set. The write lands.
func TestPublish_ValidApprovalPasses(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, proj)
	installPluginStructure(t, e, proj)

	sess := "s-publish-ok"
	tp := e.TranscriptPath(proj, sess)
	approval := cite("Approve writing rules and publishing", tp, 1)
	body := unitFrontmatter(
		"transcript_path: /abs/s.jsonl\ncreated: 2026-09-25\ntype: post\nstatus: published\npublished_urls: [\"https://x.com/nikita/status/1\"]\n",
		"Announcing the launch.\n\n## Approval\nThe user said: "+approval)

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", unitPath, body),
	))
	if res.Refused() {
		t.Fatalf("a grounded body approval + published_urls was refused:\n%s", res.Output)
	}
	if !e.Exists(proj, unitPath) {
		t.Errorf("an admitted publish write did not land on disk")
	}
}

// TestPublish_MultipleURLsPass: a unit distributed across two channels
// carries two entries in published_urls: — proves the list shape (an
// earlier draft's singular published_url: string could not express this).
func TestPublish_MultipleURLsPass(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, proj)
	installPluginStructure(t, e, proj)

	sess := "s-publish-multi"
	tp := e.TranscriptPath(proj, sess)
	approval := cite("Approve writing rules and publishing", tp, 1)
	body := unitFrontmatter(
		"transcript_path: /abs/s.jsonl\ncreated: 2026-09-25\ntype: post\nstatus: published\npublished_urls: [\"https://x.com/nikita/status/1\", \"https://reddit.com/r/x/comments/1\"]\n",
		"Announcing the launch.\n\n## Approval\nThe user said: "+approval)

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", unitPath, body),
	))
	if res.Refused() {
		t.Fatalf("a grounded approval with two published_urls was refused:\n%s", res.Output)
	}
	if !e.Exists(proj, unitPath) {
		t.Errorf("an admitted multi-channel publish write did not land on disk")
	}
}

// TestPublish_MissingPublishedURLsRefused: the body approval grounds, but
// published_urls: is absent — still refused, proving BOTH are mandatory, not
// just the approval.
func TestPublish_MissingPublishedURLsRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, proj)
	installPluginStructure(t, e, proj)

	sess := "s-publish-nourl"
	tp := e.TranscriptPath(proj, sess)
	approval := cite("Approve writing rules and publishing", tp, 1)
	body := unitFrontmatter(
		"transcript_path: /abs/s.jsonl\ncreated: 2026-09-25\ntype: post\nstatus: published\n",
		"Announcing the launch.\n\n## Approval\nThe user said: "+approval)

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", unitPath, body),
	))
	if !res.Refused() {
		t.Fatalf("status: published with an approval but no published_urls: was not refused:\n%s", res.Output)
	}
}

// TestPublish_NonPublishedStatusUnaffected: a unit at status: drafting with
// neither an approval citation nor published_urls: is NOT refused by this
// guard — the gate is specific to status: published, not a blanket
// requirement.
func TestPublish_NonPublishedStatusUnaffected(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, proj)
	installPluginStructure(t, e, proj)

	sess := "s-publish-notyet"
	body := unitFrontmatter(
		"transcript_path: /abs/s.jsonl\ncreated: 2026-09-25\ntype: post\nstatus: drafting\n",
		"Still drafting.")

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", unitPath, body),
	))
	if res.Refused() {
		t.Fatalf("a non-published unit was refused by the publish gate:\n%s", res.Output)
	}
	if !e.Exists(proj, unitPath) {
		t.Errorf("a permitted drafting-status write did not land on disk")
	}
}
