package srtest

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func project(t *testing.T, cases map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for n, body := range cases {
		d := filepath.Join(root, ".sloprail", "tests", n)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "test.sh"), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func byName(rs []Result) map[string]Result {
	m := map[string]Result{}
	for _, r := range rs {
		m[r.Subject] = r
	}
	return m
}

func TestStatusMapping(t *testing.T) {
	root := project(t, map[string]string{
		"a-pass":  "exit 0",
		"b-fail":  "echo boom; exit 1",
		"c-error": "echo bad >&2; exit 2",
		"d-other": "exit 7",
	})
	rs, err := Run(root, Options{Rules: func(string, io.Writer) []string { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	m := byName(rs)
	want := map[string]string{"a-pass": Pass, "b-fail": Fail, "c-error": Error, "d-other": Fail}
	for k, v := range want {
		if m[k].Status != v {
			t.Errorf("%s: %s want %s", k, m[k].Status, v)
		}
	}
	if m["a-pass"].Output != "" || !strings.Contains(m["b-fail"].Output, "boom") || !strings.Contains(m["c-error"].Output, "bad") {
		t.Errorf("output: %+v", rs)
	}
	if rs[0].CheckID != "sr-test" || rs[0].Kind != "test" || rs[0].CheckedAt == "" {
		t.Errorf("shape: %+v", rs[0])
	}
}

func TestEnvAndEvents(t *testing.T) {
	root := project(t, map[string]string{"e": `
test -f "$SR_TEST_CASE_DIR/test.sh" || exit 1
test "$SR_CHECKS_JUDGE_MOCKS" = "{}" || exit 1
test -f .sloprail/tests/e/test.sh || exit 1
echo '{"kind":"GateChecked","rule":"g","outcome":"permitted"}' >> "$SR_EVENTS_FILE"
`})
	rs, _ := Run(root, Options{})
	if rs[0].Status != Pass || len(rs[0].Metadata.Events) != 1 {
		t.Fatalf("%+v", rs[0])
	}
}

func TestTimeout(t *testing.T) {
	root := project(t, map[string]string{"slow": "sleep 30"})
	start := time.Now()
	rs, _ := Run(root, Options{Timeout: 300 * time.Millisecond})
	if rs[0].Status != Error || !strings.Contains(rs[0].Output, "timed out") || time.Since(start) > 10*time.Second {
		t.Fatalf("%+v", rs[0])
	}
}

func TestParallel(t *testing.T) {
	root := project(t, map[string]string{"a": "sleep 1", "b": "sleep 1", "c": "sleep 1", "d": "sleep 1"})
	start := time.Now()
	rs, _ := Run(root, Options{Jobs: 4})
	if d := time.Since(start); d > 3500*time.Millisecond {
		t.Errorf("not parallel: %v", d)
	}
	for _, r := range rs {
		if r.Status != Pass {
			t.Errorf("%+v", r)
		}
	}
}

func TestDoctor(t *testing.T) {
	root := project(t, map[string]string{"x": `echo '{"kind":"GateChecked","rule":"p/g1","outcome":"refused"}' >> "$SR_EVENTS_FILE"
echo '{"kind":"ContextActivated","rule":"c1"}' >> "$SR_EVENTS_FILE"`})
	rs, _ := Run(root, Options{Rules: func(string, io.Writer) []string {
		return []string{"gate:p/g1", "gate:g2", "context:c1", "file-guard:f"}
	}})
	got := strings.Join(Uncovered(rs), ",")
	if got != "file-guard:f,gate:g2" {
		t.Fatalf("got %s", got)
	}
}

func TestUncoveredStructureByRule(t *testing.T) {
	r := Result{Metadata: Metadata{
		Rules:  []string{"structure:sloprail/structure", "structure:structure"},
		Events: []json.RawMessage{json.RawMessage(`{"kind":"StructureChecked","rule":"sloprail/structure"}`)},
	}}
	assert.Equal(t, []string{"structure:structure"}, Uncovered([]Result{r}))
}
