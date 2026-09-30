package e2e

import (
	"testing"
)

// T001_01: nothing recorded for a rule is an empty listing, not a fault. And with
// no check results at all (deleted here: the mock session's own Stop had
// evaluated the plugin's file-guards and recorded them), status is empty and a
// query has no tables to run against and says so.
func TestT001_01_NothingRecordedYet(t *testing.T) {
	e, proj := session(t)

	if rows := statusRows(t, e, proj, "--rule", "never-evaluated"); len(rows) != 0 {
		t.Fatalf("rows = %+v, want none", rows)
	}

	e.RemoveCheckResults(proj, sessionID)
	if rows := statusRows(t, e, proj); len(rows) != 0 {
		t.Fatalf("rows = %+v, want none", rows)
	}
	if res := checks(e, proj, "status"); res.Code != 0 || res.Output != "" {
		t.Fatalf("human status with nothing recorded: exit %d output %q", res.Code, res.Output)
	}

	res := checks(e, proj, "sql", "select 1")
	if res.Code == 0 {
		t.Fatalf("sql ran with no check results to read:\n%s", res.Output)
	}
	contains(t, res.Output, "no check results")
}

// T001_02: outside a session there is nothing to read, and the command says why.
func TestT001_02_NoSession(t *testing.T) {
	e := New(t)
	proj := e.Project()
	e.GitInit(proj)
	res := e.CLIDirectEnv(proj, []string{"CLAUDE_CODE_SESSION_ID=", "CLAUDECODE="}, "sr-checks", "status")
	if res.Code == 0 {
		t.Fatalf("status outside a session succeeded:\n%s", res.Output)
	}
	contains(t, res.Output, "no session")
}

// T001_03: status shows each rule's LATEST run and its checks; --failing keeps
// only what is failing; --rule narrows to one rule.
func TestT001_03_StatusLatestRunFailingAndRule(t *testing.T) {
	e, proj := session(t)
	e.RecordCheckRun(proj, sessionID, run("file-guard/size", "head0000"), script("pass"), judge("fail", "fp0", "too long"))
	e.RecordCheckRun(proj, sessionID, run("file-guard/size", "head1111"), script("pass"), judge("pass", "fp1", "fine now"))
	e.RecordCheckRun(proj, sessionID, run("file-guard/docs", "head1111"), script("pass"), judge("fail", "fp2", "the ADR is not cited"))
	e.RecordCheckRun(proj, sessionID, run("file-guard/quiet", "head1111"))

	all := statusRows(t, e, proj)
	got := map[string][]string{}
	for _, r := range all {
		got[r.Rule] = append(got[r.Rule], r.Status)
	}
	want := map[string][]string{
		"file-guard/size":  {"pass", "pass"}, // the latest run: the fix passed
		"file-guard/docs":  {"pass", "fail"},
		"file-guard/quiet": {"pass"}, // nothing selected: a pass with no check
	}
	for rule, statuses := range want {
		if len(got[rule]) != len(statuses) {
			t.Fatalf("%s = %v, want %v (all: %+v)", rule, got[rule], statuses, all)
		}
		for i := range statuses {
			if got[rule][i] != statuses[i] {
				t.Fatalf("%s = %v, want %v", rule, got[rule], statuses)
			}
		}
	}

	failing := statusRows(t, e, proj, "--failing")
	if len(failing) != 1 || failing[0].Rule != "file-guard/docs" || failing[0].Status != "fail" {
		t.Fatalf("--failing = %+v, want only docs' failing judge", failing)
	}

	one := statusRows(t, e, proj, "--rule", "size")
	for _, r := range one {
		if r.Rule != "file-guard/size" {
			t.Fatalf("--rule size returned %+v", r)
		}
	}
	if len(one) != 2 {
		t.Fatalf("--rule size = %+v", one)
	}

	human := checks(e, proj, "status", "--failing")
	if human.Code != 0 {
		t.Fatalf("exit %d:\n%s", human.Code, human.Output)
	}
	contains(t, human.Output, "fail", "file-guard/docs", "the ADR is not cited", "base000..head1111")
}

// T001_04: a run that failed as an engine shows as an error carrying its
// message. It is never shown as an empty range.
func TestT001_04_AnEngineFailureIsAnError(t *testing.T) {
	e, proj := session(t)
	bad := run("file-guard/broken", "head1111")
	bad.ExitCode, bad.Error = 1, "git: bad object 0123"
	e.RecordCheckRun(proj, sessionID, bad)

	rows := statusRows(t, e, proj, "--failing")
	if len(rows) != 1 || rows[0].Status != "error" || rows[0].Error != "git: bad object 0123" {
		t.Fatalf("rows = %+v, want the engine failure as an error", rows)
	}
	contains(t, checks(e, proj, "status", "--failing").Output, "error", "git: bad object 0123")
}

// T001_05: `sr checks status` through the proxy is the same run.
func TestT001_05_ProxyEqualsDirect(t *testing.T) {
	e, proj := session(t)
	e.RecordCheckRun(proj, sessionID, run("file-guard/size", "head0000"), judge("fail", "fp0", "too long"))

	direct := checks(e, proj, "status")
	proxied := e.CLIDirectEnv(proj, inSession(e), "sr", "checks", "status")
	if direct.Code != proxied.Code || direct.Output != proxied.Output {
		t.Fatalf("proxy differs:\ndirect  (%d) %q\nproxied (%d) %q", direct.Code, direct.Output, proxied.Code, proxied.Output)
	}
}
