package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A real Cursor run learned the fixture's host path, then the sr-eval checkout's, from inside
// the agent: it ran `ps -ef | grep sr-eval` and read `sr-eval run --fixture <path>`. These
// tests pin every channel sr-eval itself controls: the process table, the environment, and
// the files written into the agent's HOME. (What no scrub reaches: the machine's shared
// /private/tmp, and the operator's own shell line in the process table.)

const (
	leakFixture  = "/private/tmp/LEAKCANARY/fixture/eval"
	leakCheckout = "/private/tmp/LEAKCANARY/checkout"
)

// argvRole: first pass, behave as `sr-eval run --fixture <leakFixture>` and hide the argv;
// second pass (re-executed), report what `ps` shows for this very process, then what the
// program itself sees.
func argvRole() {
	out := os.Getenv("SRE_TEST_ARGV_OUT")
	if len(os.Args) == 3 && strings.HasPrefix(os.Args[2], argsFilePrefix) {
		ps, _ := exec.Command("ps", "-o", "args=", "-p", itoa(os.Getpid())).Output()
		restoreArgv()
		b, _ := json.Marshal(map[string]any{"ps": string(ps), "args": os.Args})
		_ = os.WriteFile(out, b, 0o644)
		os.Exit(0)
	}
	os.Args = []string{os.Args[0], "run", "--fixture", leakFixture, "--harness", "cursor"}
	hideArgv()
	os.Exit(3) // hideArgv returned: it did not re-execute
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func TestHideArgv_ProcessTableCarriesNoHostPath(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Skip(err)
	}
	tmp := t.TempDir()
	out := filepath.Join(t.TempDir(), "argv.json")
	cmd := exec.Command(self)
	cmd.Env = append(os.Environ(), roleEnv+"=argv", "SRE_TEST_ARGV_OUT="+out, "TMPDIR="+tmp)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("argv role: %v: %s", err, b)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		PS   string
		Args []string
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got.PS, "LEAKCANARY") || strings.Contains(got.PS, "--fixture") {
		t.Errorf("the process table still shows the arguments: %q", got.PS)
	}
	want := []string{"run", "--fixture", leakFixture, "--harness", "cursor"}
	if len(got.Args) != 1+len(want) || strings.Join(got.Args[1:], " ") != strings.Join(want, " ") {
		t.Errorf("the program lost its real arguments: %q", got.Args)
	}
	// The stash is deleted once read.
	if left, _ := filepath.Glob(filepath.Join(tmp, "sr-eval-args-*")); len(left) > 0 {
		t.Errorf("args file left behind: %v", left)
	}
}

// The agent's whole environment, and every file sr-eval writes into its HOME, name neither the
// fixture's directory nor the checkout.
func TestAgentSurface_NamesNoHostPath(t *testing.T) {
	realHome := t.TempDir()
	cfg := "[user]\n\tname = Op\n[core]\n\thooksPath = " + realHome + "/.config/git/hooks\n" +
		"\texcludesfile = " + realHome + "/.gitignore_global\n" +
		"[safe]\n\tdirectory = " + leakCheckout + "\n\tdirectory = " + leakFixture + "\n" +
		"[credential]\n\thelper = !/opt/homebrew/bin/gh auth git-credential\n"
	src := filepath.Join(realHome, ".gitconfig")
	if err := os.WriteFile(src, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(t.TempDir(), "home")
	roots := append([]string{leakFixture, leakCheckout}, realHome)
	if err := copyGlobalConfig(src, filepath.Join(home, ".gitconfig"), roots); err != nil {
		t.Fatal(err)
	}

	environ := []string{
		"HOME=" + realHome, "PWD=" + leakCheckout, "OLDPWD=" + leakFixture, "_=" + leakCheckout + "/bin/sr-eval",
		"PATH=/usr/bin:" + leakCheckout + "/bin", "SR_EVAL_FIXTURE_DIR=" + leakFixture,
		"GOFLAGS=-modfile=" + leakCheckout + "/go.mod", "EDITOR=vim",
	}
	env := baseAgentEnv(testHarness(t, "cursor"), environ, home, filepath.Join(home, "tmp"), false)
	env = withoutHostPaths(env, roots)

	needles := append(roots, "LEAKCANARY")
	for _, kv := range env {
		for _, n := range needles {
			if strings.Contains(kv, n) {
				t.Errorf("agent env names a host path %q: %s", n, kv)
			}
		}
	}
	err := filepath.Walk(home, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		body, _ := os.ReadFile(p)
		for _, n := range needles {
			if strings.Contains(string(body), n) {
				t.Errorf("%s names a host path %q", p, n)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	kept, _ := os.ReadFile(filepath.Join(home, ".gitconfig"))
	for _, want := range []string{"name = Op", "gh auth git-credential"} {
		if !strings.Contains(string(kept), want) {
			t.Errorf("the config lost %q:\n%s", want, kept)
		}
	}
}
