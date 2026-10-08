//go:build !unix

package main

func hideArgv()         {}
func restoreArgv() bool { return false }
