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

var rng = Range{Base: "aaa111", Head: "bbb222", FirstLine: map[string]int{"docs/b,c.md": 12, "src/x.go": 1}}

func TestAnnotationsGolden(t *testing.T) {
	golden(t, "annotations.golden", Annotations(mixed(), []string{"file-guard broken: could not be read"}, rng))
}

func TestSummaryGolden(t *testing.T) {
	golden(t, "summary.golden", Summary(mixed(), []string{"file-guard broken: could not be read"}, rng))
}

func TestSummaryAllPassHasNoFix(t *testing.T) {
	out := Summary(mixed()[:1], nil, rng)
	if strings.Contains(out, "To fix") || !strings.Contains(out, "0 fail, 0 not judged, 1 cached") {
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
	lines := strings.Split(strings.TrimSpace(Annotations(in, nil, Range{})), "\n")
	if len(lines) != MaxAnnotations+1 || !strings.Contains(lines[len(lines)-1], "7 more refusals") {
		t.Fatalf("want %d annotations and a summary line, got %d lines, last %q", MaxAnnotations, len(lines), lines[len(lines)-1])
	}
}

func TestNoAnnotationsWhenGreen(t *testing.T) {
	if got := Annotations(mixed()[:1], nil, Range{}); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestGuardWrapsInjectedCommands(t *testing.T) {
	text := "stale\n::add-mask::secret\n::set-env name=X::1"
	out := Guard(text)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	tok := strings.TrimPrefix(lines[0], "::stop-commands::")
	if len(tok) < 32 || lines[len(lines)-1] != "::"+tok+"::" {
		t.Fatalf("not a stop-commands block:\n%s", out)
	}
	inner := strings.Join(lines[1:len(lines)-1], "\n")
	if inner != text || !strings.Contains(out, "\n::add-mask::secret\n") {
		t.Fatalf("the text must sit unchanged inside the block:\n%s", out)
	}
	if strings.Index(out, "::add-mask::") < strings.Index(out, "::stop-commands::") || strings.Index(out, "::add-mask::") > strings.LastIndex(out, "::"+tok+"::") {
		t.Fatal("::add-mask:: is outside the block")
	}
	if Guard(text) == out {
		t.Fatal("the token must be fresh per call")
	}
}

func TestFirstLines(t *testing.T) {
	diff := "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -3,0 +4,2 @@ x\n+a\n+b\n@@ -9 +11 @@\n-x\n+y\n" +
		"diff --git a/new.md b/new.md\n--- /dev/null\n+++ b/new.md\n@@ -0,0 +1,5 @@\n+x\n" +
		"diff --git a/gone.md b/gone.md\n--- a/gone.md\n+++ /dev/null\n@@ -1,3 +0,0 @@\n-x\n"
	got := FirstLines(diff)
	if got["a.go"] != 4 || got["new.md"] != 1 || len(got) != 2 {
		t.Fatalf("got %v", got)
	}
}

func TestManyFilesAreCappedInTheCell(t *testing.T) {
	o := checkrun.CheckOutcome{Rule: "r", Subject: "changeset", Status: "missing", Files: []string{"a", "b", "c", "d", "e", "f", "g"}}
	if s := Summary([]checkrun.CheckOutcome{o}, nil, rng); !strings.Contains(s, "+2 more") || strings.Contains(s, "`f`") {
		t.Fatalf("files not capped:\n%s", s)
	}
}

func hostile() []checkrun.CheckOutcome {
	return []checkrun.CheckOutcome{
		{Rule: "r|<b>x</b>", Subject: "changeset", Kind: "guard", Status: "fail", Source: "stored",
			Reason: "<script>alert(1)</script> | a ``` fence\n</details><img src=x onerror=1>\n```\nmore",
			Files:  []string{"a|b`c\nd.md", "<i>.md"}},
	}
}

func TestSummaryHostileGolden(t *testing.T) {
	got := Summary(hostile(), nil, rng)
	golden(t, "summary_hostile.golden", got)
	// the full reason sits in a fenced block (inert); everywhere else no raw markup may appear
	for _, ln := range strings.Split(got, "\n") {
		if strings.HasPrefix(ln, "|") || strings.HasPrefix(ln, "<details><summary>") {
			for _, bad := range []string{"<script>", "<img", "<i>", "<b>"} {
				if strings.Contains(ln, bad) {
					t.Fatalf("raw %q leaked into %q", bad, ln)
				}
			}
		}
	}
	for _, ln := range strings.Split(got, "\n") {
		if strings.HasPrefix(ln, "| r") && strings.Count(ln, "|") != 6 {
			t.Fatalf("a row was split: %q", ln)
		}
	}
}
