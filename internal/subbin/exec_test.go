package subbin_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sloprail/sloprail/internal/subbin"
)

// Exec replaces the calling process's exit status with the child's, so it
// cannot be called in-process by a test — it would exit the test binary. These
// tests therefore build a tiny proxy that calls Exec, and run IT.
//
// That is also the honest way to test it: the properties worth pinning (the
// exact exit code, streams reaching the caller unbuffered) are properties of a
// real process boundary, and a test that stubbed the boundary would be
// asserting about the stub.

// buildProxy compiles the real `sr` binary into dir and returns its path.
//
// The actual proxy rather than a stand-in written here: a stand-in would let
// this pass while `sr` did something else, and the point is to pin what a user
// runs. It dispatches `sr file ...` to sr-file, so these tests name their child
// binaries after real services and pass the matching word.
func buildProxy(t *testing.T, dir string) string {
	t.Helper()
	out := filepath.Join(dir, "sr")
	cmd := exec.Command("go", "build", "-o", out, "./services/sr")
	cmd.Dir = moduleDir(t)
	if o, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build proxy: %v\n%s", err, o)
	}
	return out
}

func moduleDir(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
	require.NoError(t, err)
	return strings.TrimSpace(string(out))
}

// buildChild compiles a child that echoes stdin, writes to both streams and
// exits with the code named by its first argument.
func buildChild(t *testing.T, dir, name string) {
	t.Helper()
	src := filepath.Join(t.TempDir(), "child.go")
	require.NoError(t, os.WriteFile(src, []byte(`package main

import (
	"io"
	"os"
	"strconv"
)

func main() {
	code, _ := strconv.Atoi(os.Args[1])
	b, _ := io.ReadAll(os.Stdin)
	os.Stdout.WriteString("out:" + string(b))
	os.Stderr.WriteString("err:" + string(b))
	os.Exit(code)
}
`), 0o644))
	cmd := exec.Command("go", "build", "-o", filepath.Join(dir, name), src)
	cmd.Dir = moduleDir(t)
	if o, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build child: %v\n%s", err, o)
	}
}

// TestExecPreservesExitCode is the property the whole CLI rests on. sloprail's
// verdict IS the exit status, so a proxy that returned an error instead of the
// child's code would turn every distinct status into 1 and silently change what
// a hook reports. 2 in particular is a refusal.
func TestExecPreservesExitCode(t *testing.T) {
	dir := t.TempDir()
	proxy := buildProxy(t, dir)
	buildChild(t, dir, "sr-file")
	t.Setenv(subbin.EnvDir, dir)

	for _, code := range []int{0, 1, 2, 3, 42} {
		cmd := exec.Command(proxy, "file", itoa(code))
		cmd.Env = append(os.Environ(), subbin.EnvDir+"="+dir)
		err := cmd.Run()

		got := 0
		if exitErr, ok := err.(*exec.ExitError); ok {
			got = exitErr.ExitCode()
		} else {
			require.NoError(t, err)
		}
		assert.Equal(t, code, got, "the proxy must exit with the child's exact status")
	}
}

// TestExecPassesStreamsThrough: stdin reaches the child, and both of the
// child's streams reach the caller unaltered.
func TestExecPassesStreamsThrough(t *testing.T) {
	dir := t.TempDir()
	proxy := buildProxy(t, dir)
	buildChild(t, dir, "sr-file")

	cmd := exec.Command(proxy, "file", "0")
	cmd.Env = append(os.Environ(), subbin.EnvDir+"="+dir)
	cmd.Stdin = strings.NewReader("payload")

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Run())

	assert.Equal(t, "out:payload", stdout.String(), "stdin must reach the child and its stdout must come back")
	assert.Equal(t, "err:payload", stderr.String(), "stderr must stay on stderr, not be merged into stdout")
}

// TestExecMissingBinaryIsAnError, rather than a silent success — a half-present
// install must say which binary it could not find.
//
// `file` is a command the proxy knows, with no sr-file built anywhere it looks:
// that is the case where dispatch is reached and resolution fails, which is the
// one worth pinning. A word the proxy does NOT know is a different failure,
// covered by TestUnknownCommandIsNotDispatched.
func TestExecMissingBinaryIsAnError(t *testing.T) {
	dir := t.TempDir()
	proxy := buildProxy(t, dir)

	cmd := exec.Command(proxy, "file")
	cmd.Env = append(os.Environ(), subbin.EnvDir+"="+dir, "PATH=")
	out, err := cmd.CombinedOutput()

	require.Error(t, err)
	assert.Contains(t, string(out), "sr-file", "the error must name the binary that is missing")
}

// TestUnknownCommandIsNotDispatched: `sr sessoin` is a typo, and the proxy must
// answer for it rather than trying to exec `sr-sessoin`. Forwarding anything
// would turn a misspelling into an obscure not-found naming a binary that was
// never meant to exist.
func TestUnknownCommandIsNotDispatched(t *testing.T) {
	dir := t.TempDir()
	proxy := buildProxy(t, dir)

	cmd := exec.Command(proxy, "sessoin")
	cmd.Env = append(os.Environ(), subbin.EnvDir+"="+dir, "PATH=")
	out, err := cmd.CombinedOutput()

	require.Error(t, err)
	assert.Contains(t, string(out), `unknown command "sessoin"`)
	assert.NotContains(t, string(out), "sr-sessoin", "a typo must not become an exec attempt")
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
