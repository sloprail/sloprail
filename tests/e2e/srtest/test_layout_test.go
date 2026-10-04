package e2e

import (
	"bufio"
	"encoding/json"
	"strings"
	"testing"
)

type layoutResult struct{ Subject, Owner, Status string }

func runResults(t *testing.T, out string) map[string]layoutResult {
	t.Helper()
	got := map[string]layoutResult{}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		var r layoutResult
		if json.Unmarshal(sc.Bytes(), &r) == nil && r.Subject != "" {
			got[r.Subject] = r
		}
	}
	return got
}

// TestSrTestOwnerLayout: a case lives in its owning rule's folder. `sr-test run` finds
// <nature>/<rule>/tests/<case> and file-guard/structure.tests/<case>, names each result
// "<owner>:<case>" with an owner field, ignores the retired top-level tests/, and --rule / --only select.
func TestSrTestOwnerLayout(t *testing.T) {
	e := New(t)
	root := t.TempDir()
	write(t, root, ".sloprail/gate/g/tests/a/test.sh", "#!/bin/sh\ntest -f .sloprail/gate/g/gate.yaml\n")
	write(t, root, ".sloprail/gate/g/gate.yaml", "")
	write(t, root, ".sloprail/file-guard/f/tests/a/test.sh", "#!/bin/sh\nexit 0\n")
	write(t, root, ".sloprail/file-guard/structure.tests/s/test.sh", "#!/bin/sh\nexit 0\n")
	write(t, root, ".sloprail/tests/old/test.sh", "#!/bin/sh\nexit 1\n")

	res := e.CLIDirect(root, "sr-test", "run")
	got := runResults(t, res.Output)
	if len(got) != 3 {
		t.Fatalf("want 3 cases, got %v\n%s", got, res.Output)
	}
	for subject, owner := range map[string]string{"gate/g:a": "gate/g", "file-guard/f:a": "file-guard/f", "file-guard/structure:s": "file-guard/structure"} {
		if r := got[subject]; r.Status != "pass" || r.Owner != owner {
			t.Errorf("%s: %+v", subject, r)
		}
	}

	res = e.CLIDirect(root, "sr-test", "run", "--rule", "file-guard/structure")
	if got := runResults(t, res.Output); len(got) != 1 || got["file-guard/structure:s"].Status != "pass" {
		t.Errorf("--rule: %v\n%s", got, res.Output)
	}
	res = e.CLIDirect(root, "sr-test", "run", "--only", ":a")
	if got := runResults(t, res.Output); len(got) != 2 {
		t.Errorf("--only: %v\n%s", got, res.Output)
	}
}

// TestSrTestDoctorListsRulesWithoutCases: doctor is structural: a rule folder holding a declaration whose
// tests/ has no case (and a structure gate with no structure.tests/ case) is "uncovered", and it exits non-zero.
func TestSrTestDoctorListsRulesWithoutCases(t *testing.T) {
	e := New(t)
	root := t.TempDir()
	write(t, root, ".sloprail/gate/covered/gate.yaml", "")
	write(t, root, ".sloprail/gate/covered/tests/a/test.sh", "#!/bin/sh\nexit 1\n") // never run by doctor
	write(t, root, ".sloprail/gate/bare/gate.yaml", "")
	write(t, root, ".sloprail/file-guard/structure.yaml", "")
	write(t, root, ".sloprail/tests/old/test.sh", "#!/bin/sh\nexit 0\n")

	res := e.CLIDirect(root, "sr-test", "doctor")
	if res.Code == 0 {
		t.Errorf("want a non-zero exit\n%s", res.Output)
	}
	for _, want := range []string{"uncovered: gate:bare", "uncovered: structure:structure"} {
		if !strings.Contains(res.Output, want) {
			t.Errorf("missing %q in\n%s", want, res.Output)
		}
	}
	if strings.Contains(res.Output, "gate:covered") {
		t.Errorf("covered rule listed:\n%s", res.Output)
	}

	write(t, root, ".sloprail/gate/bare/tests/a/test.sh", "#!/bin/sh\n")
	write(t, root, ".sloprail/file-guard/structure.tests/a/test.sh", "#!/bin/sh\n")
	res = e.CLIDirect(root, "sr-test", "doctor")
	if res.Code != 0 || !strings.Contains(res.Output, "every rule is covered") {
		t.Errorf("want covered, exit 0: %d\n%s", res.Code, res.Output)
	}
}
