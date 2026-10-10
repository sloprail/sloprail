package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// argvRole runs main's own startup (settleArgv) as `sr-eval run --fixture <leakFixture>`.
// First pass: it must re-execute. Second pass (re-executed): settleArgv must return, not
// re-execute again; then report what `ps` showed for this very process before, and what the
// program itself sees after.
func argvRole() {
	out := os.Getenv("SRE_TEST_ARGV_OUT")
	if len(os.Args) == 3 && strings.HasPrefix(os.Args[2], argsFilePrefix) {
		ps, _ := exec.Command("ps", "-o", "args=", "-p", itoa(os.Getpid())).Output()
		settleArgv()
		b, _ := json.Marshal(map[string]any{"ps": string(ps), "args": os.Args})
		_ = os.WriteFile(out, b, 0o644)
		os.Exit(0)
	}
	os.Args = []string{os.Args[0], "run", "--fixture", leakFixture, "--harness", "cursor"}
	settleArgv()
	os.Exit(3) // settleArgv returned: it did not re-execute
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
	// A re-execution loop never returns: bound it.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, self)
	cmd.Env = append(os.Environ(), roleEnv+"=argv", "SRE_TEST_ARGV_OUT="+out, "TMPDIR="+tmp)
	if b, err := cmd.CombinedOutput(); err != nil {
		if ctx.Err() != nil {
			t.Fatalf("sr-eval run re-executes itself forever: %s", b)
		}
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

// The agent commits as itself and unsigned, whatever the operator's config says, and with no
// operator config at all: a commit in the agent's HOME must not need the operator's signing key.
func TestAgentGitConfig_OwnIdentityUnsigned(t *testing.T) {
	for name, cfg := range map[string]string{
		"operator signs": "[user]\n\tname = Op\n\temail = op@example.com\n\tsigningkey = ABCDEF\n[commit]\n\tgpgsign = true\n[tag]\n\tgpgsign = true\n[alias]\n\tst = status",
		"no config":      "",
	} {
		t.Run(name, func(t *testing.T) {
			realHome, home := t.TempDir(), t.TempDir()
			src := filepath.Join(realHome, ".gitconfig")
			if cfg != "" {
				if err := os.WriteFile(src, []byte(cfg), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			dst := filepath.Join(home, ".gitconfig")
			if err := copyGlobalConfig(src, dst, []string{realHome}); err != nil {
				t.Fatal(err)
			}
			get := func(key string) string {
				c := exec.Command("git", "config", "--file", dst, "--get", key)
				out, _ := c.Output()
				return strings.TrimSpace(string(out))
			}
			for key, want := range map[string]string{
				"user.name": "sr-eval agent", "user.email": "agent@sr-eval.invalid",
				"commit.gpgsign": "false", "tag.gpgsign": "false",
			} {
				if got := get(key); got != want {
					t.Errorf("%s = %q, want %q", key, got, want)
				}
			}
			if cfg != "" && get("alias.st") != "status" {
				t.Errorf("the operator's alias was not kept")
			}
		})
	}
}
