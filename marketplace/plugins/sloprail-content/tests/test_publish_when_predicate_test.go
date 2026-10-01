package e2e

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// The gate's `when` predicate for the publish approval: exit 0 applies the
// citation requirement, exit 1 waives it. A publish transition must apply it
// even when the event carries no path — the shared library once read $path
// only the file-guard entry set, so under `set -u` the gate's predicate died
// with exit 1 and the approval was silently waived.
func TestPublish_WhenPredicateAppliesOnAPublishEvenWithoutAPath(t *testing.T) {
	e := harness.New(t)
	pred := filepath.Join(pluginRoot(t), ".sloprail", "gate", "unit-publish-approved", "enters-published.sh")
	draft := unitFrontmatter("status: drafting\n", "body")
	published := unitFrontmatter("status: published\npublished_urls: [\"https://example.com/x\"]\n", "body")

	exitOf := func(t *testing.T, payload string) int {
		t.Helper()
		cmd := exec.Command("bash", pred)
		cmd.Env = append(os.Environ(), "PATH="+e.BinDir()+string(os.PathListSeparator)+os.Getenv("PATH"))
		cmd.Stdin = strings.NewReader(payload)
		out, err := cmd.CombinedOutput()
		var ee *exec.ExitError
		switch {
		case err == nil:
			return 0
		case errors.As(err, &ee):
			return ee.ExitCode()
		default:
			t.Fatalf("the predicate could not run: %v\n%s", err, out)
			return -1
		}
	}
	event := func(path, oldContent, newContent string) string {
		fields := `"kind":"PreFileUpdate","resultKnown":true,` +
			`"oldContent":` + jsonString(oldContent) + `,"newContent":` + jsonString(newContent)
		if path != "" {
			fields += `,"path":` + jsonString(path)
		}
		return `{"event":{` + fields + `}}`
	}
	unit := "memories/topics/t/units/u/UNIT.md"

	if got := exitOf(t, event(unit, draft, draft)); got != 1 {
		t.Fatalf("a draft edit must be decided as not a publish (exit 1), got %d; the predicate cannot decide here, so the cases below prove nothing", got)
	}
	if got := exitOf(t, event(unit, draft, published)); got != 0 {
		t.Errorf("a drafting→published edit must apply the citation requirement (exit 0), got %d", got)
	}
	if got := exitOf(t, event("", draft, published)); got != 0 {
		t.Errorf("a drafting→published edit with no path on the event must still apply the requirement (exit 0), got %d", got)
	}
}

func jsonString(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`)
	return `"` + r.Replace(s) + `"`
}

// The file-guard entry is a `when` predicate too: a Changeset file that lacks the
// content its status carries, or a subject matching no changed file, decided nothing, so
// it APPLIES the approval (exit 0) instead of waiving it as "not entering publish". The
// control, a decided draft edit, still waives.
func TestPublish_FileGuardWhenAppliesOnAMissingFieldOrSubject(t *testing.T) {
	e := harness.New(t)
	pred := filepath.Join(pluginRoot(t), ".sloprail", "file-guard", "unit-publish-approved", "enters-published.sh")
	draft := unitFrontmatter("status: drafting\n", "body")
	published := unitFrontmatter("status: published\npublished_urls: [\"https://example.com/x\"]\n", "body")
	unit := "memories/topics/t/units/u/UNIT.md"

	exitOf := func(t *testing.T, subject, files string) int {
		t.Helper()
		payload := `{"event":{"kind":"Changeset"},"subject":{"id":"` + subject + `","files":["` + subject + `"]},"changeset":{"files":[` + files + `]}}`
		cmd := exec.Command("bash", pred)
		cmd.Env = append(os.Environ(), "PATH="+e.BinDir()+string(os.PathListSeparator)+os.Getenv("PATH"))
		cmd.Stdin = strings.NewReader(payload)
		out, err := cmd.CombinedOutput()
		var ee *exec.ExitError
		switch {
		case err == nil:
			return 0
		case errors.As(err, &ee):
			return ee.ExitCode()
		default:
			t.Fatalf("the predicate could not run: %v\n%s", err, out)
			return -1
		}
	}
	file := func(status string, fields ...string) string {
		return `{"status":"` + status + `","path":` + jsonString(unit) + `,` + strings.Join(fields, ",") + `}`
	}
	oldc, newDraft, newPub := `"oldContent":`+jsonString(draft), `"newContent":`+jsonString(draft), `"newContent":`+jsonString(published)

	if got := exitOf(t, unit, file("M", oldc, newDraft)); got != 1 {
		t.Fatalf("control: a draft edit exited %d, want 1 (waived); the cases below prove nothing", got)
	}
	if got := exitOf(t, unit, file("M", oldc, newPub)); got != 0 {
		t.Errorf("control: drafting to published exited %d, want 0", got)
	}
	for name, files := range map[string]string{
		"M with no newContent":  file("M", oldc),
		"M with no oldContent":  file("M", newDraft),
		"A with no newContent":  file("A"),
		"M with a null content": file("M", oldc, `"newContent":null`),
	} {
		if got := exitOf(t, unit, files); got != 0 {
			t.Errorf("%s exited %d, want 0 (applies)", name, got)
		}
	}
	if got := exitOf(t, "memories/topics/t/units/other/UNIT.md", file("M", oldc, newDraft)); got != 0 {
		t.Errorf("a subject matching no changed file exited %d, want 0 (applies)", got)
	}
}
