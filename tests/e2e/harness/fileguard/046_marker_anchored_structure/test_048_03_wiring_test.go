package e2e

// TODO(D3): drive verdict via a10n-claude-mock once a10n-cli#470 lands + new mock
// on PATH; today InstallJudgeClaudeCapturing supplies the verdict AND records the
// rendered prompt.
//
// EVENT -> TEMPLATE WIRING for marker-anchored-structure. This judge has NO prepare
// — it renders straight from the CheckPayload's own event
// (endpoint-uses-required-libs.md.j2: `{{ event.newContent if event.newContent else
// event.oldContent }}`). Lower stakes than the prepare-bearing judges, but still
// unproven by a stub that only flips the verdict: the renderer treats an undefined
// variable as empty, so the template would render fine even if event.newContent
// never reached it. This captures the rendered prompt and asserts the marked file's
// content — the sr:endpoint marker AND the endpoint code — is in it, and that
// different content yields a different prompt.

import (
	"strings"
	"testing"
)

// T048_07: the marked endpoint's content reaches the judge prompt. The filename is
// valid (so the script passes to the judge) and the file carries a distinctive
// marker fqn and endpoint code. Both must appear in the rendered prompt — only
// possible if the event's newContent reached the template.
func TestT048_07_EventContentReachesJudgePrompt(t *testing.T) {
	e := newEnv(t)
	proj := masProject(t, e)

	// A distinctive marker fqn and a distinctive route path the judge would rule on.
	const content = "// sr:endpoint ZZ_users.listAll\n" +
		"import express from 'express'\n" +
		"express.Router().get('/zz-user-profiles', (req, res) => prisma.user.findMany())\n"

	e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt",
		`{"pass": true, "reasoning": "Express + prisma, kebab-case route"}`)

	sess := "s-048-07"
	e.Run(proj, sess, "add a conforming endpoint", Turns("done",
		Write("w1", "get-users.ts", content),
	).ThenCommit("write the files"))

	prompt := e.JudgePrompt(proj, "judge-prompt.txt")
	if prompt == "" {
		t.Fatalf("the judge never ran — no prompt captured (did the filename pass to reach the judge?)")
	}
	// The marker fqn: present only if event.newContent (which carries the marker
	// line) reached the template.
	if !strings.Contains(prompt, "ZZ_users.listAll") {
		t.Fatalf("the sr:endpoint marker did not reach the judge prompt — event/template wiring is broken:\n%s", prompt)
	}
	// The distinctive route the judge rules on.
	if !strings.Contains(prompt, "/zz-user-profiles") {
		t.Fatalf("the endpoint code did not reach the judge prompt:\n%s", prompt)
	}
}

// T048_08: different marked content yields a different prompt. Without this, a
// prompt that ignored the event would still pass T048_07. Two runs with two
// different route paths must produce two prompts, each carrying its OWN route.
func TestT048_08_DifferentContentYieldsDifferentPrompt(t *testing.T) {
	runWithRoute := func(tag, route string) string {
		e := newEnv(t)
		proj := masProject(t, e)
		content := "// sr:endpoint users.list\n" +
			"import express from 'express'\n" +
			"express.Router().get('" + route + "', (req, res) => prisma.user.findMany())\n"
		e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt", `{"pass": true, "reasoning": ""}`)
		e.Run(proj, "s-048-08-"+tag, "add endpoint", Turns("done",
			Write("w1", "get-users.ts", content),
		).ThenCommit("write the files"))
		p := e.JudgePrompt(proj, "judge-prompt.txt")
		if p == "" {
			t.Fatalf("[%s] the judge never ran — no prompt captured", tag)
		}
		return p
	}

	const routeA = "/aaa-only-route"
	const routeB = "/bbb-only-route"
	promptA := runWithRoute("A", routeA)
	promptB := runWithRoute("B", routeB)

	if promptA == promptB {
		t.Fatalf("two different endpoint bodies produced identical judge prompts — the prompt does not reflect the event content")
	}
	if !strings.Contains(promptA, routeA) || strings.Contains(promptA, routeB) {
		t.Fatalf("prompt A did not carry ONLY route A:\n%s", promptA)
	}
	if !strings.Contains(promptB, routeB) || strings.Contains(promptB, routeA) {
		t.Fatalf("prompt B did not carry ONLY route B:\n%s", promptB)
	}
}
