package cursor

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

type hooksFile struct {
	Version int                         `json:"version"`
	Hooks   map[string][]map[string]any `json:"hooks"`
	Extra   any                         `json:"extra"`
}

func readHooks(t *testing.T, project string) hooksFile {
	t.Helper()
	raw, err := os.ReadFile(HooksPath(project))
	if err != nil {
		t.Fatal(err)
	}
	var f hooksFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	return f
}

func commands(f hooksFile, event string) []string {
	var out []string
	for _, e := range f.Hooks[event] {
		out = append(out, e["command"].(string))
	}
	return out
}

func TestInstallProjectHooksCreatesTheFile(t *testing.T) {
	project := t.TempDir()
	if err := (Harness{}).InstallProjectHooks(project, "/p/sloprail"); err != nil {
		t.Fatal(err)
	}
	f := readHooks(t, project)
	if f.Version != 1 {
		t.Errorf("version = %d", f.Version)
	}
	if got := commands(f, "stop"); len(got) != 1 || got[0] != "/p/sloprail/hooks/sr-session-hook-cursor.sh stop" {
		t.Errorf("stop = %v", got)
	}
	if got := commands(f, "sessionStart"); len(got) != 1 || !strings.HasSuffix(got[0], "sr-session-hook-cursor.sh start") {
		t.Errorf("sessionStart = %v", got)
	}
	if got := commands(f, "preCompact"); len(got) != 1 || !strings.HasSuffix(got[0], "sr-session-hook-cursor.sh post-tool") {
		t.Errorf("preCompact = %v", got)
	}
	if len(f.Hooks) != 3 {
		t.Errorf("only stop, sessionStart and preCompact belong in the project: %v", f.Hooks)
	}
}

func TestInstallProjectHooksKeepsTheUsersHooks(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(project+"/.cursor", 0o755); err != nil {
		t.Fatal(err)
	}
	existing := `{"version":1,"extra":{"k":1},"hooks":{
	  "stop":[{"command":"./mine.sh","timeout":5}],
	  "afterFileEdit":[{"command":"./fmt.sh"}]}}`
	if err := os.WriteFile(HooksPath(project), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := (Harness{}).InstallProjectHooks(project, "/p/sloprail"); err != nil {
		t.Fatal(err)
	}
	f := readHooks(t, project)
	if got := commands(f, "stop"); len(got) != 2 || got[0] != "./mine.sh" || !strings.HasSuffix(got[1], "cursor.sh stop") {
		t.Errorf("stop = %v", got)
	}
	if got := commands(f, "afterFileEdit"); len(got) != 1 || got[0] != "./fmt.sh" {
		t.Errorf("afterFileEdit = %v", got)
	}
	if f.Extra == nil {
		t.Error("an unrelated key of the file was dropped")
	}
	if f.Hooks["stop"][0]["timeout"].(float64) != 5 {
		t.Error("the user's entry changed")
	}
}

func TestInstallProjectHooksIsIdempotentAndFollowsAMovedPlugin(t *testing.T) {
	project := t.TempDir()
	h := Harness{}
	for i := 0; i < 3; i++ {
		if err := h.InstallProjectHooks(project, "/p/sloprail"); err != nil {
			t.Fatal(err)
		}
	}
	first, _ := os.ReadFile(HooksPath(project))
	if err := h.InstallProjectHooks(project, "/p/sloprail"); err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(HooksPath(project))
	if string(first) != string(again) {
		t.Errorf("a repeat changed the file:\n%s\n%s", first, again)
	}
	f := readHooks(t, project)
	if len(f.Hooks["stop"]) != 1 || len(f.Hooks["sessionStart"]) != 1 {
		t.Fatalf("repeated installs duplicated entries: %v", f.Hooks)
	}
	if err := h.InstallProjectHooks(project, "/q/sloprail"); err != nil {
		t.Fatal(err)
	}
	f = readHooks(t, project)
	if got := commands(f, "stop"); len(got) != 1 || !strings.HasPrefix(got[0], "/q/") {
		t.Errorf("a moved plugin left stale entries: %v", got)
	}
}

func TestInstallProjectHooksQuotesAPathWithSpaces(t *testing.T) {
	project := t.TempDir()
	if err := (Harness{}).InstallProjectHooks(project, "/p q/sloprail"); err != nil {
		t.Fatal(err)
	}
	if got := commands(readHooks(t, project), "stop"); got[0] != "'/p q/sloprail/hooks/sr-session-hook-cursor.sh' stop" {
		t.Errorf("stop = %v", got)
	}
}

func TestInstallProjectHooksRefusesAFileItCannotRead(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(project+"/.cursor", 0o755); err != nil {
		t.Fatal(err)
	}
	const broken = `{"hooks": [`
	if err := os.WriteFile(HooksPath(project), []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := (Harness{}).InstallProjectHooks(project, "/p/sloprail"); err == nil {
		t.Fatal("a hooks file that is not JSON was overwritten")
	}
	if got, _ := os.ReadFile(HooksPath(project)); string(got) != broken {
		t.Errorf("file changed: %s", got)
	}
}

func TestRemoveProjectHooksTakesOnlySloprailsOut(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(project+"/.cursor", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(HooksPath(project), []byte(`{"version":1,"hooks":{"stop":[{"command":"./mine.sh"}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	h := Harness{}
	if err := h.InstallProjectHooks(project, "/p/sloprail"); err != nil {
		t.Fatal(err)
	}
	if err := h.RemoveProjectHooks(project); err != nil {
		t.Fatal(err)
	}
	f := readHooks(t, project)
	if got := commands(f, "stop"); len(got) != 1 || got[0] != "./mine.sh" {
		t.Errorf("stop = %v", got)
	}
	if _, ok := f.Hooks["sessionStart"]; ok {
		t.Error("an empty sessionStart list was left behind")
	}
}

func TestRemoveProjectHooksDeletesAFileOnlySloprailWrote(t *testing.T) {
	project := t.TempDir()
	h := Harness{}
	if err := h.InstallProjectHooks(project, "/p/sloprail"); err != nil {
		t.Fatal(err)
	}
	if err := h.RemoveProjectHooks(project); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(HooksPath(project)); !os.IsNotExist(err) {
		t.Errorf("hooks file still there: %v", err)
	}
	if err := h.RemoveProjectHooks(project); err != nil {
		t.Errorf("removing from a project with no hooks file: %v", err)
	}
}
