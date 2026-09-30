package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The history a citation is judged against at Stop, probed on ordinary
// workflows: a citation grounds only the change it rode on, in the pool it was
// cited in — but a cited write restating the file settles what came before
// it, and a change the agent never made is never charged to it. Every rule
// here is the file-guard afterCitationGuard (`memories/**`, a user
// citation, judged at Stop).

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func stopBlocks(e *harness.Env, proj, sess string) string {
	return strings.Join(append(e.BlockingErrorsFrom(proj, sess, "Stop"), e.BlockingErrorsFrom(proj, sess, "SubagentStop")...), "\n")
}

// T041_37 (P1): an uncited rewrite after a cited write is refused — and the
// remedy the refusal prints, a cited sr-file write restating the whole file
// (here followed by a cited edit), settles it. Without that the agent loops:
// every cited edit grounds only its own part and the uncited one stands.
func TestT041_37_TheRemedySettlesAnUncitedChange(t *testing.T) {
	e, proj := guarded(t, afterCitationGuard)
	e.SetStopBlockCap(1)
	e.Run(proj, "s-041-37", prompt, Turns("done",
		Bash("b1", `sr-file write memories/a.md --cite:user 'adopt a decision log' --content 'v1'`),
		Write("w1", "memories/a.md", "v2 nobody asked for"),
	))
	blocks := stopBlocks(e, proj, "s-041-37")
	if !strings.Contains(blocks, "sr-file write memories/a.md --cite:user") {
		t.Fatalf("the uncited rewrite was not refused with the restating remedy:\n%s", blocks)
	}

	e2, proj2 := guarded(t, afterCitationGuard)
	e2.Run(proj2, "s-041-37b", prompt, Turns("done",
		Bash("b1", `sr-file write memories/a.md --cite:user 'adopt a decision log' --content 'v1'`),
		Write("w1", "memories/a.md", "v2 nobody asked for"),
		Bash("b2", `sr-file write memories/a.md --cite:user 'adopt a decision log' --content 'v2 nobody asked for'`),
		Bash("b3", `sr-file edit memories/a.md --old-string 'nobody asked for' --new-string 'as asked' --cite:user 'adopt a decision log'`),
	))
	if got := readProj(t, proj2, "memories/a.md"); got != "v2 as asked" {
		t.Fatalf("the remedy did not land: %q", got)
	}
	if blocks := stopBlocks(e2, proj2, "s-041-37b"); blocks != "" {
		t.Errorf("the printed remedy was still refused:\n%s", blocks)
	}
}

// T041_38 (P2): a rewrite cited in ANOTHER pool does not ground a user-pool
// requirement: citing a tool's output (here what an echo printed) for an
// arbitrary rewrite of a file the user's words grounded is refused.
func TestT041_38_AChangeCitedInAnotherPoolIsUncited(t *testing.T) {
	e, proj := guarded(t, afterCitationGuard)
	e.Run(proj, "s-041-38", prompt, Turns("done",
		Bash("b0", `echo ECHOED-7781`),
		Bash("b1", `sr-file write memories/a.md --cite:user 'adopt a decision log' --content 'the log'`),
		Bash("b2", `sr-file write memories/a.md --cite:tool_result 'ECHOED-7781' --content 'EVIL REWRITE'`),
	))
	if got := readProj(t, proj, "memories/a.md"); got != "EVIL REWRITE" {
		t.Fatalf("the rewrite did not land, so this tests nothing: %q", got)
	}
	if blocks := stopBlocks(e, proj, "s-041-38"); !strings.Contains(blocks, "was changed without a citation") {
		t.Errorf("a rewrite cited in the tool_result pool passed a user-pool requirement:\n%s", blocks)
	}
}

// T041_39 (P3): a file already dirty when the session began — changed by
// nobody in this session — is not charged: a cited edit of it passes.
func TestT041_39_DirtyAtSessionStartIsNotCharged(t *testing.T) {
	e, proj := guarded(t, afterCitationGuard)
	e.WriteFile(proj, "memories/a.md", "base\n")
	commitAll(t, proj)
	e.WriteFile(proj, "memories/a.md", "base\nthe user's uncommitted line\n")

	e.Run(proj, "s-041-39", prompt, Turns("done",
		Bash("b1", `sr-file edit memories/a.md --old-string 'base' --new-string 'decided' --cite:user 'adopt a decision log'`),
	))
	if got := readProj(t, proj, "memories/a.md"); !strings.HasPrefix(got, "decided") {
		t.Fatalf("the cited edit did not land: %q", got)
	}
	if blocks := stopBlocks(e, proj, "s-041-39"); blocks != "" {
		t.Errorf("a change made before the session began was charged to the agent:\n%s", blocks)
	}
}

// T041_40 (P4): the user edits a file by hand between turns; the agent's next
// cited edit passes — the user's edit is not the agent's to cite — and so does
// a turn that makes no change at all.
func TestT041_40_TheUsersEditBetweenTurnsIsNotCharged(t *testing.T) {
	e, proj := guarded(t, afterCitationGuard)
	e.Run(proj, "s-041-40", prompt, Turns("done",
		Bash("b1", `sr-file write memories/a.md --cite:user 'adopt a decision log' --content 'v1'`),
	))
	e.WriteFile(proj, "memories/a.md", "v1 and the user's own line")
	e.Run(proj, "s-041-40", "carry on", Turns("done", Bash("b2", "true")))
	e.WriteFile(proj, "memories/a.md", "v1 and the user's own line, edited again")
	e.Run(proj, "s-041-40", "carry on", Turns("done",
		Bash("b3", `sr-file edit memories/a.md --old-string 'v1' --new-string 'v2' --cite:user 'adopt a decision log'`),
	))
	if blocks := stopBlocks(e, proj, "s-041-40"); blocks != "" {
		t.Errorf("the user's own edit between turns was charged to the agent:\n%s", blocks)
	}
}

// T041_41 (P6): a checkout filter (`*.md text eol=crlf`) makes the working
// file differ from the committed blob in bytes, not in content. A cited edit
// of such a file passes: the baseline is read through the filters.
func TestT041_41_ACheckoutFilterIsNotACharge(t *testing.T) {
	e, proj := guarded(t, afterCitationGuard)
	e.WriteFile(proj, ".gitattributes", "*.md text eol=crlf\n")
	e.WriteFile(proj, "memories/a.md", "one\r\ntwo\r\n")
	commitAll(t, proj)

	e.Run(proj, "s-041-41", prompt, Turns("done",
		Bash("b1", `sr-file edit memories/a.md --old-string 'two' --new-string 'TWO' --cite:user 'adopt a decision log'`),
	))
	if got := readProj(t, proj, "memories/a.md"); got != "one\r\nTWO\r\n" {
		t.Fatalf("the cited edit did not land: %q", got)
	}
	if blocks := stopBlocks(e, proj, "s-041-41"); blocks != "" {
		t.Errorf("an eol filter's rewrite was charged to the agent:\n%s", blocks)
	}
}

// T041_42 (P7): the user switches branches between turns; the agent's next
// cited edit passes. The file differs from the agent's last cited state
// because of the switch, not the agent.
func TestT041_42_TheUsersBranchSwitchIsNotCharged(t *testing.T) {
	e, proj := guarded(t, afterCitationGuard)
	e.WriteFile(proj, "memories/a.md", "base\n")
	commitAll(t, proj)
	git(t, proj, "branch", "other")
	git(t, proj, "checkout", "-q", "other")
	e.WriteFile(proj, "memories/a.md", "the other branch's version\n")
	git(t, proj, "commit", "-qam", "other")
	git(t, proj, "checkout", "-q", "-")
	e.WriteFile(proj, "notes.txt", "main moves on\n")
	git(t, proj, "add", "-A")
	git(t, proj, "commit", "-qm", "main moves on")

	e.Run(proj, "s-041-42", prompt, Turns("done",
		Bash("b1", `sr-file edit memories/a.md --old-string 'base' --new-string 'base, decided' --cite:user 'adopt a decision log'`),
	))
	git(t, proj, "stash", "-q")
	git(t, proj, "checkout", "-q", "other")
	e.Run(proj, "s-041-42", "carry on", Turns("done",
		Bash("b2", `sr-file edit memories/a.md --old-string 'version' --new-string 'version, decided' --cite:user 'adopt a decision log'`),
	))
	if got := readProj(t, proj, "memories/a.md"); got != "the other branch's version, decided\n" {
		t.Fatalf("the cited edit on the other branch did not land: %q", got)
	}
	if blocks := stopBlocks(e, proj, "s-041-42"); blocks != "" {
		t.Errorf("the user's branch switch was charged to the agent:\n%s", blocks)
	}
}

// T041_43 (P8): the root creates a file with a citation, then dispatches a
// sub-agent that edits it with one. Neither the sub-agent's cycle end nor the
// root's refuses: the sub-agent reads the root's cited changes beside its own.
func TestT041_43_ASubagentEditsWhatItsRootCreated(t *testing.T) {
	e, proj := guarded(t, afterCitationGuard)
	sub := subagentScript(t, harness.Turns("sub done",
		Bash("sb1", `sr-file edit memories/a.md --old-string 'v1' --new-string 'v2' --cite:user 'adopt a decision log'`),
	))
	e.Run(proj, "s-041-43", prompt, Turns("done",
		Bash("b1", `sr-file write memories/a.md --cite:user 'adopt a decision log' --content 'v1'`),
		harness.Dispatch("d1", "adopt a decision log: edit memories/a.md", sub, ""),
	))
	if got := readProj(t, proj, "memories/a.md"); got != "v2" {
		t.Fatalf("the sub-agent's cited edit did not land: %q", got)
	}
	if blocks := stopBlocks(e, proj, "s-041-43"); blocks != "" {
		t.Errorf("a sub-agent's cited edit of its root's cited file was refused:\n%s", blocks)
	}
}

// T041_44: the dry run computes a cited write, but the real run fails (the
// directory is not writable). Its citation grounds nothing: an uncited write of
// the file afterwards is refused at Stop. (T041_33's failing edit never gets
// that far — its dry run already fails.)
func TestT041_44_ACitedWriteThatFailsForRealGroundsNothing(t *testing.T) {
	e, proj := guarded(t, afterCitationGuard)
	if err := os.MkdirAll(filepath.Join(proj, "memories"), 0o755); err != nil {
		t.Fatal(err)
	}
	e.WriteFile(proj, "memories/.keep", "")
	commitAll(t, proj)
	if err := os.Chmod(filepath.Join(proj, "memories"), 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(proj, "memories"), 0o755) })

	e.Run(proj, "s-041-44", prompt, Turns("done",
		Bash("b1", `sr-file write memories/a.md --cite:user 'adopt a decision log' --content 'cited'`),
		Bash("b2", `chmod 755 memories`),
		Write("w1", "memories/a.md", "uncited"),
	))
	if got := readProj(t, proj, "memories/a.md"); got != "uncited" {
		t.Fatalf("the uncited write did not land, so this tests nothing: %q", got)
	}
	if blocks := stopBlocks(e, proj, "s-041-44"); blocks == "" {
		t.Errorf("an uncited write passed at Stop on the citation of a cited write that never landed")
	}
}

// T041_46 (P9): work the agent started in the background lands after its
// Stop. The next turn's first hook must not set that change aside as the
// user's: once the agent has started work that can outlive the call, what
// changes between its turns is charged to it, and the uncited rewrite is
// refused.
func TestT041_46_BackgroundWorkLandingAfterStopIsCharged(t *testing.T) {
	e, proj := guarded(t, afterCitationGuard)
	e.Run(proj, "s-041-46", prompt, Turns("done",
		Bash("b1", `sr-file write memories/a.md --cite:user 'adopt a decision log' --content 'the log'`),
		Bash("b2", `nohup sh -c 'sleep 3; printf "EVIL UNCITED REWRITE" > memories/a.md' >/dev/null 2>&1 &`),
	))
	deadline := time.Now().Add(20 * time.Second)
	for readProj(t, proj, "memories/a.md") != "EVIL UNCITED REWRITE" {
		if time.Now().After(deadline) {
			t.Fatalf("the background rewrite never landed, so this tests nothing")
		}
		time.Sleep(200 * time.Millisecond)
	}
	e.Run(proj, "s-041-46", "carry on", Turns("done", Bash("b3", "true")))
	if blocks := stopBlocks(e, proj, "s-041-46"); !strings.Contains(blocks, "changed after your last Stop") || !strings.Contains(blocks, "nohup") {
		t.Errorf("an uncited rewrite by the agent's own background job passed as the user's edit:\n%s", blocks)
	}
}

// T041_47 (P15): a cited sr-file named by the path of the engine's own sr-file
// is the same program, so it is dry-run like the bare name: the change lands
// and passes at Stop (file-guard) and is admitted (gate). A cited
// call the dry run cannot compute (here behind an `export`) lands with its
// citations untied to what landed — and the Stop refusal says why, and how to
// run sr-file so they count.
func TestT041_47_TheEnginesOwnSRFileByPath(t *testing.T) {
	e, proj := guarded(t, afterCitationGuard)
	byPath := filepath.Join(e.BinDir(), "sr-file")
	e.Run(proj, "s-041-47", prompt, Turns("done",
		Bash("b1", byPath+` write memories/a.md --cite:user 'adopt a decision log' --content 'by path'`),
	))
	if got := readProj(t, proj, "memories/a.md"); got != "by path" {
		t.Fatalf("the write by path did not land: %q", got)
	}
	if blocks := stopBlocks(e, proj, "s-041-47"); blocks != "" {
		t.Errorf("a cited write by the engine's own sr-file path was refused at Stop:\n%s", blocks)
	}

	e2, proj2 := guardedPre(t)
	res := e2.Run(proj2, "s-041-47b", prompt, Turns("done",
		Bash("b1", filepath.Join(e2.BinDir(), "sr-file")+` write memories/a.md --cite:user 'adopt a decision log' --content 'by path'`),
	))
	if !e2.Exists(proj2, "memories/a.md") {
		t.Errorf("the gate refused a cited write by the engine's own sr-file path:\n%s", res.Output)
	}

	e3, proj3 := guarded(t, afterCitationGuard)
	e3.Run(proj3, "s-041-47c", prompt, Turns("done",
		Bash("b1", `export X=1 && sr-file write memories/a.md --cite:user 'adopt a decision log' --content 'behind export'`),
	))
	blocks := stopBlocks(e3, proj3, "s-041-47c")
	if !strings.Contains(blocks, "could not be computed before it ran") || !strings.Contains(blocks, "command -v sr-file") {
		t.Errorf("the Stop refusal does not say why the citation did not count, or how to run sr-file:\n%s", blocks)
	}
}

// waitFor polls until the file holds want, or fails the test.
func waitFor(t *testing.T, proj, rel, want string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for readProj(t, proj, rel) != want {
		if time.Now().After(deadline) {
			t.Fatalf("%s never came to hold %q, so this tests nothing", rel, want)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// T041_49 (P18, P19, P20): honest work mentioning `&` does not mark the session
// as running work between turns — a cited write whose content holds "R&D", an
// echo of a URL with `&` in its query, a background job the line waits for. The
// user's own edit between turns is then set aside as theirs, as on main.
func TestT041_49_AmpersandsThatDetachNothing(t *testing.T) {
	for name, cmd := range map[string]string{
		"P18 R&D content": `sr-file write memories/b.md --cite:user 'adopt a decision log' --content '# R&D decision log'`,
		"P19 URL":         `echo 'https://x.invalid/?a=1&b=2'`,
		"P20 waited job":  `sleep 0 & wait`,
	} {
		t.Run(name, func(t *testing.T) {
			e, proj := guarded(t, afterCitationGuard)
			e.Run(proj, "s-041-49", prompt, Turns("done",
				Bash("b1", `sr-file write memories/a.md --cite:user 'adopt a decision log' --content 'v1'`),
				Bash("b2", cmd),
			))
			e.WriteFile(proj, "memories/a.md", "v1 and the user's own line")
			e.Run(proj, "s-041-49", "carry on", Turns("done", Bash("b3", "true")))
			if blocks := stopBlocks(e, proj, "s-041-49"); blocks != "" {
				t.Errorf("the user's edit was charged after a command that detached nothing:\n%s", blocks)
			}
		})
	}
}

// T041_50 (P17): a bare `&` nothing waits for may still be running between
// turns, so the change that lands then is charged — and the refusal says the
// file changed after the agent's last Stop, and names the work.
func TestT041_50_ABareBackgroundJobChargesAndExplains(t *testing.T) {
	e, proj := guarded(t, afterCitationGuard)
	e.Run(proj, "s-041-50", prompt, Turns("done",
		Bash("b1", `sr-file write memories/a.md --cite:user 'adopt a decision log' --content 'v1'`),
		Bash("b2", `sleep 1 &`),
	))
	e.WriteFile(proj, "memories/a.md", "v1 and a later line")
	e.Run(proj, "s-041-50", "carry on", Turns("done", Bash("b3", "true")))
	blocks := stopBlocks(e, proj, "s-041-50")
	for _, want := range []string{"memories/a.md changed after your last Stop", "(sleep 1 &)", "sr-file write memories/a.md --cite:user"} {
		if !strings.Contains(blocks, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, blocks)
		}
	}
}

// T041_51 (P21): a foreground sub-agent detaches a job that rewrites a file the
// root cited, after the root's Stop. The sub-agent's mark lives in its own
// store; the root's next cycle reads it, and charges the rewrite.
func TestT041_51_ASubagentsDetachedJobIsCharged(t *testing.T) {
	e, proj := guarded(t, afterCitationGuard)
	sub := subagentScript(t, harness.Turns("sub done",
		Bash("sb1", `nohup sh -c 'sleep 3; printf "EVIL SUB REWRITE" > memories/a.md' >/dev/null 2>&1 &`),
	))
	e.Run(proj, "s-041-51", prompt, Turns("done",
		Bash("b1", `sr-file write memories/a.md --cite:user 'adopt a decision log' --content 'the log'`),
		harness.Dispatch("d1", "tidy up", sub, ""),
	))
	waitFor(t, proj, "memories/a.md", "EVIL SUB REWRITE")
	e.Run(proj, "s-041-51", "carry on", Turns("done", Bash("b3", "true")))
	if blocks := stopBlocks(e, proj, "s-041-51"); !strings.Contains(blocks, "changed after your last Stop") {
		t.Errorf("a sub-agent's detached rewrite passed as the user's edit:\n%s", blocks)
	}
}
