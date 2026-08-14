package subbin

import (
	"context"
	"os"
	"os/exec"
	"os/signal"
)

// Exec finds a co-installed service binary and runs it in this process's place,
// as nearly as a parent process can. Stdin, stdout and stderr are inherited, so
// the child reads the harness's payload and writes the harness's reply with
// nothing in between to buffer, reorder, or reinterpret them.
//
// It does not return on success or on a non-zero child exit: it calls os.Exit
// with the child's status. Callers should treat it as terminal.
//
// EXIT CODE PRESERVATION IS THE WHOLE POINT. sloprail's contract with a harness
// is carried in the exit status — a refusal IS a non-zero exit, and a hook that
// returns 1 instead of 2 has changed the verdict rather than reported it. Cobra
// converts any returned error into exit code 1, so a proxy that returned the
// child's error would flatten every distinct status into "something failed".
// Calling os.Exit with the child's exact code is what makes `sr session pre-tool`
// and `sr-session pre-tool` the same command as far as the harness can tell.
//
// Signals are relayed for the life of the child. A hook that is killed on a
// timeout must have the kill reach the process actually doing the work; without
// relaying, a SIGTERM would kill the proxy alone and orphan the child, which
// holds the inherited pipes open and hangs whatever waits on the proxy.
func Exec(ctx context.Context, name string, args ...string) error {
	bin, err := Find(name)
	if err != nil {
		return err
	}

	c := exec.CommandContext(ctx, bin, args...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr

	if err := c.Start(); err != nil {
		return err
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case s := <-sigCh:
				if c.Process != nil {
					_ = c.Process.Signal(s)
				}
			case <-done:
				return
			}
		}
	}()

	runErr := c.Wait()
	close(done)
	signal.Stop(sigCh)

	if runErr == nil {
		os.Exit(0)
	}
	if exitErr, ok := runErr.(*exec.ExitError); ok {
		os.Exit(exitErr.ExitCode())
	}
	return runErr
}
