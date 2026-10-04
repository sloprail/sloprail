package scriptexec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, body string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "x.sh")
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestVerify(t *testing.T) {
	cases := []struct {
		name string
		body string
		mode os.FileMode
		want error
	}{
		{"env bash", "#!/usr/bin/env bash\ntrue\n", 0o755, nil},
		{"bin sh", "#!/bin/sh\ntrue\n", 0o755, nil},
		{"bin bash", "#!/bin/bash\ntrue\n", 0o755, nil},
		{"no shebang", "true\n", 0o755, ErrNoShebang},
		{"not executable", "#!/bin/sh\ntrue\n", 0o644, ErrNotExecutable},
		{"local bash", "#!/usr/local/bin/bash\ntrue\n", 0o755, ErrBadInterpreter},
		{"relative interpreter", "#!bash\ntrue\n", 0o755, ErrBadInterpreter},
		{"bare env", "#!/usr/bin/env\ntrue\n", 0o755, ErrBadInterpreter},
		{"empty shebang", "#!\ntrue\n", 0o755, ErrNoShebang},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := Verify(write(t, c.body, c.mode))
			if !errors.Is(err, c.want) || (c.want == nil && err != nil) {
				t.Fatalf("got %v, want %v", err, c.want)
			}
		})
	}
}

func TestVerifyErrorNamesFileAndFix(t *testing.T) {
	err := Verify(write(t, "true\n", 0o755))
	if err == nil || !strings.Contains(err.Error(), "x.sh") || !strings.Contains(err.Error(), "#!/usr/bin/env bash") {
		t.Fatalf("error should name the file and the fix: %v", err)
	}
	err = Verify(write(t, "#!/bin/sh\n", 0o644))
	if err == nil || !strings.Contains(err.Error(), "chmod +x") {
		t.Fatalf("error should name chmod +x: %v", err)
	}
}

func TestVerifyDeclared(t *testing.T) {
	dir := t.TempDir()
	if err := VerifyDeclared(dir, "./missing.sh"); err != nil {
		t.Fatalf("a missing file is left to the runner: %v", err)
	}
	if err := VerifyDeclared(dir, ""); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "a.sh"), []byte("true\n"), 0o755)
	if err := VerifyDeclared(dir, "./a.sh arg one"); !errors.Is(err, ErrNoShebang) {
		t.Fatalf("args must not hide the file: %v", err)
	}
}

func TestCommandRunsDirectly(t *testing.T) {
	p := write(t, "#!/bin/sh\nexit 3\n", 0o755)
	cmd, err := Command(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Run(); err == nil || !strings.Contains(err.Error(), "exit status 3") {
		t.Fatalf("expected exit 3, got %v", err)
	}
	if _, err := Command(context.Background(), write(t, "exit 0\n", 0o755)); !errors.Is(err, ErrNoShebang) {
		t.Fatalf("got %v", err)
	}
}
