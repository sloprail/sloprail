package commandmod

import "testing"

func TestEchoesRecord(t *testing.T) {
	for _, cmd := range []string{
		"git log -1", "sh -c 'git log'", `bash -c "git show HEAD"`, "/usr/bin/git log", `git -C "a b" log`,
		"env FOO=1 sh -c 'cd x && git log'", "git for-each-ref --format=%(contents)", "git tag -n99", "git stash list",
		"cat .git/COMMIT_EDITMSG", "cat .git/logs/HEAD", "cd x && git reflog", "git --no-pager -c a=b log",
		"sr trajectory", "sr-mark list", "sr-checks run", "sr-agent x", "sr-session trajectory cite 'q'", "sr-file write a",
		"/opt/bin/sr-session state list", "echo hi | sr-file write a", "sh -c 'cat < .git/COMMIT_EDITMSG'",
	} {
		if !EchoesRecord(cmd) {
			t.Errorf("%q is an echo", cmd)
		}
	}
	for _, cmd := range []string{
		"echo 'tests passed'", "go test ./...", "git status", "git diff --stat", "git add -A && git commit -m x",
		"cat README.md", "make build", "ls src", "echo 'git log'", "sh -c 'echo ok'", "git -C dir status",
	} {
		if EchoesRecord(cmd) {
			t.Errorf("%q is not an echo", cmd)
		}
	}
}
