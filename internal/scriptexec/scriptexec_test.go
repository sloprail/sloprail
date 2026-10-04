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

func TestArgv(t *testing.T) {
	dir := "/guard"
	ok := map[string][]string{
		"./a.sh":              {"/guard/a.sh"},
		"./staged.sh check":   {"/guard/staged.sh", "check"},
		"  ./a.sh   x  --k=v": {"/guard/a.sh", "x", "--k=v"},
		"/abs/a.sh 1,2 a@b":   {"/abs/a.sh", "1,2", "a@b"},
		"sr-checks verify":    {"sr-checks", "verify"},
		"sub/dir/a.sh":        {"/guard/sub/dir/a.sh"},
	}
	for in, want := range ok {
		got, err := Argv(dir, in)
		if err != nil || strings.Join(got, "\x00") != strings.Join(want, "\x00") {
			t.Errorf("Argv(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{
		"./a.sh && ./b.sh", "./x.sh | y", `"my dir/x.sh"`, "'x.sh'", "$HOME/x.sh", "./x.sh >out", "./x.sh *",
		"./a.sh; ./b.sh", "./a.sh `id`", "./a.sh $(id)", `./a\ b.sh`, "~/x.sh", "FOO=1 ./x.sh", "./a.sh # c", "", "   ",
	} {
		if _, err := Argv(dir, in); !errors.Is(err, ErrShellSyntax) {
			t.Errorf("Argv(%q): got %v, want ErrShellSyntax", in, err)
		}
	}
}

// Only the first word used to be checked, so a chain, a pipe or a quoted path ran unchecked under
// `sh -c`: the whole string must now be a path plus plain arguments, and the file must verify.
func TestVerifyDeclaredRefusesShellSyntax(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.sh"), []byte("#!/bin/sh\ntrue\n"), 0o755)
	os.WriteFile(filepath.Join(dir, "b.sh"), []byte("true\n"), 0o755) // no shebang
	for _, s := range []string{"./a.sh && ./b.sh", "./a.sh | ./b.sh", `"./a.sh"`, "./a.sh; ./b.sh"} {
		if err := VerifyDeclared(dir, s); !errors.Is(err, ErrShellSyntax) {
			t.Errorf("VerifyDeclared(%q) = %v, want ErrShellSyntax", s, err)
		}
	}
	if err := VerifyDeclared(dir, "./a.sh one two"); err != nil {
		t.Errorf("a path plus plain arguments: %v", err)
	}
}
