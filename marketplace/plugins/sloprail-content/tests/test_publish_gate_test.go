package e2e

import "testing"

// unit-publish-approved is a PREVENTIVE file-guard over UNIT.md only. A unit
// may not reach status: published without BOTH approved: (a [quote](jsonl)
// citation grounding to a REAL USER MESSAGE, via the vendored cite-links.sh —
// the same cite --source-types user mechanism sloprail-tasks's task body
// uses) and published_url:.
//
// These prove: status: published with no approved: is refused; an approved:
// citation whose quote does NOT ground to a real user message (a fabricated
// quote, standing in for "the agent's own output" — cite's exclusion of
// non-user pools is proven directly by sloprail-tasks's own suite, and this
// plugin reuses that exact mechanism verbatim) is refused; and a valid
// approval plus published_url passes and lands.

// TestPublish_NoApprovedRefused: status: published with no approved: field at
// all is refused before it lands.
func TestPublish_NoApprovedRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, proj)

	sess := "s-publish-noapproval"
	body := unitFrontmatter(
		"transcript_path: /abs/s.jsonl\ncreated: 2026-09-25\ntype: post\nstatus: published\npublished_url: \"https://x.com/nikita/status/1\"\n",
		"Announcing the launch.")

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", unitPath, body),
	))
	if !res.Refused() {
		t.Fatalf("status: published with no approved: was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, unitPath) {
		t.Errorf("the preventive guard let an unapproved publish land on disk")
	}
	if !res.Saw("PUBLISH NOT APPROVED") {
		t.Errorf("the refusal was not the publish-gate's own reason:\n%s", res.Output)
	}
}

// TestPublish_UngroundedApprovalRefused: approved: carries a citation link
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

	sess := "s-publish-ungrounded"
	tp := e.TranscriptPath(proj, sess)
	approval := cite("nobody ever actually said this", tp, 1)
	body := unitFrontmatter(
		"transcript_path: /abs/s.jsonl\ncreated: 2026-09-25\ntype: post\nstatus: published\napproved: \""+approval+"\"\npublished_url: \"https://x.com/nikita/status/1\"\n",
		"Announcing the launch.")

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", unitPath, body),
	))
	if !res.Refused() {
		t.Fatalf("an approved: citation that does not ground to a real user message was not refused:\n%s", res.Output)
	}
	if e.Exists(proj, unitPath) {
		t.Errorf("the preventive guard let an ungrounded approval land on disk")
	}
}

// TestPublish_ValidApprovalPasses: approved: cites a substring of the
// session's real line-1 user prompt (authPrompt) — grounds for real via cite
// — and published_url: is set. The write lands.
func TestPublish_ValidApprovalPasses(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, proj)

	sess := "s-publish-ok"
	tp := e.TranscriptPath(proj, sess)
	approval := cite("Approve writing rules and publishing", tp, 1)
	body := unitFrontmatter(
		"transcript_path: /abs/s.jsonl\ncreated: 2026-09-25\ntype: post\nstatus: published\napproved: \""+approval+"\"\npublished_url: \"https://x.com/nikita/status/1\"\n",
		"Announcing the launch.")

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", unitPath, body),
	))
	if res.Refused() {
		t.Fatalf("a grounded approval + published_url was refused:\n%s", res.Output)
	}
	if !e.Exists(proj, unitPath) {
		t.Errorf("an admitted publish write did not land on disk")
	}
}

// TestPublish_MissingPublishedURLRefused: approved: grounds, but
// published_url: is absent — still refused, proving BOTH fields are
// mandatory, not just approved:.
func TestPublish_MissingPublishedURLRefused(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, proj)

	sess := "s-publish-nourl"
	tp := e.TranscriptPath(proj, sess)
	approval := cite("Approve writing rules and publishing", tp, 1)
	body := unitFrontmatter(
		"transcript_path: /abs/s.jsonl\ncreated: 2026-09-25\ntype: post\nstatus: published\napproved: \""+approval+"\"\n",
		"Announcing the launch.")

	res := e.Run(proj, sess, authPrompt, Turns("done",
		Write("w1", unitPath, body),
	))
	if !res.Refused() {
		t.Fatalf("status: published with approved: but no published_url: was not refused:\n%s", res.Output)
	}
}

// TestPublish_NonPublishedStatusUnaffected: a unit at status: drafting with
// neither approved: nor published_url: is NOT refused by this guard — the
// gate is specific to status: published, not a blanket requirement.
func TestPublish_NonPublishedStatusUnaffected(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	installPluginTree(t, proj)

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
