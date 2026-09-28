package commandmod

import "testing"

func TestBackgrounds(t *testing.T) {
	for _, src := range []string{
		`sleep 4 &`,
		`nohup sh -c 'sleep 4; printf x > a.md' >/dev/null 2>&1 &`,
		`nohup ./job.sh`,
		`setsid ./job.sh`,
		`./job.sh & disown`,
		`echo 'printf x > a.md' | at now + 1 minute`,
		`crontab jobs.txt`,
		`bash -c 'sleep 4 & echo started'`,
		`coproc cat`,
		`tmux new -d 'sleep 9'`,
	} {
		if !Backgrounds(src) {
			t.Errorf("Backgrounds(%q) = false, want true", src)
		}
	}
	for _, src := range []string{
		`make test && echo ok`,
		`go test ./... 2>&1 | tail`,
		`sr-file write a.md --content 'x' >/dev/null 2>&1`,
		`cat a &> b`,
	} {
		if Backgrounds(src) {
			t.Errorf("Backgrounds(%q) = true, want false", src)
		}
	}
}
