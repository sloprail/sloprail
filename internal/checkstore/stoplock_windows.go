//go:build windows

package checkstore

// No advisory locks here: the RUNNING-row check alone decides whether to postpone.

type lockState int

const (
	lockAcquired lockState = iota
	lockHeld
	lockUnavailable
)

func processGone(pid int) bool { return false }

func lockShared(path string) func() { return func() {} }

func tryExclusive(path string) (func(), lockState) { return func() {}, lockUnavailable }
