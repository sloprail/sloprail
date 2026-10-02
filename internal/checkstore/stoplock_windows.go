//go:build windows

package checkstore

// No advisory locks here: the RUNNING-row check alone decides whether to postpone.

func lockShared(path string) func() { return func() {} }

func tryExclusive(path string) (func(), bool) { return func() {}, true }
