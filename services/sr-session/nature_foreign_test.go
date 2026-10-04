package main

import (
	"encoding/json"
	"reflect"
	"testing"
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
