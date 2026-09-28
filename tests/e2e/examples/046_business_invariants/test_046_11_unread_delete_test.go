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
func TestT046_40_UnreadDeleteOfAPinnedFileApplies(t *testing.T) {
	repo, _ := unreadDeleteRepo(t)
	writeExec(t, repo, "notes.md", "nothing pinned here\n")
	dir := ruleDir(t, "pinned-spec-holds")

	for _, path := range []string{"SPEC.md", "charge.go"} {
		if out, code := runRuleScript(t, dir, "changes-pinned-lines.sh", repo, unreadDelete(path)); code != 0 {
			t.Errorf("an unread delete of %s was waived (exit %d): %s", path, code, out)
		}
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

// T046_41: pinned-invariant is not preventive, so no PreFileDelete — the only
// kind that can arrive unread — ever reaches it: it judges a delete at Stop, as a
// PostFileDelete whose oldContent and oldMarkers are the session baseline's, read
// from git. That is what keeps it from passing an unread delete on an empty
// marker list; if it were ever made preventive, this test says why it must not be
// without reading an unread delete's pins from HEAD.
func TestT046_41_PinnedInvariantNeverSeesAPreDelete(t *testing.T) {
	yaml := readFile(t, filepath.Join(repoRoot(t), "examples", "business-invariants", ".sloprail", "file-guard", "pinned-invariant"), "file-guard.yaml")
	for _, line := range strings.Split(yaml, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "preventive:") && !strings.Contains(line, "false") {
			t.Fatalf("pinned-invariant is preventive, so an unread PreFileDelete (no oldMarkers) would reach it and pass: %s", line)
		}
	}
}
