package e2e

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/tests/e2e/harness"

	_ "modernc.org/sqlite"
)

// subjectsRule splits specs/ into one subject per file, each checked on its own.
const subjectsRule = "match: \"specs/**\"\nsubjects: ./subjects.sh\nchecks:\n  - script: ./check.sh\n"

const subjectsScript = `#!/bin/sh
jq -c '[.changeset.files[] | {id: (.path | split("/")[1] | sub("\\.md$"; "")), files: [.path]}]'
`

// checkScript records the subject it is handed (one line per call) and refuses "bad".
func checkScript(ledger string) string {
	return `#!/bin/sh
payload="$(cat)"
printf '%s\n' "$(printf '%s' "$payload" | jq -r '.subject.id + " " + (.subject.files | join(","))')" >> ` + ledger + `
if [ "$(printf '%s' "$payload" | jq -r '.subject.id')" = "bad" ]; then
  echo '{"reason":"bad is not allowed"}'
  exit 1
fi
exit 0
`
}

func ledgerLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	sort.Strings(out)
	return out
}

// T001_06: a rule with a `subjects:` script is handed ONE payload per subject, each subject
// has its own verdict, and a subject already judged is not run again.
func TestT001_06_SubjectsScriptOnePayloadAndVerdictPerSubject(t *testing.T) {
	e, proj := session(t)
	led := filepath.Join(t.TempDir(), "ledger")
	e.FileGuard(proj, "specs", subjectsRule, map[string]string{"subjects.sh": subjectsScript, "check.sh": checkScript(led)})
	base := e.CommitAll(proj, "the rule")
	e.WriteFile(proj, "specs/good.md", "fine\n")
	e.WriteFile(proj, "specs/bad.md", "not fine\n")
	e.CommitAll(proj, "two specs")

	shown := checks(e, proj, "changeset", "--rule", "specs", "--base", base, "--head", "HEAD")
	if shown.Code != 0 {
		t.Fatalf("changeset exited %d:\n%s", shown.Code, shown.Output)
	}
	var out struct {
		Payload  json.RawMessage `json:"payload"`
		Subjects []struct {
			ID      string `json:"id"`
			Payload struct {
				Subject struct {
					ID    string   `json:"id"`
					Files []string `json:"files"`
				} `json:"subject"`
			} `json:"payload"`
		} `json:"subjects"`
	}
	if err := json.Unmarshal([]byte(shown.Output), &out); err != nil {
		t.Fatalf("changeset printed unreadable JSON: %v\n%s", err, shown.Output)
	}
	if len(out.Payload) != 0 || len(out.Subjects) != 2 {
		t.Fatalf("changeset = %s, want one payload per subject and no single payload", shown.Output)
	}
	for _, s := range out.Subjects {
		if s.Payload.Subject.ID != s.ID || len(s.Payload.Subject.Files) != 1 || s.Payload.Subject.Files[0] != "specs/"+s.ID+".md" {
			t.Fatalf("subject %s was handed %+v", s.ID, s.Payload.Subject)
		}
	}

	if r := checks(e, proj, "run", "--base", base, "--head", "HEAD"); r.Code != 1 {
		t.Fatalf("run: exit %d, want 1 (bad is refused):\n%s", r.Code, r.Output)
	}
	if got := ledgerLines(t, led); strings.Join(got, "|") != "bad specs/bad.md|good specs/good.md" {
		t.Fatalf("the check ran for %q, want once per subject, each handed its own file", got)
	}

	verdicts := map[string]string{}
	for _, row := range statusRows(t, e, proj, "--base", base, "--head", "HEAD") {
		verdicts[row.Subject] = row.Status
	}
	if len(verdicts) != 2 || verdicts["good"] != "pass" || verdicts["bad"] != "fail" {
		t.Fatalf("per-subject verdicts = %v, want good=pass bad=fail", verdicts)
	}
	if v := checks(e, proj, "verify", "--base", base, "--head", "HEAD"); v.Code != 1 || !strings.Contains(v.Output, "bad is not allowed") {
		t.Fatalf("verify: exit %d, want the bad subject's refusal:\n%s", v.Code, v.Output)
	}

	// Each verdict is keyed by its own subject: a second run over the same content finds good's
	// pass (a hit, nothing runs) and asks the script again for bad only (a script's refusal is
	// cheap and is checked again; only a judge's is replayed).
	checks(e, proj, "run", "--base", base, "--head", "HEAD")
	if got := ledgerLines(t, led); strings.Join(got, "|") != "bad specs/bad.md|bad specs/bad.md|good specs/good.md" {
		t.Fatalf("a second run ran the check for %q, want bad again and good not at all", got)
	}
}

// T001_07: a range that quotes citations can only be judged where the session's transcript
// is. A run with no session says so, and stores nothing, so verify still says "not judged yet".
// sr:proves cache/unfinished-never-stored
func TestT001_07_RunWithoutASessionNeedsOneToJudgeCitations(t *testing.T) {
	e, proj := session(t)
	e.FileGuard(proj, "docs", "match: \"docs/**\"\nrequire:\n  - citation: {source_types: [user]}\nchecks:\n  - script: ./check.sh\n",
		map[string]string{"check.sh": "#!/bin/sh\nexit 0\n"})
	base := e.CommitAll(proj, "the rule")
	e.WriteFile(proj, "docs/a.md", "the release is Friday\n")
	e.CommitAll(proj, "add a", harness.CitesUser("document the release"))

	res := quiet(e.CLIDirectEnv(proj, harness.NoSessionEnv, "sr-checks", "run", "--base", base, "--head", "HEAD"))
	if res.Code != 1 {
		t.Fatalf("a sessionless run over a cited range: exit %d, want a refusal:\n%s", res.Code, res.Output)
	}
	contains(t, res.Output, "needs a session to judge")

	v := quiet(e.CLIDirectEnv(proj, harness.NoSessionEnv, "sr-checks", "verify", "--base", base, "--head", "HEAD"))
	if v.Code != 1 || !strings.Contains(v.Output, "not judged yet") {
		t.Fatalf("the sessionless run stored a verdict: verify exit %d:\n%s", v.Code, v.Output)
	}
}

func repoFile(parts ...string) string {
	abs, err := filepath.Abs(filepath.Join(append([]string{"..", "..", "..", ".."}, parts...)...))
	if err != nil {
		panic(err)
	}
	return abs
}

// T001_09: a session's state.db written by an older engine (before session_refs became the
// table of tracked ranges) is migrated in place when a hook next opens it: the hook works
// and the rows it held are kept, an abandoned one now reading as untracked.
func TestT001_09_SessionRefsMigrationKeepsOldRows(t *testing.T) {
	e, proj := session(t)
	path := e.StateDBPath(proj, sessionID)
	id := filepath.Base(filepath.Dir(path))
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(path + suffix)
	}

	// The schema as of migration 005: session_refs without base, added_by, untracked_reason.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(repoFile("internal", "sessionstate", "migrations", "*.sql"))
	if err != nil || len(files) < 6 {
		t.Fatalf("migrations: %v %v", files, err)
	}
	sort.Strings(files)
	for _, f := range files[:5] {
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(body)); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
	for _, q := range []string{
		`INSERT INTO session_refs (session_id, folder, ref, first_tip, tip, agent_id, abandoned_tip) VALUES ('` + id + `', '` + proj + `', 'refs/heads/kept', 'aaaa', 'bbbb', '', '')`,
		`INSERT INTO session_refs (session_id, folder, ref, first_tip, tip, agent_id, abandoned_tip) VALUES ('` + id + `', '` + proj + `', 'refs/heads/dropped', 'cccc', 'dddd', '', 'dddd')`,
		`PRAGMA user_version = 5`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("old-schema fixture: %v\n%s", err, q)
		}
	}
	db.Close()

	res := e.Run(proj, sessionID, "again", Turns("done", Bash("b2", "true")))
	if res.Code != 0 {
		t.Fatalf("the hooks failed over an old-schema state.db: exit %d:\n%s", res.Code, res.Output)
	}

	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	reason := map[string]string{}
	rows, err := db.Query(`SELECT ref, untracked_reason FROM session_refs WHERE ref IN ('refs/heads/kept', 'refs/heads/dropped')`)
	if err != nil {
		t.Fatalf("session_refs was not migrated: %v", err)
	}
	for rows.Next() {
		var ref, why string
		if err := rows.Scan(&ref, &why); err != nil {
			t.Fatal(err)
		}
		reason[ref] = why
	}
	rows.Close()
	if len(reason) != 2 || reason["refs/heads/kept"] != "" || reason["refs/heads/dropped"] != "abandoned by an older engine" {
		t.Fatalf("migrated rows = %v, want both kept, only the abandoned one untracked", reason)
	}
}
