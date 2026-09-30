package e2e

// A delete whose content the engine did not read (#84: `oldContentKnown: false`,
// e.g. `rm -r` past the engine's byte budget) carries an empty oldContent and no
// oldMarkers. Taken at face value, an empty oldContent makes a pinned spec's lines
// look unchanged by the delete, and no markers make a marked file look unmarked —
// both fail open. The predicate reads an empty oldContent from HEAD instead (the
// field itself is not in main's registry yet, so it is not read); the payloads
// here carry it as #84's engine will.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// unreadDeleteRepo commits SPEC.md and src/charge.go pinned to SPEC.md L3 at
// shaV1, then rewords rule 2 so that pin is stale at HEAD.
func unreadDeleteRepo(t *testing.T) (repo, shaV1 string) {
	t.Helper()
	repo = t.TempDir()
	git := func(args ...string) string {
		out, err := exec.Command("git", append([]string{"-C", repo, "-c", "user.email=t@t", "-c", "user.name=t", "-c", "commit.gpgsign=false"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	writeExec(t, repo, "SPEC.md", billingSpec)
	git("add", "-A")
	git("commit", "-qm", "spec")
	shaV1 = git("rev-parse", "HEAD")
	writeExec(t, repo, "charge.go", invariantCode(repo+"@"+shaV1+":SPEC.md#L3-3", refundBody))
	git("add", "-A")
	git("commit", "-qm", "pinned charge")
	return repo, shaV1
}

func unreadDelete(path string) string {
	return `{"event":{"kind":"PreFileDelete","path":"` + path + `","oldContent":"","oldMarkers":[],"oldContentKnown":false}}`
}

// T046_40: pinned-spec-holds' predicate applies the citation to an unread delete
// of a pinned spec, and of a file HEAD shows carrying a pin; it still waives an
// unread delete of a file nothing pins and that carries no pin.
//
// The marked-file case is the predicate's answer, not something the engine asks
// before the write: the gate's PreFileDelete match (a spec path, or
// any(event.oldMarkers, …)) cannot select a marked file whose unread delete
// carries no oldMarkers, so that delete is caught only by the file-guard at
// Stop, when the baseline's markers arrive. #84 filling oldMarkers from HEAD for
// an unread delete would let the match select it before the write.
func TestT046_40_UnreadDeleteOfAPinnedFileApplies(t *testing.T) {
	repo, _ := unreadDeleteRepo(t)
	writeExec(t, repo, "notes.md", "nothing pinned here\n")
	dir := gateDir(t, "pinned-spec-holds")

	for _, path := range []string{"SPEC.md", "charge.go"} {
		if out, code := runRuleScript(t, dir, "changes-pinned-lines.sh", repo, unreadDelete(path)); code != 0 {
			t.Errorf("an unread delete of %s was waived (exit %d): %s", path, code, out)
		}
	}
	// The hint says what happened: the spec was emptied (or not read) before the
	// delete — not that the delete "rewrites" lines it never saw.
	if out, _ := runRuleScript(t, dir, "changes-pinned-lines.sh", repo, unreadDelete("SPEC.md")); !strings.Contains(out, "which was emptied before the delete") {
		t.Errorf("the hint for an emptied-then-deleted spec does not say so: %s", out)
	}
	if out, code := runRuleScript(t, dir, "changes-pinned-lines.sh", repo, unreadDelete("notes.md")); code != 1 {
		t.Errorf("an unread delete of a file nothing pins was not waived (exit %d): %s", code, out)
	}
	// Absent means known: a read delete of the whole spec is still a change to it.
	body, _ := json.Marshal(billingSpec)
	known := `{"event":{"kind":"PreFileDelete","path":"SPEC.md","oldContent":` + string(body) + `,"oldMarkers":[]}}`
	if out, code := runRuleScript(t, dir, "changes-pinned-lines.sh", repo, known); code != 0 || !strings.Contains(out, "rewrites SPEC.md L3-3") {
		t.Errorf("a read delete of the pinned spec was not applied as a pinned-line change (exit %d): %s", code, out)
	}
}

// T046_41: pinned-invariant is a plain file-guard (a rule's prevention is a gate,
// and it has none), so no PreFileDelete — the only kind that can arrive unread —
// ever reaches it: it judges a delete at Stop, as a PostFileDelete whose
// oldContent and oldMarkers are the session baseline's, read from git. That is
// what keeps it from passing an unread delete on an empty marker list; if it were
// ever given a gate, this test says why it must not be without reading an unread
// delete's pins from HEAD.
func TestT046_41_PinnedInvariantNeverSeesAPreDelete(t *testing.T) {
	root := filepath.Join(repoRoot(t), "examples", "business-invariants", ".sloprail")
	if _, err := os.Stat(filepath.Join(root, "gate", "pinned-invariant")); err == nil {
		t.Fatal("pinned-invariant has a gate, so an unread PreFileDelete (no oldMarkers) would reach it and pass")
	}
	yaml := readFile(t, filepath.Join(root, "file-guard", "pinned-invariant"), "file-guard.yaml")
	for _, line := range strings.Split(yaml, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "preventive:") {
			t.Fatalf("pinned-invariant declares preventive, which the engine refuses at load: %s", line)
		}
	}
}

// unreadWrite is a Post create or update whose settled file the engine could not
// read (newContentKnown false: a link to a FIFO, a device or a directory, or past
// the read cap), with the baseline's markers as the engine gives them.
func unreadWrite(kind, path, oldMarkers string) string {
	return `{"event":{"kind":"` + kind + `","path":"` + path + `","oldContent":"","newContent":"",` +
		`"oldMarkers":` + oldMarkers + `,"newMarkers":[],"newContentKnown":false}}`
}

// T046_60: an unread Post create or update is not a pass, but it is not a
// citation for every file either. A file nothing pins that carries no marker is
// waived — nothing pinned is at stake — while a pinned spec, or a file that
// carried a pin, applies, with a hint about the file as it was left rather than
// about how a Pre edit was made. Before, an early exit applied the citation to
// every unread write, pinned or not (data/big.bin in a project with no pin on it).
func TestT046_60_UnreadWriteAppliesOnlyWherePinned(t *testing.T) {
	repo, shaV1 := unreadDeleteRepo(t)
	dir := ruleDir(t, "pinned-spec-holds")

	for _, kind := range []string{"PostFileCreate", "PostFileUpdate"} {
		if out, code := runRuleScript(t, dir, "changes-pinned-lines.sh", repo, unreadWrite(kind, "data/big.bin", "[]")); code != 1 || !strings.Contains(out, `"waived"`) {
			t.Errorf("%s of an unread file nothing pins was not waived (exit %d): %s", kind, code, out)
		}
		out, code := runRuleScript(t, dir, "changes-pinned-lines.sh", repo, unreadWrite(kind, "SPEC.md", "[]"))
		if code != 0 {
			t.Errorf("%s of an unread pinned spec was waived (exit %d): %s", kind, code, out)
		}
		if !strings.Contains(out, "what it left could not be read") {
			t.Errorf("%s of an unread pinned spec: the hint does not say the file could not be read: %s", kind, out)
		}
		marker := `[{"kind":"invariant","fqn":"` + repo + "@" + shaV1 + `:SPEC.md#L3-3"}]`
		if out, code := runRuleScript(t, dir, "changes-pinned-lines.sh", repo, unreadWrite(kind, "charge.go", marker)); code != 0 {
			t.Errorf("%s of an unread file that carried a pin was waived (exit %d): %s", kind, code, out)
		}
	}
}
