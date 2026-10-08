package main

import (
	"strings"
	"testing"
)

func TestWithoutHostPaths_NothingPointsAtTheCheckout(t *testing.T) {
	roots := []string{"/Users/op/ws/sloprail", "/Users/op/ws/sloprail/sub"}
	env := []string{
		"PWD=/Users/op/ws/sloprail/sub", "OLDPWD=/somewhere", "_=/usr/bin/env",
		"GOFLAGS=-modfile=/Users/op/ws/sloprail/go.mod", "EDITOR=vim",
		"HOME=/ws/home", "TMPDIR=/ws/tmp",
	}
	got := strings.Join(withoutHostPaths(env, roots), " ")
	for _, bad := range []string{"PWD=", "OLDPWD", "_=", "GOFLAGS", "/Users/op"} {
		if strings.Contains(got, bad) {
			t.Errorf("%q survived: %s", bad, got)
		}
	}
	for _, keep := range []string{"EDITOR=vim", "HOME=/ws/home", "TMPDIR=/ws/tmp"} {
		if !strings.Contains(got, keep) {
			t.Errorf("%q was dropped: %s", keep, got)
		}
	}
	if got := cleanPath("/usr/bin:/Users/op/ws/sloprail/bin:/opt/x", roots); got != "/usr/bin:/opt/x" {
		t.Errorf("PATH keeps entries under the checkout: %s", got)
	}
}
