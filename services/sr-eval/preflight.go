package main

import (
	"fmt"
	"os"
	"strconv"
	"syscall"
)

// defaultMinFreeMB is the free space a run needs before it starts: the fresh
// build of every sloprail binary, the workspace's HOME, the agent's transcripts
// and the archive all land on the temp volume. A run that starts below it dies
// somewhere inside the build or the agent with ENOSPC — measured with EMPTY
// stdout and stderr, because the failure also swallowed the report of itself.
const defaultMinFreeMB = 3072

// minFreeMBEnv overrides defaultMinFreeMB (a whole number of MiB; 0 disables).
const minFreeMBEnv = "SR_EVAL_MIN_FREE_MB"

// freeBytes is the space available to an unprivileged writer under dir.
func freeBytes(dir string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}

// minFreeBytes is the threshold in force: the env override when it is a valid
// number, else the default.
func minFreeBytes(getenv func(string) string) uint64 {
	mb := uint64(defaultMinFreeMB)
	if v := getenv(minFreeMBEnv); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			mb = n
		}
	}
	return mb << 20
}

// checkFreeSpace refuses a run whose temp volume has less than min bytes free.
// A min of 0 disables the check. Failing to measure is itself a failure: a
// volume that cannot even be stat'ed is not one to start a run on.
func checkFreeSpace(dir string, min uint64, free func(string) (uint64, error)) error {
	if min == 0 {
		return nil
	}
	got, err := free(dir)
	if err != nil {
		return fmt.Errorf("could not measure free disk space under %s: %w", dir, err)
	}
	if got < min {
		return fmt.Errorf("not enough free disk space under %s: %d MiB free, need at least %d MiB (free some space, or set %s to lower the bar)",
			dir, got>>20, min>>20, minFreeMBEnv)
	}
	return nil
}

// preflight is every check that must pass before a run is worth starting.
func preflight() error {
	return checkFreeSpace(os.TempDir(), minFreeBytes(os.Getenv), freeBytes)
}
