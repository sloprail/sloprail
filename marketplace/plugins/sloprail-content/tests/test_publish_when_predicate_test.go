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
		cmd.Env = append(harness.HostEnv(), "PATH="+e.BinDir()+string(os.PathListSeparator)+os.Getenv("PATH"))
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
