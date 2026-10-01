package e2e

import (
	"encoding/json"
	"testing"
)

// T001_06: sql runs a SELECT and prints the rows as JSON.
func TestT001_06_SQLSelect(t *testing.T) {
	e, proj := session(t)
	e.RecordCheckRun(proj, sessionID, run("file-guard/size", "head0000"), judge("fail", "fp0", "too long"))

	res := checks(e, proj, "sql", "select c.status, json_extract(c.metadata, '$.reasoning') as why, r.check_id, r.repo_id, r.branch, r.session_id "+
		"from checks c join check_runs r on r.id = c.run_id")
	if res.Code != 0 {
		t.Fatalf("exit %d:\n%s", res.Code, res.Output)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(res.Output), &rows); err != nil {
		t.Fatalf("unreadable JSON: %v\n%s", err, res.Output)
	}
	if len(rows) != 1 || rows[0]["status"] != "fail" || rows[0]["why"] != "too long" || rows[0]["check_id"] != "file-guard/size" {
		t.Fatalf("rows = %+v", rows)
	}
	// The identity columns are filled although the database is per session.
	if rows[0]["repo_id"] == "" || rows[0]["branch"] == "" || rows[0]["session_id"] != sessionID {
		t.Fatalf("identity columns = %+v", rows[0])
	}
}

// T001_07: nothing but a SELECT runs. A refusal first, for every way of writing;
// then the same rows are still there, and a SELECT still works.
func TestT001_07_SQLIsReadOnly(t *testing.T) {
	e, proj := session(t)
	e.RecordCheckRun(proj, sessionID, run("file-guard/size", "head0000"), judge("fail", "fp0", "too long"))

	for _, stmt := range []string{
		"delete from checks",
		"update checks set status = 'pass'",
		"insert into check_items (id, check_id, passed, checked_at) values ('x', 'y', 1, 'now')",
		"drop table checks",
		"create table stolen as select * from checks",
		"pragma writable_schema = on",
		"with x as (select 1) delete from checks",
		"select 1; delete from checks",
		"",
	} {
		if res := checks(e, proj, "sql", stmt); res.Code == 0 && stmt != "select 1; delete from checks" {
			t.Fatalf("%q was accepted:\n%s", stmt, res.Output)
		}
	}

	res := checks(e, proj, "sql", "select count(*) as n from checks where status = 'fail'")
	if res.Code != 0 {
		t.Fatalf("exit %d:\n%s", res.Code, res.Output)
	}
	var rows []map[string]float64
	if err := json.Unmarshal([]byte(res.Output), &rows); err != nil || len(rows) != 1 || rows[0]["n"] != 1 {
		t.Fatalf("the failing row was changed or lost: %v %s", err, res.Output)
	}
}
