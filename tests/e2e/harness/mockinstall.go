package harness

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
)

// The suite never skips for a missing mock agent: when the pinned mock is not in
// .bin/, it installs it there itself — what `make mock` does — and fails with the
// reason if that cannot be done.

// mockInstalls memoises the install per (repo root, harness): one attempt per test
// binary, its outcome (nil or the failure) kept for every later test.
var mockInstalls sync.Map // key string -> *mockInstall

type mockInstall struct {
	once sync.Once
	err  error
}

// pinnedMock names what `make mock` installs for one harness.
type pinnedMock struct {
	name        string // claude | codex | cursor
	versionFile string // under tests/e2e/harness
	overrideEnv string // an own mock build, which needs no install
}

var pinnedMocks = map[string]pinnedMock{
	"claude": {"claude", "MOCK_VERSION", "A10N_CLAUDE_MOCK"},
	"codex":  {"codex", "CODEX_MOCK_VERSION", "A10N_CODEX_MOCK"},
	"cursor": {"cursor", "CURSOR_MOCK_VERSION", "A10N_CURSOR_MOCK"},
}

// EnsurePinnedMock makes sure the mock agent pinned for the current harness is
// installed in the repo's .bin/ at its pinned version (installing it when it is
// not) and fails the test with the reason when it cannot. A mock named by the
// harness's A10N_*_MOCK variable is the caller's own build and is left alone.
func EnsurePinnedMock(t testing.TB) {
	t.Helper()
	drv, err := selectDriver()
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureMock(topLevel(t), drv.Name()); err != nil {
		t.Fatalf("harness: the pinned %s mock agent is not installed and installing it failed: %v", drv.Name(), err)
	}
}

func topLevel(t testing.TB) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("harness: locate repo root: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// ensureMock installs the pinned mock once per test binary.
func ensureMock(root, harness string) error {
	spec, ok := pinnedMocks[harness]
	if !ok {
		return fmt.Errorf("no pinned mock for harness %q", harness)
	}
	if os.Getenv(spec.overrideEnv) != "" {
		return nil
	}
	v, _ := mockInstalls.LoadOrStore(root+"\x00"+harness, &mockInstall{})
	m := v.(*mockInstall)
	m.once.Do(func() { m.err = installPinnedMock(root, spec) })
	return m.err
}

// installPinnedMock is `make mock` for one harness, serialised across the test
// binaries of concurrently running packages by a lock file in .bin/.
func installPinnedMock(root string, spec pinnedMock) error {
	raw, err := os.ReadFile(filepath.Join(root, "tests", "e2e", "harness", spec.versionFile))
	if err != nil {
		return fmt.Errorf("pinned version unreadable: %w", err)
	}
	version := strings.TrimSpace(string(raw))
	bin := filepath.Join(root, ".bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(bin, ".mock-install.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock .bin: %w", err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	target := filepath.Join(bin, "a10n-"+spec.name+"-mock")
	stamp := target + "." + version
	if fileExists(target) && fileExists(stamp) {
		return nil // installed already, or by another package while this one waited
	}
	cmd := exec.Command("go", "install", "github.com/sloprail/harness-mocks/"+spec.name+"-mock@"+version)
	cmd.Env = append(os.Environ(), "GOBIN="+bin)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("go install %s-mock@%s: %v\n%s", spec.name, version, err, out)
	}
	if err := os.Rename(filepath.Join(bin, spec.name+"-mock"), target); err != nil {
		return err
	}
	old, _ := filepath.Glob(target + ".v*")
	for _, p := range old {
		os.Remove(p)
	}
	return os.WriteFile(stamp, nil, 0o644)
}
