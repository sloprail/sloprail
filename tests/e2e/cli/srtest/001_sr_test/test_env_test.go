package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runCasesEnv is runCases with extra variables set on the sr-test process (the "caller").
func runCasesEnv(t *testing.T, root string, env []string, args ...string) map[string]result {
	t.Helper()
	e := New(t)
	res := e.CLIDirectEnv(root, env, "sr-test", append([]string{"run"}, args...)...)
	return results(t, res.Output)
}

// TestSrTestCaseEnvIsAnAllowlist: what the caller has set (GIT_DIR, GIT_CONFIG_GLOBAL, GIT_CONFIG_SYSTEM,
// XDG_CONFIG_HOME, its own TMPDIR, a stray FOO, an extra PATH entry) does not reach a case, and git in the
// case ignores the caller's global and system configuration.
// sr:proves authoring-tools/test-case-environment-is-hermetic
func TestSrTestCaseEnvIsAnAllowlist(t *testing.T) {
	callerDir := t.TempDir()
	extraBin := filepath.Join(callerDir, "extra-bin")
	callerTmp := filepath.Join(callerDir, "tmp")
	gitconfig := filepath.Join(callerDir, "gitconfig")
	xdg := filepath.Join(callerDir, "xdg")
	for _, d := range []string{extraBin, callerTmp, filepath.Join(xdg, "git")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	bogus := "[alias]\n\tbogus = status\n[core]\n\thooksPath = " + filepath.Join(callerDir, "hooks") + "\n"
	for _, p := range []string{gitconfig, filepath.Join(xdg, "git", "config")} {
		if err := os.WriteFile(p, []byte(bogus), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	root := t.TempDir()
	out := t.TempDir()
	tcase(t, root, "sees-nothing", `env | sort > "`+filepath.Join(out, "env")+`"
test -z "$(git config --get alias.bogus)" || { echo "the caller's git alias is visible"; exit 1; }
test -z "$(git config --get core.hookspath)" || { echo "the caller's hooksPath is visible"; exit 1; }
git commit -q --allow-empty -m probe`)
	got := runCasesEnv(t, root, []string{
		"GIT_DIR=" + filepath.Join(callerDir, "no-such.git"),
		"GIT_CONFIG_GLOBAL=" + gitconfig,
		"GIT_CONFIG_SYSTEM=" + gitconfig,
		"XDG_CONFIG_HOME=" + xdg,
		"TMPDIR=" + callerTmp,
		"FOO=bar",
		"PATH=" + extraBin + string(os.PathListSeparator) + os.Getenv("PATH"),
	})
	want(t, got, "sees-nothing", "pass")

	seen := map[string]string{}
	for _, kv := range strings.Split(readTrim(t, filepath.Join(out, "env")), "\n") {
		k, v, _ := strings.Cut(kv, "=")
		seen[k] = v
	}
	for _, k := range []string{"FOO", "GIT_DIR", "XDG_CONFIG_HOME", "GIT_CONFIG_SYSTEM"} {
		if v, ok := seen[k]; ok {
			t.Errorf("the case sees the caller's %s=%s", k, v)
		}
	}
	if strings.Contains(seen["PATH"], extraBin) {
		t.Errorf("the case PATH %q carries the caller's extra entry", seen["PATH"])
	}
	if g := seen["GIT_CONFIG_GLOBAL"]; g == gitconfig || g == "" {
		t.Errorf("GIT_CONFIG_GLOBAL=%q, want the case's own file", g)
	}
	if seen["GIT_CONFIG_NOSYSTEM"] != "1" {
		t.Errorf("GIT_CONFIG_NOSYSTEM=%q", seen["GIT_CONFIG_NOSYSTEM"])
	}
	if seen["TMPDIR"] == callerTmp || seen["TMPDIR"] == "" {
		t.Errorf("the case TMPDIR %q is the caller's", seen["TMPDIR"])
	}
}

// TestSrTestTmpdirIsPerCase: each case has its own TMPDIR, inside its own temp folder (so it goes with it).
func TestSrTestTmpdirIsPerCase(t *testing.T) {
	root := t.TempDir()
	out := t.TempDir()
	for _, n := range []string{"a", "b"} {
		tcase(t, root, n, `test -d "$TMPDIR" || { echo "TMPDIR is not a directory"; exit 1; }
echo "$(cd "$TMPDIR/.." && pwd -P)|$(cd .. && pwd -P)|$(basename "$TMPDIR")|$TMPDIR" > "`+filepath.Join(out, n)+`"`)
	}
	got := runCasesEnv(t, root, []string{"TMPDIR=" + t.TempDir()})
	seen := map[string]bool{}
	for _, n := range []string{"a", "b"} {
		want(t, got, n, "pass")
		parts := strings.Split(readTrim(t, filepath.Join(out, n)), "|")
		if len(parts) != 4 || parts[0] != parts[1] || parts[2] != "tmp" {
			t.Errorf("%s: TMPDIR parent|temp folder|name|TMPDIR = %v, want <temp folder>/tmp", n, parts)
			continue
		}
		if seen[parts[3]] {
			t.Errorf("two cases share TMPDIR %s", parts[3])
		}
		seen[parts[3]] = true
	}
}

// TestSrTestCredentialsDoNotReachACase: a judge is always a mock script, so the caller's credentials and
// network settings are not passed to a case (nor is anything else not on the allowlist).
// sr:proves authoring-tools/test-case-environment-is-hermetic
func TestSrTestCredentialsDoNotReachACase(t *testing.T) {
	probe := `test -z "${ANTHROPIC_API_KEY-}" || { echo "ANTHROPIC_API_KEY leaked"; exit 1; }
test -z "${HTTPS_PROXY-}" || { echo "HTTPS_PROXY leaked"; exit 1; }
test -z "${FOO-}" || { echo "FOO leaked"; exit 1; }`
	caller := []string{"ANTHROPIC_API_KEY=k-123", "HTTPS_PROXY=http://proxy.invalid:1", "FOO=bar"}
	root := t.TempDir()
	tcase(t, root, "mocked", probe)
	want(t, runCasesEnv(t, root, caller), "mocked", "pass")
}
