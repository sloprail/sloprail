package ghresults

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/internal/checkrun"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func golden(t *testing.T, name, got string) {
	t.Helper()
	p := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(p, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != got {
		t.Errorf("%s differs (run with -update to accept):\n--- want\n%s\n--- got\n%s", name, want, got)
	}
}

func mixed() []checkrun.CheckOutcome {
	return []checkrun.CheckOutcome{
		{Rule: "sloprail/file-guard/docs-fresh", Subject: "docs/a.md", Kind: "guard", Status: "pass", Source: "cached"},
		{Rule: "sloprail/file-guard/docs-fresh", Subject: "docs/b,c.md", Kind: "guard", Status: "fail", Source: "stored", Reason: "b.md is stale: 100% of it\nreads like the old design"},
		{Rule: "sloprail/file-guard/no-slop", Subject: "changeset", Kind: "guard", Status: "missing", Source: "stored", Reason: "no stored verdict; run sr-checks run", Files: []string{"src/x.go", "src/z.go"}},
		{Rule: "sloprail/file-guard/no-slop", Subject: "src/x.go", Kind: "check[0]:script", Status: "pass", Source: "ran"},
		{Rule: "sloprail/file-guard/no-slop", Subject: "src/y.go", Kind: "check[1]:judge", Status: "skipped", Source: "ran", Reason: "judge deferred"},
	}
}

const fix = "To fix: run `sr-checks run --base origin/main --head abc123` locally, then re-run this job."

func TestAnnotationsGolden(t *testing.T) {
	golden(t, "annotations.golden", Annotations(mixed(), []string{"file-guard broken: could not be read"}))
}

func TestSummaryGolden(t *testing.T) {
	golden(t, "summary.golden", Summary(mixed(), []string{"file-guard broken: could not be read"}, fix))
}

func TestSummaryAllPassHasNoFix(t *testing.T) {
	out := Summary(mixed()[:1], nil, fix)
	if strings.Contains(out, "To fix") || !strings.Contains(out, "All 1 checks pass") {
		t.Fatalf("a green summary must not tell the reader to fix anything:\n%s", out)
	}
}

func TestJUnitGolden(t *testing.T) {
	b, err := JUnit(mixed())
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "junit.golden", string(b))
}

func TestAnnotationsAreCapped(t *testing.T) {
	var in []checkrun.CheckOutcome
	for i := 0; i < MaxAnnotations+7; i++ {
		in = append(in, checkrun.CheckOutcome{Rule: "r", Subject: fmt.Sprintf("f%03d.go", i), Status: "fail", Source: "stored", Reason: "no"})
	}
	lines := strings.Split(strings.TrimSpace(Annotations(in, nil)), "\n")
	if len(lines) != MaxAnnotations+1 || !strings.Contains(lines[len(lines)-1], "7 more refusals") {
		t.Fatalf("want %d annotations and a summary line, got %d lines, last %q", MaxAnnotations, len(lines), lines[len(lines)-1])
	}
}

func TestNoAnnotationsWhenGreen(t *testing.T) {
	if got := Annotations(mixed()[:1], nil); got != "" {
		t.Fatalf("got %q", got)
	}
}
