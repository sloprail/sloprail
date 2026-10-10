package harness

import (
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
)

// A regression test, not an invariant's proof: the start hook must get past its own
// announcement of the automatic install when stderr is a SOCKET, as it is for the hook
// of a session whose output is captured (a headless run, CI, an eval).
//
// The wrapper used to announce with `| tee /dev/stderr`, which opens the path and fails
// on a socket ("No such device or address"); under `set -e` it exited there, before
// install.sh ran, and the session went on unguarded. Here the release it is pointed at
// does not exist, so reaching "the automatic install failed" is the proof that the
// install was attempted at all: before the fix the wrapper exited 1 with only the
// announcement printed.
func TestSrSessionHookWrapper_StartSurvivesStderrBeingASocket(t *testing.T) {
	pair, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("socketpair: %v", err)
	}
	ours, theirs := os.NewFile(uintptr(pair[0]), "stderr-read"), os.NewFile(uintptr(pair[1]), "stderr-write")
	defer ours.Close()

	cmd := exec.Command(hookScriptPath(t), "start")
	cmd.Dir = noModuleDir(t)
	cmd.Env = append([]string{"PATH=/usr/bin:/bin", "HOME=" + hookHOME(t), "XDG_CONFIG_HOME=" + xdgConfigHomeOverride,
		"SLOPRAIL_RELEASE_URL=file://" + t.TempDir() + "/no-such-release"}, goEnvPassthrough...)
	cmd.Stdin = strings.NewReader("")
	cmd.Stderr = theirs
	var stderr strings.Builder
	drained := make(chan struct{})
	go func() { _, _ = io.Copy(&stderr, ours); close(drained) }()
	out, err := cmd.Output()
	theirs.Close()
	<-drained

	if err != nil {
		t.Fatalf("start exited with %v when stderr is a socket\nstdout: %s\nstderr: %s", err, out, stderr.String())
	}
	for _, want := range []string{"installing the sr binaries", "the automatic install failed"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("the agent was not told %q (stdout):\n%s", want, out)
		}
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("the person was not told %q (stderr):\n%s", want, stderr.String())
		}
	}
}
