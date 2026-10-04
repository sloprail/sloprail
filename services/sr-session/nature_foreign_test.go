package main

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sloprail/sloprail/internal/event"
)

func bashPayload(t *testing.T, cwd, command string) HookPayload {
	t.Helper()
	in, err := json.Marshal(map[string]string{"command": command})
	if err != nil {
		t.Fatal(err)
	}
	return HookPayload{ToolName: "Bash", Cwd: cwd, ToolInput: in}
}

func TestCommandTargetDirs(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    []string
	}{
		{"own folder", "ls", []string{"/a/sub"}},
		{"git -C", "git -C /b commit -m x", []string{"/b"}},
		{"cd then command", "cd /b && make", []string{"/a/sub", "/b"}},
		{"relative cd", "cd ../b && make", []string{"/a/sub", "/a/b"}},
		{"both, in order", "git -C /b status && make", []string{"/b", "/a/sub"}},
		{"unknowable cd is skipped", "cd $X && make", []string{"/a/sub"}},
	}
	for _, c := range cases {
		got := commandTargetDirs(bashPayload(t, "/a/sub", c.command))
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestCommandTargetDirsOnlyForBash(t *testing.T) {
	if got := commandTargetDirs(HookPayload{ToolName: "Write", Cwd: "/a"}); got != nil {
		t.Errorf("a Write has no command targets, got %v", got)
	}
}

func TestCommandEventForKeepsOnlyInvocationsInTheRepo(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	for _, d := range []string{a, b} {
		if out, err := exec.Command("git", "-C", d, "init", "-q").CombinedOutput(); err != nil {
			t.Fatalf("git init: %v\n%s", err, out)
		}
	}
	root, err := filepath.EvalSymlinks(b)
	if err != nil {
		t.Fatal(err)
	}
	inv := func(bin, cwd string, argv ...string) any {
		args := make([]any, len(argv))
		for i, s := range argv {
			args[i] = s
		}
		return map[string]any{"bin": bin, "cwd": cwd, "argv": args}
	}
	e := event.Event{Kind: "PreCommandInvoke", Fields: map[string]any{
		"raw": "rm -rf build; git -C " + b + " status",
		"invocations": []any{
			inv("rm", ".", "rm", "-rf", "build"),
			inv("git", ".", "git", "-C", b, "status"),
		},
	}}
	got, ok := commandEventFor(e, HookPayload{Cwd: a}, root, owningRepo)
	if !ok {
		t.Fatal("the event with an invocation in the repo was dropped")
	}
	if n := len(got.Fields["invocations"].([]any)); n != 1 {
		t.Errorf("kept %d invocations, want 1", n)
	}
	if raw := got.Fields["raw"].(string); strings.Contains(raw, "rm -rf") {
		t.Errorf("raw still carries the other repo's command: %q", raw)
	}
	if _, ok := commandEventFor(e, HookPayload{Cwd: a}, "/nowhere", owningRepo); ok {
		t.Error("an event with no invocation in the repo was kept")
	}
}
