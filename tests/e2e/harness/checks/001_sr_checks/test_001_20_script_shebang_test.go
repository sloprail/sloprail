package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every script sloprail runs is exec'd DIRECTLY — no `sh <file>` fallback — so it must be
// executable and start with a shebang naming a standard interpreter. A rule whose declared
// script is not stays LOADED and enforced: it is reported (`sr-file declarations`, the next
// session hook) and the action it guards is refused at run time, both naming the file and the
// fix. A script only reached at run time (a judge mock) is refused when it is exec'd likewise.

// T001_20: a declared script without a shebang, without the execute bit, or with an
// interpreter outside /bin and /usr/bin is REPORTED by `sr-file declarations` and at the next
// session hook (the rule stays loaded and refuses what it guards), and a sound sibling is not
// reported.
// sr:proves checks/check-that-cannot-answer-refuses
func TestT001_20_AScriptWithoutShebangOrExecBitIsReported(t *testing.T) {
	e, proj := session(t)
	guard := "match: \"docs/**\"\nchecks:\n  - script: ./check.sh\n"
	e.FileGuard(proj, "fine", guard, map[string]string{"check.sh": "#!/usr/bin/env bash\nexit 0\n"})
	cases := []struct{ name, body, want string }{
		{"bare", "exit 0\n", "#!/usr/bin/env bash"},
		{"noexec", "#!/bin/sh\nexit 0\n", "chmod +x"},
		{"homebrew", "#!/usr/local/bin/bash\nexit 0\n", "#!/usr/bin/env bash"},
	}
	for _, c := range cases {
		e.FileGuard(proj, c.name, guard, nil)
		p := filepath.Join(proj, ".sloprail", "file-guard", c.name, "check.sh")
		mode := os.FileMode(0o755)
		if c.name == "noexec" {
			mode = 0o644
		}
		if err := os.WriteFile(p, []byte(c.body), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
	}

	decl := e.CLIDirect(proj, "sr-file", "declarations", proj)
	if decl.Code != 1 {
		t.Fatalf("sr-file declarations: exit %d, want 1 for rules whose script cannot run:\n%s", decl.Code, decl.Output)
	}
	for _, c := range cases {
		contains(t, decl.Output, "file-guard/"+c.name, "check.sh", c.want, "loaded and enforced")
	}
	if strings.Contains(decl.Output, "could not be loaded") {
		t.Errorf("an unrunnable script was reported as an unloadable rule:\n%s", decl.Output)
	}
	if strings.Contains(decl.Output, "file-guard/fine") {
		t.Errorf("the sound rule was reported as invalid:\n%s", decl.Output)
	}

	start := e.CLI(proj, "session", "start")
	contains(t, start.Output, "loaded and enforced", "check.sh")
}

// T001_21: a script reached only at run time is refused when exec'd. The judge mock
// (SR_CHECKS_JUDGE_MOCKS) is one: pointed at a file without a shebang it is an error
// naming the file and the fix, never run through `sh`; with a shebang it answers.
func TestT001_21_AJudgeMockWithoutShebangIsRefusedAtExec(t *testing.T) {
	e, proj := session(t)
	e.FileGuard(proj, "docs", judgeRule, map[string]string{"rubric.md.j2": rubric})
	base := e.CommitAll(proj, "the rule")
	e.WriteFile(proj, "docs/a.md", "the release is Friday\n")
	e.CommitAll(proj, "add a")

	mock := filepath.Join(t.TempDir(), "mock.sh")
	mocks := func() []string {
		raw, _ := json.Marshal(map[string]string{"file-guard/docs/rubric": mock})
		return append(e.SessionEnv(sessionID), "SR_CHECKS_JUDGE_MOCKS="+string(raw))
	}
	run := func() string {
		r := quiet(e.CLIDirectEnv(proj, mocks(), "sr-checks", "run", "--base", base, "--head", "HEAD"))
		return r.Output
	}

	if err := os.WriteFile(mock, []byte("cat >/dev/null\necho '{\"pass\": true, \"reasoning\": \"ok\"}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := run()
	contains(t, out, "mock.sh", "#!/usr/bin/env bash")

	if err := os.WriteFile(mock, []byte("#!/bin/sh\ncat >/dev/null\necho '{\"pass\": true, \"reasoning\": \"ok\"}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out := run(); strings.Contains(out, "#!/usr/bin/env bash") {
		t.Fatalf("a mock with a shebang was still refused:\n%s", out)
	}
}
