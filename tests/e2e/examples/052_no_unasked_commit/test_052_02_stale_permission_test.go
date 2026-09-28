package e2e

// The core #95745 regression case: earlier in the SAME session the user said
// "commit + push"; the agent's LATEST message asks for something unrelated.
// Citing the OLD message must still be refused — the whole bug was an agent
// treating an earlier grant as standing permission for the rest of the
// session. Two Run calls sharing one session id build a real two-turn
// transcript (the same pattern TestT035_02 uses for a context): the first
// call's prompt becomes an older user message once the second call's prompt is
// appended, so the citation in the second call's Bash turn can be pointed at
// either the stale or the live one.

import (
	"strings"
	"testing"
)

const oldAsk = "looks good — go ahead and commit and push whenever it's ready"
const newAsk = "please fix the null check in parser.py"

// T052_05: STALE CITATION, COMMIT — the agent cites the OLD "commit + push"
// message from several turns back while the user's actual latest message
// ("please fix the null check") never asked for this. citation-is-
// latest-user-turn.sh finds the cited line is NOT the latest real user
// message and refuses before the judge is ever asked — this is the exact
// failure #95745 reports, caught deterministically.
func TestT052_05_StaleCommitCitationIsRefused(t *testing.T) {
	e := newEnv(t)
	proj := nucProject(t, e)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "SR052 must not be reached — the stale citation is refused by the script first"}`)

	sess := "s-052-05"

	// Turn 1: the user grants (what will become) a stale permission.
	e.Run(proj, sess, oldAsk, Turns("done",
		Bash("b0", `echo "noted, will commit when asked"`),
	))

	// Turn 2: the user's ACTUAL latest message asks for something unrelated.
	// The agent wrongly cites the OLD message and tries to commit anyway.
	res := e.Run(proj, sess, newAsk, Turns("done",
		Bash("b1", `sr-session trajectory cite `+shq(oldAsk)+` && git commit -am "fix null check"`),
	))

	if !res.Refused() {
		t.Fatalf("a commit cited to an OLDER user message, while the latest message asked for something else, was NOT refused:\n%s", res.Output)
	}
	if !res.Saw("not the user's latest message") {
		t.Errorf("the refusal does not name the stale-citation reason:\n%s", res.Output)
	}
	if res.Saw("SR052 must not be reached") {
		t.Errorf("the judge ran on a stale citation the deterministic script should have refused first")
	}
}

// T052_06: STALE CITATION, PUSH — the same regression for `git push`, so the
// gate's match (both verbs) and the stale-citation script are proven together
// for the other half of #95745's "committed AND pushed twice, unasked".
func TestT052_06_StalePushCitationIsRefused(t *testing.T) {
	e := newEnv(t)
	proj := nucProject(t, e)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "SR052 must not be reached — the stale citation is refused by the script first"}`)

	sess := "s-052-06"

	e.Run(proj, sess, oldAsk, Turns("done",
		Bash("b0", `echo "noted, will push when asked"`),
	))

	res := e.Run(proj, sess, newAsk, Turns("done",
		Bash("b1", `sr-session trajectory cite `+shq(oldAsk)+` && git push`),
	))

	if !res.Refused() {
		t.Fatalf("a push cited to an OLDER user message, while the latest message asked for something else, was NOT refused:\n%s", res.Output)
	}
	if !res.Saw("not the user's latest message") {
		t.Errorf("the refusal does not name the stale-citation reason:\n%s", res.Output)
	}
}

// T052_07: THE LIVE COMPLEMENT — same two-turn session shape as T052_05, but
// the SECOND (latest) message is the one that asks for the commit, and the
// agent correctly cites IT rather than the earlier one. This isolates the
// staleness check as what's driving the refusal above: the identical
// two-message session structure, with only WHICH message is cited changed,
// flips the verdict from refuse to admit.
func TestT052_07_CitingTheLiveLatestMessageAdmits(t *testing.T) {
	e := newEnv(t)
	proj := nucProject(t, e)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "the user's latest message genuinely asks for this commit"}`)

	sess := "s-052-07"
	liveAsk := "please commit this fix"

	e.Run(proj, sess, oldAsk, Turns("done",
		Bash("b0", `echo "noted, will commit when asked"`),
	))

	res := e.Run(proj, sess, liveAsk, Turns("done",
		Write("w1", "src/parser.py", "def parse():\n    return None\n"),
		Bash("b1", `git add -A`),
		Bash("b2", `sr-session trajectory cite `+shq(liveAsk)+` && git commit -m "fix null check"`),
	))

	if res.Refused() {
		t.Fatalf("a commit cited to the user's actual LATEST message (which does ask for it) was refused:\n%s", res.Output)
	}
	if log := e.Git(proj, "log", "--oneline", "-1"); !strings.Contains(log, "fix null check") {
		t.Fatalf("the admitted commit did not actually land (git itself may have rejected an empty commit): %s", log)
	}
}
