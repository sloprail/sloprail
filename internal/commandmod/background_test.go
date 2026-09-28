package commandmod

import (
	"strings"
	"testing"
)

func TestDetaches(t *testing.T) {
	for src, names := range map[string]string{
		`sleep 4 &`: "sleep 4",
		`nohup sh -c 'sleep 4; printf x > a.md' >/dev/null 2>&1 &`: "nohup",
		`nohup ./job.sh`:    "nohup",
		`setsid ./job.sh`:   "setsid",
		`./job.sh & disown`: "disown",
		`echo 'printf x > a.md' | at now + 1 minute`: "at",
		`crontab jobs.txt`:                           "crontab",
		`bash -c 'sleep 4 & echo started'`:           "sleep 4",
		`sh -c "nohup ./job.sh"`:                     "nohup",
		`coproc cat`:                                 "coproc",
		`tmux new -d 'sleep 9'`:                      "tmux",
		`sleep 1 & wait; nohup ./x &`:                "nohup",
	} {
		ok, what := Detaches(src)
		if !ok {
			t.Errorf("Detaches(%q) = false, want true", src)
		} else if !strings.Contains(what, names) {
			t.Errorf("Detaches(%q) names %q, want it to name %q", src, what, names)
		}
	}
	for _, src := range []string{
		`make test && echo ok`,
		`go test ./... 2>&1 | tail`,
		`sr-file write a.md --content '# R&D decision log'`,
		`echo 'https://x.invalid/?a=1&b=2'`,
		`sleep 0 & wait`,
		`a & b & wait`,
		`cat a &> b`,
		`bash -c 'echo "R&D"'`,
		"cat <<'EOF'\nx & y\nEOF",
	} {
		if ok, what := Detaches(src); ok {
			t.Errorf("Detaches(%q) = true (%q), want false", src, what)
		}
	}
}
