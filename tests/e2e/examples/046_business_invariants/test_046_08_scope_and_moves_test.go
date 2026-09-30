package e2e

// The second review of the business-invariants rules (#82): marked code can be
// moved, a delete of a correctly pinned file lands, a shell edit of a file no pin
// can involve is not refused, a shell rewrite of a pinned spec line is still
// caught, and the pin itself must be one pinned-spec-holds guards.

import (
	"github.com/sloprail/sloprail/tests/e2e/harness"
	"os/exec"
	"strings"
	"testing"
)

// T046_28: moving marked code is not dropping its pin. Written with its marker in
// the new place first, the code may then leave the old file: another file
// carries the same pin, so nothing pinned changed — no citation, no rule-change
// judge. `git mv` (which the engine sees only at Stop) is the same move.
func TestT046_28_MovingMarkedCodeKeepsItsPin(t *testing.T) {
	t.Run("new-place-first", func(t *testing.T) {
		e := newEnv(t)
		proj, sha := pinnedSpecProjectMarker(t, e, func(proj, sha string) string {
			return "// sr:invariant \"" + proj + "@" + sha + ":SPEC.md#L3-3\""
		})
		e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt", `{"pass": true, "reasoning": ""}`)

		marked := invariantCode(proj+"@"+sha+":SPEC.md#L3-3", refundBody)
		sess := "s-046-28"
		res := e.Run(proj, sess, "move Refund into its own file", Turns("done",
			Write("w1", "src/refund.go", marked),
			Write("w2", "src/charge.go", "package billing\n"),
		).ThenCommit("write the files"))
		if res.Refused() {
			t.Fatalf("moving marked code (new place first) was refused:\n%s", res.Output)
		}
		if n := e.JudgeCalls(proj, "judge-prompt.txt", ruleChangeHeading); n != 0 {
			t.Errorf("the move was sent to the rule-change judge %d time(s)", n)
		}
		if blocks := joinBlocks(e.BlockingErrorsFrom(proj, sess, "Stop")); strings.Contains(blocks, "pinned-spec-holds") {
			t.Errorf("the move was refused at Stop:\n%s", blocks)
		}
		if strings.Contains(readFile(t, proj, "src/charge.go"), "sr:invariant") {
			t.Errorf("the old place still carries the marker")
		}
	})
	t.Run("git-mv", func(t *testing.T) {
		e := newEnv(t)
		proj := pinnedSpecProject(t, e)
		e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)
		sess := "s-046-28-gitmv"
		e.Run(proj, sess, "rename charge.go to refund.go", Turns("done",
			Bash("b1", "git mv src/charge.go src/refund.go"),
		).ThenCommit("write the files"))
		if blocks := joinBlocks(e.BlockingErrorsFrom(proj, sess, "Stop")); strings.Contains(blocks, "pinned-spec-holds") {
			t.Errorf("a `git mv` of marked code was refused at Stop as dropping its pin:\n%s", blocks)
		}
	})
}

// T046_29: a cited delete of a file whose pin is real and current lands, and the
// turn ends. The pinned-invariant judge has no code left to rule on: before, its
// template read the delete's (absent) newContent, failed to render, and so failed
// the check at every Stop — the turn could never end.
func TestT046_29_CitedDeleteOfACurrentPinLands(t *testing.T) {
	e := newEnv(t)
	proj := pinnedSpecProject(t, e)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": "the user asked to delete it"}`)

	const ask = "delete src/charge.go, the refund code moves elsewhere"
	sess := "s-046-29"
	e.Run(proj, sess, ask, Turns("done",
		Bash("b1", "sr-file delete src/charge.go --cite:user '"+ask+"'"),
	).ThenCommit("write the files", harness.CitesUser(ask)))
	if e.Exists(proj, "src/charge.go") {
		t.Fatalf("the cited delete did not land")
	}
	if blocks := e.BlockingErrorsFrom(proj, sess, "Stop"); len(blocks) != 0 {
		t.Fatalf("a cited delete of a correctly pinned file blocked the turn at Stop:\n%s", joinBlocks(blocks))
	}
}

// T046_30: a spec edit the engine cannot see ahead — a script rewriting the file —
// lands, and is caught at Stop: the rule's line changed without the user's words.
func TestT046_30_ShellRewriteOfAPinnedLineIsCaughtAtStop(t *testing.T) {
	e := newEnv(t)
	proj := pinnedSpecProject(t, e)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	sess := "s-046-30"
	e.Run(proj, sess, "allow goodwill refunds", Turns("done",
		Bash("b1", `python3 -c "import pathlib; p=pathlib.Path('SPEC.md'); p.write_text(p.read_text().replace('charge amount.', 'charge amount, except goodwill refunds.'))"`),
	).ThenCommit("write the files"))
	if !strings.Contains(readSpec(t, proj), "except goodwill") {
		t.Fatalf("the rewrite did not land, so this no longer tests the Stop backstop")
	}
	joined := joinBlocks(e.BlockingErrorsFrom(proj, sess, "Stop"))
	if !containsAll(joined, "pinned-spec-holds", "rewrites SPEC.md L3-3") {
		t.Fatalf("a shell rewrite of a pinned spec line was not refused at Stop:\n%s", joined)
	}
}

// T046_31: pinned-spec-holds ships a gate, and a gate refuses a write whose result
// the engine cannot work out ahead when its decision reads the bytes. It triggers
// only on files a pin can involve — specs and marker-carrying files — so a
// `sed -i` of an unrelated file is not its business, while a `sed -i` of a pinned
// spec line is still refused before it lands, by the gate.
func TestT046_31_ShellEditsOutsideAPinsReachAreAdmitted(t *testing.T) {
	e := newEnv(t)
	proj := pinnedSpecProject(t, e)
	e.WriteFile(proj, "README.md", "billing service\n")
	e.WriteFile(proj, "src/util.go", "package billing\n")
	e.CommitAll(proj, "unrelated files")
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	res := e.Run(proj, "s-046-31a", "tidy the readme", Turns("done",
		Bash("b1", "sed -i.bak 's/billing service/Billing service/' README.md"),
		Bash("b2", "sed -i.bak 's/package billing/package billingutil/' src/util.go"),
	).ThenCommit("write the files"))
	if res.Refused() {
		t.Fatalf("a shell edit of files no pin can involve was refused:\n%s", res.Output)
	}
	if !strings.Contains(readFile(t, proj, "README.md"), "Billing service") {
		t.Errorf("the README edit did not land")
	}

	res = e.Run(proj, "s-046-31b", "allow goodwill refunds", Turns("done",
		Bash("b1", "sed -i.bak 's/charge amount\\./charge amount, except goodwill refunds./' SPEC.md"),
	).ThenCommit("write the files"))
	if !res.Refused() || !res.Saw(`gate \"pinned-spec-holds\"`) {
		t.Fatalf("a shell edit of a pinned spec line was not refused before it landed, by the gate:\n%s", res.Output)
	}
	// The hint names the cases a result is unknown in, including the one where it
	// is sr-file's own dry run that failed — not a bare "cannot be worked out",
	// which misled an agent whose quote merely did not resolve.
	if !res.Saw("a shell command that edits it, or an sr-file call whose dry run failed") {
		t.Errorf("the refusal does not say why the result is unknown:\n%s", res.Output)
	}
	if got := readSpec(t, proj); got != billingSpec {
		t.Errorf("the refused shell edit reached SPEC.md:\n%s", got)
	}
}

// T046_32: a pin into a file that is not a spec names a rule nothing guards —
// pinned-spec-holds matches only SPEC.md and specs/**/*.md — so pinned-invariant
// refuses it.
func TestT046_32_PinOutsideTheSpecsIsRefused(t *testing.T) {
	e := newEnv(t)
	proj := biProject(t, e)
	sha := commitSpec(t, e, proj, "docs/rules.md", specV1, "rules outside the specs")
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	sess := "s-046-32"
	e.Run(proj, sess, "pin charge to the rules doc", Turns("done",
		Write("w1", "src/charge.go", invariantCode(proj+"@"+sha+":docs/rules.md#L2-2", "func charge(total int) {}\n")),
	).ThenCommit("write the files"))
	joined := joinBlocks(e.BlockingErrorsFrom(proj, sess, "Stop"))
	if !containsAll(joined, "not a spec file", "pinned-invariant") {
		t.Fatalf("a pin into a non-spec file was not refused:\n%s", joined)
	}
}

// T046_33: a short sha that a branch of the same name shadows resolves to that
// branch's commit, not to the one the code was pinned against. A pin must name
// its commit by its full id, read as an object id only.
func TestT046_33_ShortShaShadowedByARefIsRefused(t *testing.T) {
	e := newEnv(t)
	proj := biProject(t, e)
	shaV1 := commitSpec(t, e, proj, "SPEC.md", specV1, "spec v1")
	commitSpec(t, e, proj, "SPEC.md", "an invariants spec\nan order total must never be negative OR ZERO\n(end)\n", "spec v2")
	short := shaV1[:7]
	e.Git(proj, "branch", short, "HEAD")
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	sess := "s-046-33"
	e.Run(proj, sess, "pin by a short sha", Turns("done",
		Write("w1", "src/charge.go", invariantCode(proj+"@"+short+":SPEC.md#L2-2", "func charge(total int) {}\n")),
	).ThenCommit("write the files"))
	joined := joinBlocks(e.BlockingErrorsFrom(proj, sess, "Stop"))
	if !containsAll(joined, "full commit sha", "pinned-invariant") {
		t.Fatalf("a short sha a branch shadows was not refused:\n%s", joined)
	}
}

// T046_34: a pinned line is compared byte for byte, as pin-still-matches-head.sh
// compares it: a whitespace-only change to it needs the user's words too (and
// would make every pin to it stale).
func TestT046_34_WhitespaceChangeToAPinnedLineIsAChange(t *testing.T) {
	e := newEnv(t)
	proj := pinnedSpecProject(t, e)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	spaced := strings.Replace(billingSpec, "charge amount.", "charge amount.  ", 1)
	res := e.Run(proj, "s-046-34", "tidy the spec", Turns("done",
		Write("w1", "SPEC.md", spaced),
	).ThenCommit("write the files"))
	if !res.Refused() || !res.Saw("rewrites SPEC.md L3-3") {
		t.Fatalf("a whitespace change to a pinned line was not treated as a change:\n%s", res.Output)
	}
}

// The ubuntu runner's GNU seq prints nothing for `seq 0 -1`; macOS's counts down.
// A seq that counts down, first on PATH, makes T046_25 catch the old loop anywhere.
func bsdSeqDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	body := "#!/bin/sh\na=$1; b=$2; i=$a\nif [ \"$a\" -le \"$b\" ]; then while [ \"$i\" -le \"$b\" ]; do echo \"$i\"; i=$((i+1)); done\n" +
		"else while [ \"$i\" -ge \"$b\" ]; do echo \"$i\"; i=$((i-1)); done; fi\n"
	writeExec(t, dir, "seq", body)
	if out, _ := exec.Command(dir+"/seq", "0", "-1").Output(); string(out) != "0\n-1\n" {
		t.Fatalf("the BSD-behaving seq shim does not count down: %q", out)
	}
	return dir
}

// T046_38: a pin the agent wrote this session, and is now correcting, is not a
// pin being dropped. A real second-invariant run wrote `#L3` (malformed), was
// refused by pinned-invariant, and was then refused again for fixing it to
// `#L3-3` — "re-pinning" needed the user's words — and bounced off Stop until it
// cited the user anyway. Only the pins the file held before the session (HEAD's,
// or the baseline's at Stop) can be dropped.
func TestT046_38_CorrectingAPinWrittenThisSessionNeedsNothing(t *testing.T) {
	e := newEnv(t)
	proj := biProject(t, e)
	sha := commitSpec(t, e, proj, "SPEC.md", billingSpec, "spec")
	e.WriteFile(proj, "src/charge.go", "package billing\n")
	e.CommitAll(proj, "unpinned charge")
	e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt", `{"pass": true, "reasoning": ""}`)

	pin := proj + "@" + sha + ":SPEC.md#"
	sess := "s-046-38"
	res := e.Run(proj, sess, "add Refund enforcing rule 2", Turns("done",
		Write("w1", "src/charge.go", invariantCode(pin+"L3", refundBody)),
		Write("w2", "src/charge.go", invariantCode(pin+"L2-2", refundBody)),
		Write("w3", "src/charge.go", invariantCode(pin+"L3-3", refundBody)),
	).ThenCommit("write the files"))
	if res.Refused() {
		t.Fatalf("correcting a pin written this session was refused:\n%s", res.Output)
	}
	if n := e.JudgeCalls(proj, "judge-prompt.txt", ruleChangeHeading); n != 0 {
		t.Errorf("correcting a pin written this session went to the rule-change judge %d time(s)", n)
	}
	if !strings.Contains(readFile(t, proj, "src/charge.go"), "#L3-3") {
		t.Errorf("the corrected pin did not land")
	}
}

// T046_38b: a malformed pin already committed pinned nothing (pinned-invariant
// refuses it), so correcting it drops nothing either.
func TestT046_38b_CorrectingACommittedMalformedPinNeedsNothing(t *testing.T) {
	e := newEnv(t)
	proj := biProject(t, e)
	sha := commitSpec(t, e, proj, "SPEC.md", billingSpec, "spec")
	pin := proj + "@" + sha + ":SPEC.md#"
	e.WriteFile(proj, "src/charge.go", invariantCode(pin+"L3", refundBody))
	e.CommitAll(proj, "a malformed pin")
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	res := e.Run(proj, "s-046-38b", "fix the pin", Turns("done",
		Write("w1", "src/charge.go", invariantCode(pin+"L3-3", refundBody)),
	).ThenCommit("write the files"))
	if res.Refused() {
		t.Fatalf("correcting a committed malformed pin was refused:\n%s", res.Output)
	}
}
