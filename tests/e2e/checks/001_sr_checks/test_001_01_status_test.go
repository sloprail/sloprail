package e2e

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"
)

// T001_01: nothing recorded for a range is a listing that says so, not a fault. And with
// no file-guard at all, show is empty.
func TestT001_01_NothingRecordedYet(t *testing.T) {
	e, proj := session(t)

	if res := checks(e, proj, "show", "--base", "HEAD", "--head", "HEAD"); res.Code != 0 || res.Output != "" {
		t.Fatalf("human show with nothing recorded: exit %d output %q", res.Code, res.Output)
	}

	base := judged(t, e, proj, verdictPass)
	rows := statusRows(t, e, proj, "--base", base, "--head", "HEAD")
	if len(rows) != 1 || rows[0].Rule != "file-guard/docs" || rows[0].Status != "missing" {
		t.Fatalf("rows = %+v, want the judge as missing", rows)
	}
	if n := e.JudgeCalls(proj, promptFile, ""); n != 0 {
		t.Fatalf("show asked the judge (%d calls)", n)
	}
}

// T001_02: show is a read: with no session at all it still lists what is stored (verify's
// read-only needs none), and it does not refuse for want of one.
func TestT001_02_NoSession(t *testing.T) {
	e, proj := session(t)
	base := judged(t, e, proj, verdictFail)
	if r := checks(e, proj, "run", "--base", base, "--head", "HEAD"); r.Code != 1 {
		t.Fatalf("premise: the failing judge did not refuse: exit %d:\n%s", r.Code, r.Output)
	}
	res := quiet(e.CLIDirectEnv(proj, harness.NoSessionEnv, "sr-checks", "show", "--json", "--base", base, "--head", "HEAD"))
	if res.Code != 0 || strings.Contains(strings.ToLower(res.Output), "no session") {
		t.Fatalf("show outside a session: exit %d:\n%s", res.Code, res.Output)
	}
	var rows []Row
	if err := json.Unmarshal([]byte(res.Output), &rows); err != nil || len(rows) != 1 || rows[0].Status != "fail" {
		t.Fatalf("show outside a session = %q (%v), want the stored fail", res.Output, err)
	}
}

// T001_03: show shows each subject's LATEST result and its reasons: the failing run's
// reasoning, then the fix's pass.
func TestT001_03_StatusLatestRunFailingAndRule(t *testing.T) {
	e, proj := session(t)
	base := judged(t, e, proj, verdictFail)
	if r := checks(e, proj, "run", "--base", base, "--head", "HEAD"); r.Code != 1 {
		t.Fatalf("premise: the failing judge did not refuse: exit %d:\n%s", r.Code, r.Output)
	}

	failing := statusRows(t, e, proj, "--base", base, "--head", "HEAD", "--failing")
	if len(failing) != 1 || failing[0].Rule != "file-guard/docs" || failing[0].Status != "fail" {
		t.Fatalf("show = %+v, want only docs' failing judge", failing)
	}

	human := checks(e, proj, "show", "--base", base, "--head", "HEAD")
	if human.Code != 0 {
		t.Fatalf("exit %d:\n%s", human.Code, human.Output)
	}
	contains(t, human.Output, "fail", "file-guard/docs")

	one := statusRows(t, e, proj, "--base", base, "--head", "HEAD", "--rule", "docs")
	if len(one) != 1 || one[0].Rule != "file-guard/docs" {
		t.Fatalf("--rule docs = %+v, want the docs guard", one)
	}
	if none := statusRows(t, e, proj, "--base", base, "--head", "HEAD", "--rule", "size"); len(none) != 0 {
		t.Fatalf("--rule size = %+v, want nothing: no such guard", none)
	}

	e.InstallJudgeClaudeCapturing(proj, promptFile, verdictPass)
	e.WriteFile(proj, "docs/a.md", "the release is Monday\n")
	e.CommitAll(proj, "fix a")
	if r := checks(e, proj, "run", "--base", base, "--head", "HEAD"); r.Code != 0 {
		t.Fatalf("the fix was refused: exit %d:\n%s", r.Code, r.Output)
	}
	all := statusRows(t, e, proj, "--base", base, "--head", "HEAD")
	if len(all) != 1 || all[0].Status != "pass" { // the latest run: the fix passed
		t.Fatalf("show = %+v, want the fix's pass", all)
	}
	if f := statusRows(t, e, proj, "--base", base, "--head", "HEAD", "--failing"); len(f) != 0 {
		t.Fatalf("--failing = %+v after the fix passed", f)
	}
}

// T001_04: a judge that produced no verdict is a failed verdict carrying its message (a
// refusal is a complete fail; only an engine-internal error is left unjudged). It is never
// shown as an empty range or a pass.
func TestT001_04_AnEngineFailureIsAnError(t *testing.T) {
	e, proj := session(t)
	base := judged(t, e, proj, "not a verdict")

	r := checks(e, proj, "run", "--base", base, "--head", "HEAD")
	if r.Code == 0 {
		t.Fatalf("a judge that answered nothing passed:\n%s", r.Output)
	}
	contains(t, r.Output, "the judge did not produce")
	rows := statusRows(t, e, proj, "--base", base, "--head", "HEAD")
	if len(rows) != 1 || rows[0].Status != "fail" || rows[0].Reason != "the judge did not produce a JSON verdict object (after 2 attempts)" {
		t.Fatalf("rows = %+v, want a failed verdict (never an empty range, never a pass) carrying its exact message", rows)
	}
	if failing := statusRows(t, e, proj, "--base", base, "--head", "HEAD", "--failing"); len(failing) != 1 || failing[0].Status != "fail" {
		t.Fatalf("--failing = %+v, want the failed verdict", failing)
	}
	if res := checks(e, proj, "show", "--base", "no-such-rev", "--head", "HEAD"); res.Code == 0 {
		t.Fatalf("show over an unresolvable range succeeded:\n%s", res.Output)
	}
}

// T001_05: `sr checks show` through the proxy is the same run.
func TestT001_05_ProxyEqualsDirect(t *testing.T) {
	e, proj := session(t)
	base := judged(t, e, proj, verdictFail)
	checks(e, proj, "run", "--base", base, "--head", "HEAD")

	direct := checks(e, proj, "show", "--base", base, "--head", "HEAD")
	proxied := quiet(e.CLIDirectEnv(proj, e.SessionEnv(sessionID), "sr", "checks", "show", "--base", base, "--head", "HEAD"))
	if direct.Code != proxied.Code || direct.Output != proxied.Output {
		t.Fatalf("proxy differs:\ndirect  (%d) %q\nproxied (%d) %q", direct.Code, direct.Output, proxied.Code, proxied.Output)
	}
}
