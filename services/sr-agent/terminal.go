package main

import (
	"io"
	"os"
)

// isTerminal reports whether r is a terminal (a character device file).
func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
