package main

import "testing"

func TestSameRepoURL(t *testing.T) {
	same := [][2]string{
		{"https://github.com/o/r\n", "https://github.com/o/r.git"},
		{"https://x-access-token:abc@github.com/o/r.git", "https://github.com/O/R"},
		{"https://github.com/o/r/", "https://github.com/o/r"},
	}
	for _, c := range same {
		if !sameRepoURL(c[0], c[1]) {
			t.Errorf("%q and %q are one repository", c[0], c[1])
		}
	}
	if sameRepoURL("https://github.com/o/r", "https://github.com/fork/r") {
		t.Error("a fork is another repository")
	}
}
