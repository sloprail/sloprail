package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// A login file is LINKED into the agent's HOME (linkAuthFiles) so a token the
// harness refreshes during the run lands in the operator's real file. That
// holds only while the harness writes the file in place. One that saves by
// writing a temporary file and renaming it over the path replaces the LINK
// with a regular file: the refreshed login then exists only in the sandbox,
// and when the refresh token rotates on use the operator's real file is left
// holding a dead one — their own sessions are logged out by an eval.
//
// keepAuthLinked closes that: while the run lasts it looks at each login file,
// and one that is no longer the link is written back to the real file and
// linked again. The returned function stops the watch after one last pass, so
// a refresh in the run's final moments is carried back too.
func keepAuthLinked(ctx context.Context, realHome, home string, names []string) (stop func()) {
	if len(names) == 0 {
		return func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				restoreAuthLinks(realHome, home, names)
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			wg.Wait()
			restoreAuthLinks(realHome, home, names)
		})
	}
}

// restoreAuthLinks carries back every login file the harness replaced; a
// failure is said on stderr and tried again on the next pass, never fatal to
// the run (the run is not what is at risk: the operator's login is).
func restoreAuthLinks(realHome, home string, names []string) {
	for _, name := range names {
		if err := restoreAuthLink(filepath.Join(realHome, name), filepath.Join(home, name)); err != nil {
			fmt.Fprintf(os.Stderr, "sr-eval: the login the agent refreshed could not be carried back to ~/%s: %v\n", name, err)
		}
	}
}

// restoreAuthLink does it for one file: when sandboxed is a regular file (the
// harness renamed a new one over the link), its content replaces real,
// atomically and readable by the owner alone, and sandboxed becomes the link
// again. A link still in place, or no file at all (the agent logged out), is
// left as it is.
func restoreAuthLink(real, sandboxed string) error {
	info, err := os.Lstat(sandboxed)
	if err != nil || !info.Mode().IsRegular() {
		return nil
	}
	content, err := os.ReadFile(sandboxed)
	if err != nil {
		return err
	}
	if len(content) == 0 {
		return nil // caught mid-write: the next pass sees the whole file
	}
	tmp, err := os.CreateTemp(filepath.Dir(real), ".sr-eval-auth-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), real); err != nil {
		return err
	}
	// Link under a temporary name and rename it over the file: the path is
	// never missing for a harness reading its login at that instant.
	link := sandboxed + ".sr-eval-link"
	_ = os.Remove(link)
	if err := os.Symlink(real, link); err != nil {
		return err
	}
	return os.Rename(link, sandboxed)
}
