package e2e

// The third review of the business-invariants rules (#82): a pin's sha is read
// as an object id, never through a ref; the unknown-result remedy leads with
// making the edit checkable; a pin must name this project's repository; the two
// statements of the spec convention agree; and one command deleting two files
// that carry the same pin does not let each vouch for the other.

import (
	"github.com/sloprail/sloprail/tests/e2e/harness"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/filemod"
	"github.com/sloprail/sloprail/internal/guardrail"
	"github.com/sloprail/sloprail/internal/module"
)

// T046_43: a short-sha pin still pins its lines when a branch named like the sha
// is created, and when its sha names no commit at all: a pin's range names lines
// of its path, and the citation applies to changing them. Before, a pin whose sha
// did not resolve (or resolved through the branch to another commit) was taken
// to pin nothing, so the rewrite went through uncited.
func TestT046_43_UnresolvedShaStillPinsItsLines(t *testing.T) {
	for _, c := range []struct {
		name  string
		sha   func(sha string) string
		setup string
	}{
		{"short-sha-shadowed-by-a-branch", func(sha string) string { return sha[:7] }, "git branch %s HEAD"},
		{"sha-naming-no-commit", func(string) string { return "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef" }, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			var short string
			proj, _ := pinnedSpecProjectMarker(t, e, func(proj, sha string) string {
				short = c.sha(sha)
				return "// sr:invariant \"" + proj + "@" + short + ":SPEC.md#L3-3\""
			})
			e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)
			turns := []Turn{}
			if c.setup != "" {
				turns = append(turns, Bash("b0", strings.ReplaceAll(c.setup, "%s", short)))
			}
			turns = append(turns, Write("w1", "SPEC.md", relaxedSpec))
			sess := "s-046-43-" + c.name
			res := e.Run(proj, sess, "allow goodwill refunds", Turns("done", turns...))
			if !res.Refused() || !res.Saw("rewrites SPEC.md L3-3") {
				t.Fatalf("an uncited rewrite of a line pinned by an unresolved sha was not refused:\n%s", res.Output)
			}
			if got := readSpec(t, proj); got != billingSpec {
				t.Errorf("the rewrite reached SPEC.md:\n%s", got)
			}
		})
	}
}

// T046_44: a shell rename in a marked file is refused (its result cannot be
// checked first), and the refusal leads with making the edit checkable — which
// needs no citation when it keeps the pin — rather than with citing the user,
// whose words a rename would never have.
func TestT046_44_UnknownResultRemedyLeadsWithACheckableEdit(t *testing.T) {
	e := newEnv(t)
	proj, sha := pinnedSpecProjectMarker(t, e, func(proj, sha string) string {
		return "// sr:invariant \"" + proj + "@" + sha + ":SPEC.md#L3-3\""
	})
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	renamed := invariantCode(proj+"@"+sha+":SPEC.md#L3-3",
		"func Refund(chargedCents, amount int) bool { return amount <= chargedCents }\n")
	res := e.Run(proj, "s-046-44", "rename charged to chargedCents", Turns("done",
		Bash("b1", "sed -i.bak 's/charged/chargedCents/g' src/charge.go"),
		Write("w1", "src/charge.go", renamed),
	).ThenCommit("write the files"))
	i := strings.Index(res.Output, "Make this edit with Edit or Write")
	j := strings.Index(res.Output, "cite their words")
	if i < 0 || (j >= 0 && j < i) {
		t.Fatalf("the refusal of a shell edit did not lead with making it checkable:\n%s", res.Output)
	}
	if !strings.Contains(readFile(t, proj, "src/charge.go"), "chargedCents") {
		t.Errorf("the same rename made with Write, keeping the pin, did not land")
	}
}

// T046_45: a pin into another repository names text nothing in this project
// guards, and pinned-invariant refuses it.
func TestT046_45_PinIntoAnotherRepositoryIsRefused(t *testing.T) {
	other := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"-c", "user.email=t@t", "-c", "user.name=t", "-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", "x"}} {
		if out, err := exec.Command("git", append([]string{"-C", other}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	writeExec(t, other, "SPEC.md", billingSpec)
	if out, err := exec.Command("sh", "-c", "cd "+other+" && git add -A && git -c user.email=t@t -c user.name=t -c commit.gpgsign=false commit -qm spec").CombinedOutput(); err != nil {
		t.Fatalf("commit: %v %s", err, out)
	}
	otherSha, _ := exec.Command("git", "-C", other, "rev-parse", "HEAD").Output()

	e := newEnv(t)
	proj := biProject(t, e)
	commitSpec(t, e, proj, "SPEC.md", billingSpec, "spec")
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)
	sess := "s-046-45"
	e.Run(proj, sess, "pin to the other repo", Turns("done",
		Write("w1", "src/charge.go", invariantCode(other+"@"+strings.TrimSpace(string(otherSha))+":SPEC.md#L3-3", refundBody)),
	).ThenCommit("write the files"))
	joined := joinBlocks(e.BlockingErrorsFrom(proj, sess, "Stop"))
	if !containsAll(joined, "not this project's", "pinned-invariant") {
		t.Fatalf("a pin into another repository was not refused:\n%s", joined)
	}
}

// T046_46: the spec convention is stated three times — pinned-spec-holds' match in
// its file-guard and in its gate's triggers (what the rule watches) and pin.sh's
// SPEC_PATH_RE (where a pin may point). If they disagree, a pin can name a file
// the rule does not watch. All are run over the same paths and must agree.
func TestT046_46_SpecConventionAgrees(t *testing.T) {
	root := filepath.Join(repoRoot(t), "examples", "business-invariants", ".sloprail", "file-guard")
	raw, err := os.ReadFile(filepath.Join(root, "pinned-spec-holds", "file-guard.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var guard struct {
		Match string `yaml:"match"`
	}
	if err := yaml.Unmarshal(raw, &guard); err != nil {
		t.Fatal(err)
	}
	match, err := guardrail.CompileFileMatch(guard.Match)
	if err != nil {
		t.Fatalf("compile the guard's match: %v", err)
	}

	paths := map[string]bool{
		"SPEC.md": true, "a/SPEC.md": true, "spec.md": true, "specs/x/y.md": true, "specs/billing.md": true,
		"docs/rules.md": false, "SPEC.markdown": false, "specs.md": false, "src/charge.go": false, "specs/notes.txt": false,
	}
	pinSh := filepath.Join(root, "pinned-invariant", "pin.sh")
	for p, want := range paths {
		watched, err := match.Match(event.Event{Fields: map[string]any{
			"path": p, "status": "M", "markers": []any{}, "oldMarkers": []any{}, "context": map[string]any{},
		}})
		if err != nil {
			t.Fatalf("%s: match: %v", p, err)
		}
		err = exec.Command("bash", "-c", `. "$1"; printf '%s' "$2" | grep -Eiq "$SPEC_PATH_RE"`, "_", pinSh, p).Run()
		pinnable := err == nil
		if watched != want || pinnable != want {
			t.Errorf("%s: the guard watches it: %v, a pin may name it: %v, want both %v", p, watched, pinnable, want)
		}
		for _, trig := range gateTriggers(t) {
			gated, err := trig.match.Match(event.Event{Fields: map[string]any{
				"event": map[string]any{"path": p, "newMarkers": []any{}, "oldMarkers": []any{}}, "context": map[string]any{},
			}})
			if err != nil {
				t.Fatalf("%s: gate %s match: %v", p, trig.event, err)
			}
			if gated != want {
				t.Errorf("%s: the gate's %s trigger watches it: %v, want %v", p, trig.event, gated, want)
			}
		}
	}
}

// T046_47: `rm a.go b.go`, both carrying the same pin. The engine asks the gate
// about every file a command touches, before it runs, and each file sees the
// other still holding the pin, so the command runs. At Stop both
// are gone, neither holds the pin, and both deletes are refused by the file-guard:
// the after-check is the backstop, and names both files.
func TestT046_47_DeletingTwoHoldersOfOnePinInOneCommand(t *testing.T) {
	e := newEnv(t)
	proj := biProject(t, e)
	sha := commitSpec(t, e, proj, "SPEC.md", billingSpec, "spec")
	marked := invariantCode(proj+"@"+sha+":SPEC.md#L3-3", refundBody)
	e.WriteFile(proj, "src/a.go", marked)
	e.WriteFile(proj, "src/b.go", marked)
	e.CommitAll(proj, "two holders of one pin")
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	sess := "s-046-47"
	settleBaseline(t, e, proj, sess, "clean up")
	e.Run(proj, sess, "go on", Turns("done",
		Bash("b1", "rm src/a.go src/b.go"),
	).ThenCommit("write the files"))
	joined := joinBlocks(e.BlockingErrorsFrom(proj, sess, "Stop"))
	if !containsAll(joined, "moves src/a.go off", "moves src/b.go off", "pinned-spec-holds") {
		t.Fatalf("deleting both holders of a pin in one command was not refused at Stop for both:\n%s", joined)
	}
}

// T046_48: a replace ref (`git replace <old> <new>`) makes git read another
// commit in place of the one a pin names. A pin to a since-reworded rule, with
// its commit replaced by the reworded one, would then read the new wording and
// match HEAD. The scripts read objects as themselves (GIT_NO_REPLACE_OBJECTS).
func TestT046_48_ReplaceRefDoesNotStandInForThePinnedCommit(t *testing.T) {
	e := newEnv(t)
	proj := biProject(t, e)
	shaV1 := commitSpec(t, e, proj, "SPEC.md", specV1, "spec v1")
	shaV2 := commitSpec(t, e, proj, "SPEC.md", "an invariants spec\nan order total must never be negative OR ZERO\n(end)\n", "spec v2")
	e.Git(proj, "replace", shaV1, shaV2)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	sess := "s-046-48"
	e.Run(proj, sess, "code pinned to v1", Turns("done",
		Write("w1", "src/charge.go", invariantCode(proj+"@"+shaV1+":SPEC.md#L2-2", "func charge(total int) {}\n")),
	).ThenCommit("write the files"))
	if joined := joinBlocks(e.BlockingErrorsFrom(proj, sess, "Stop")); !containsAll(joined, "since changed at HEAD", "pinned-invariant") {
		t.Fatalf("a replace ref let a stale pin read the new wording:\n%s", joined)
	}
}

// T046_49: a new line in a pinned spec — here an exception to the pinned rule 2,
// the line a real run added uncited (231517Z) — needs the user's words, even
// though no pinned line changes. A file that is not a spec is not affected.
func TestT046_49_UncitedNewLineInAPinnedSpecIsRefused(t *testing.T) {
	e := newEnv(t)
	proj := pinnedSpecProject(t, e)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	withException := strings.Replace(billingSpec, "(end)\n", "3. A goodwill refund may include a $5 courtesy credit on top of the charge.\n(end)\n", 1)
	res := e.Run(proj, "s-046-49", "allow goodwill refunds", Turns("done",
		Write("w1", "SPEC.md", withException),
		Write("w2", "NOTES.md", "goodwill refunds: ask the user\n"),
	).ThenCommit("write the files"))
	if !res.Refused() || !res.Saw("every rule in a pinned spec is the user's") {
		t.Fatalf("an uncited new line in a pinned spec was not refused:\n%s", res.Output)
	}
	if got := readSpec(t, proj); got != billingSpec {
		t.Errorf("the uncited line reached SPEC.md:\n%s", got)
	}
	if !e.Exists(proj, "NOTES.md") {
		t.Errorf("a write to a file that is not a spec was refused")
	}
}

// T046_50: a new rule the user asked for, cited, is admitted.
func TestT046_50_CitedNewRuleTheUserAskedForIsAdmitted(t *testing.T) {
	e := newEnv(t)
	proj := pinnedSpecProject(t, e)
	e.InstallJudgeClaudeCapturing(proj, "judge-prompt.txt", `{"pass": true, "reasoning": "the user asked for rule 3"}`)

	const ask = "add a rule 3 to the spec: a refund must be issued within 30 days of the charge"
	res := e.Run(proj, "s-046-50", ask, Turns("done",
		Bash("b1", "sr-file edit SPEC.md --old-string '(end)' --new-string '3. A refund must be issued within 30 days of the charge.\n(end)' --cite:user '"+ask+"'"),
	).ThenCommit("write the files", harness.CitesUser(ask)))
	if res.Refused() {
		t.Fatalf("a cited new rule the user asked for was refused:\n%s", res.Output)
	}
	if !strings.Contains(readSpec(t, proj), "within 30 days") {
		t.Errorf("the cited rule did not land:\n%s", readSpec(t, proj))
	}
	if n := e.JudgeCalls(proj, "judge-prompt.txt", ruleChangeHeading); n == 0 {
		t.Errorf("the cited change to a pinned spec was not judged")
	}
}

// T046_54: whitespace outside the pinned lines of a pinned spec — trailing spaces
// trimmed or added, a trailing newline — changes no rule and needs no citation.
// The pinned lines stay byte-exact (T046_34 refuses a trailing space on L3).
func TestT046_54_WhitespaceOutsidePinnedLinesNeedsNothing(t *testing.T) {
	e := newEnv(t)
	proj := pinnedSpecProject(t, e)
	e.InstallJudgeClaude(`{"pass": true, "reasoning": ""}`)

	reformatted := strings.Replace(billingSpec, "Billing invariants\n", "Billing invariants   \n", 1) + "\n"
	res := e.Run(proj, "s-046-54", "tidy the spec", Turns("done",
		Write("w1", "SPEC.md", reformatted),
	).ThenCommit("write the files"))
	if res.Refused() {
		t.Fatalf("a whitespace-only change outside the pinned lines was refused:\n%s", res.Output)
	}
	if got := readSpec(t, proj); got != reformatted {
		t.Errorf("the whitespace change did not land:\n%q", got)
	}
}

type gateTrigger struct {
	event string
	match *guardrail.Matcher
}

// gateTriggers compiles every trigger of the shipped pinned-spec-holds gate
// against the file event kind it fires on.
func gateTriggers(t *testing.T) []gateTrigger {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "examples", "business-invariants", ".sloprail", "gate", "pinned-spec-holds", "gate.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var g struct {
		On []struct {
			Event string `yaml:"event"`
			Match string `yaml:"match"`
		} `yaml:"on"`
	}
	if err := yaml.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	kinds := map[string]module.KindDecl{}
	for _, k := range (&filemod.Module{}).Kinds() {
		kinds[k.Name] = k
	}
	var out []gateTrigger
	for _, on := range g.On {
		kind, ok := kinds[on.Event]
		if !ok {
			t.Fatalf("the gate triggers on %s, which is not a file event kind", on.Event)
		}
		m, err := guardrail.CompileGateMatch(on.Match, kind)
		if err != nil {
			t.Fatalf("compile the gate's %s match: %v", on.Event, err)
		}
		out = append(out, gateTrigger{on.Event, m})
	}
	if len(out) != 3 {
		t.Fatalf("the gate has %d triggers, want PreFileCreate, PreFileUpdate and PreFileDelete", len(out))
	}
	return out
}

// T046_61: the gate's refuse-unknown-result.sh refuses a create or an update whose
// result the engine could not work out (resultKnown false: a `sed -i`, `>`, `cp`)
// unless the predicate decided nothing pinned is at stake, and never a write whose
// result is known nor a delete (which has no result to work out).
func TestT046_61_GateRefusesAnUnknownResultOnlyWherePinned(t *testing.T) {
	repo, _ := unreadDeleteRepo(t)
	dir := gateDir(t, "pinned-spec-holds")
	write := func(kind, path, known string) string {
		return `{"event":{"kind":"` + kind + `","path":"` + path + `","resultKnown":` + known +
			`,"oldContent":"","newContent":"","oldMarkers":[],"newMarkers":[]}}`
	}
	for _, kind := range []string{"PreFileCreate", "PreFileUpdate"} {
		out, code := runRuleScript(t, dir, "refuse-unknown-result.sh", repo, write(kind, "SPEC.md", "false"))
		if code != 1 || !strings.Contains(out, "cannot be worked out before it runs") {
			t.Errorf("%s of a pinned spec with an unknown result was not refused (exit %d): %s", kind, code, out)
		}
		if out, code := runRuleScript(t, dir, "refuse-unknown-result.sh", repo, write(kind, "SPEC.md", "true")); code != 0 {
			t.Errorf("%s with a known result was refused (exit %d): %s", kind, code, out)
		}
		if out, code := runRuleScript(t, dir, "refuse-unknown-result.sh", repo, write(kind, "notes.md", "false")); code != 0 {
			t.Errorf("%s of a file nothing pins was refused for an unknown result (exit %d): %s", kind, code, out)
		}
	}
	if out, code := runRuleScript(t, dir, "refuse-unknown-result.sh", repo, unreadDelete("SPEC.md")); code != 0 {
		t.Errorf("a delete was refused for an unknown result (exit %d): %s", code, out)
	}
}
