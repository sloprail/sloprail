package dispatch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mockJudge(t *testing.T, qualified string, script string, mocks func(path string) map[string]string) (Verdict, error) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "is-it-good.md.j2"), []byte("rubric"), 0o644); err != nil {
		t.Fatal(err)
	}
	sp := filepath.Join(dir, "mock.sh")
	if err := os.WriteFile(sp, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(mocks(sp))
	return runMockedJudge(judgeCall{Dir: dir, Template: "./is-it-good.md.j2", InputJSON: []byte(`{"a":1}`), Qualified: qualified}, string(raw))
}

func TestJudgeID(t *testing.T) {
	if got := JudgeID("sloprail/file-guard/capability-rigor", "./tests-prove-as-documented.md.j2"); got != "sloprail/file-guard/capability-rigor/tests-prove-as-documented" {
		t.Fatal(got)
	}
	if got := JudgeID("file-guard/m", "x.j2"); got != "file-guard/m/x" {
		t.Fatal(got)
	}
}

func TestMockedJudgeVerdicts(t *testing.T) {
	id := "file-guard/memory-quality/is-it-good"
	at := func(p string) map[string]string { return map[string]string{id: p} }
	v, err := mockJudge(t, "file-guard/memory-quality", `cat >/dev/null; echo '{"pass":true,"reasoning":""}'`, at)
	if err != nil || v.Refused {
		t.Fatalf("pass: %+v %v", v, err)
	}
	v, err = mockJudge(t, "file-guard/memory-quality", `cat >/dev/null; echo '{"pass":false,"reasoning":"needs a Why"}'`, at)
	if err != nil || !v.Refused || v.Reason != "needs a Why" {
		t.Fatalf("fail: %+v %v", v, err)
	}
	// stdin carries the prompt and the payload
	v, err = mockJudge(t, "file-guard/memory-quality", `if grep -q rubric && true; then echo '{"pass":true}'; else echo '{"pass":false,"reasoning":"x"}'; fi`, at)
	if err != nil || v.Refused {
		t.Fatalf("stdin: %+v %v", v, err)
	}
}

func TestMockedJudgeErrorsAreNeverPasses(t *testing.T) {
	id := "file-guard/memory-quality/is-it-good"
	at := func(p string) map[string]string { return map[string]string{id: p} }
	for name, script := range map[string]string{
		"exit non-zero": `echo '{"pass":true}'; exit 3`,
		"bad json":      `echo nope`,
		"no pass key":   `echo '{"reasoning":"x"}'`,
	} {
		if _, err := mockJudge(t, "file-guard/memory-quality", script, at); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	_, err := mockJudge(t, "file-guard/memory-quality", `echo '{"pass":true}'`, func(string) map[string]string { return map[string]string{} })
	if err == nil || !strings.Contains(err.Error(), "no entry") {
		t.Fatalf("unlisted: %v", err)
	}
	_, err = mockJudge(t, "plugin/file-guard/memory-quality", `echo '{"pass":true}'`, at)
	if err == nil {
		t.Fatal("plugin id must not match an in-repo entry")
	}
}
