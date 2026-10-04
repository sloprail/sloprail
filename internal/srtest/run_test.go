package srtest

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func project(t *testing.T, cases map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for n, body := range cases {
		d := filepath.Join(root, ".sloprail", "gate", "r", "tests", n)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "test.sh"), []byte("#!/usr/bin/env bash\n"+body), 0o755); err != nil {
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
	rs, err := Run(root, Options{Rules: func(Context, io.Writer) []string { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	m := byName(rs)
	want := map[string]string{"a-pass": Pass, "b-fail": Fail, "c-error": Error, "d-other": Fail}
	for k, v := range want {
		if m["gate/r:"+k].Status != v {
			t.Errorf("%s: %s want %s", k, m["gate/r:"+k].Status, v)
		}
	}
	if m["gate/r:a-pass"].Output != "" || !strings.Contains(m["gate/r:b-fail"].Output, "boom") || !strings.Contains(m["gate/r:c-error"].Output, "bad") {
		t.Errorf("output: %+v", rs)
	}
	if rs[0].Owner != "gate/r" || rs[0].Metadata.Owner != "gate/r" || rs[0].CheckID != "sr-test" || rs[0].Kind != "test" || rs[0].CheckedAt == "" {
		t.Errorf("shape: %+v", rs[0])
	}
}

func TestEnvAndEvents(t *testing.T) {
	root := project(t, map[string]string{"e": `
test -f "$SR_TEST_CASE_DIR/test.sh" || exit 1
test "$SR_CHECKS_JUDGE_MOCKS" = "{}" || exit 1
test ! -e .sloprail/gate/r/tests || exit 1   # the case is not part of the project it runs in
case "$PWD" in */project) ;; *) exit 1 ;; esac
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

// test.sh is exec'd directly: a missing shebang or execute bit is an error result
// naming the file and the fix, never a silent `sh test.sh`.
func TestTestShNeedsShebangAndExecBit(t *testing.T) {
	root := project(t, map[string]string{"ok": "exit 0"})
	for name, tc := range map[string]struct {
		body string
		mode os.FileMode
		want string
	}{
		"no-shebang": {"exit 0\n", 0o755, "#!/usr/bin/env bash"},
		"no-exec":    {"#!/bin/sh\nexit 0\n", 0o644, "chmod +x"},
	} {
		d := filepath.Join(root, ".sloprail", "gate", "r", "tests", name)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(d, "test.sh")
		if err := os.WriteFile(p, []byte(tc.body), tc.mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, tc.mode); err != nil {
			t.Fatal(err)
		}
	}
	rs, err := Run(root, Options{Rules: func(Context, io.Writer) []string { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	m := byName(rs)
	if m["gate/r:ok"].Status != Pass {
		t.Errorf("ok: %+v", m["gate/r:ok"])
	}
	for name, want := range map[string]string{"no-shebang": "#!/usr/bin/env bash", "no-exec": "chmod +x"} {
		r := m["gate/r:"+name]
		if r.Status != Error || !strings.Contains(r.Output, "test.sh") || !strings.Contains(r.Output, want) {
			t.Errorf("%s: want an error naming test.sh and %q, got %+v", name, want, r)
		}
	}
}

// The case is not part of the project it runs in: the project's .sloprail/ copy holds the rules, not
// any rule's tests/ nor structure.tests/.
func TestCasesAreNotCopiedIntoTheProject(t *testing.T) {
	root := t.TempDir()
	put(t, root, ".sloprail/gate/g/tests/a/test.sh", `test -f .sloprail/gate/g/gate.yaml && test ! -e .sloprail/gate/g/tests && test ! -e .sloprail/file-guard/structure.tests && test -f .sloprail/file-guard/structure.yaml`)
	put(t, root, ".sloprail/gate/g/gate.yaml", "")
	put(t, root, ".sloprail/file-guard/structure.yaml", "")
	put(t, root, ".sloprail/file-guard/structure.tests/s/test.sh", "exit 0")
	rs, err := Run(root, Options{})
	if err != nil || len(rs) != 2 {
		t.Fatalf("%v %+v", err, rs)
	}
	for _, r := range rs {
		if r.Status != Pass {
			t.Errorf("%s: %s", r.Subject, r.Output)
		}
	}
}

func TestOnlyAndOwnerFilters(t *testing.T) {
	root := t.TempDir()
	put(t, root, ".sloprail/gate/g/tests/a/test.sh", "exit 0")
	put(t, root, ".sloprail/gate/g/tests/b/test.sh", "exit 0")
	put(t, root, ".sloprail/context/c/tests/a/test.sh", "exit 0")
	subjects := func(o Options) string {
		rs, err := Run(root, o)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, r := range rs {
			out = append(out, r.Subject)
		}
		return strings.Join(out, ",")
	}
	if got := subjects(Options{Owners: []string{"gate/g"}}); got != "gate/g:a,gate/g:b" {
		t.Errorf("owner: %s", got)
	}
	if got := subjects(Options{Only: []string{":a"}}); got != "context/c:a,gate/g:a" {
		t.Errorf("only: %s", got)
	}
	if got := subjects(Options{Only: []string{":a"}, Owners: []string{"gate/g"}}); got != "gate/g:a" {
		t.Errorf("both: %s", got)
	}
	if got := subjects(Options{Owners: []string{"gate/none"}}); got != "" {
		t.Errorf("none: %s", got)
	}
}

func TestRunWithNoCases(t *testing.T) {
	rs, err := Run(t.TempDir(), Options{})
	if err != nil || len(rs) != 0 {
		t.Fatalf("%v %v", err, rs)
	}
}
