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
		`job1 & job2 & wait $!`:                      "job1",
		`job1 & job2 & wait -n`:                      "job1",
		`job1 & job2 & wait 1234`:                    "job1",
		// `$!` is fixed by the LAST `&` and does not move: a second `wait $!`
		// with no new `&` in between targets the exact same job the first one
		// did, not job1. round-5 review of #83, finding 1.
		`job1 & job2 & wait $!; wait $!`:             "job1",
		`job1 & job2 & wait "$!"; wait "$!"`:         "job1",
		`job1 & job2 & job3 & wait $!; wait $!`:      "job1",
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
		`a & wait $!`,
		`a & wait "$!"`,
		`a & wait -n`,
		// Two DISTINCT jobs, each covered by its own targeted wait after its
		// own `&` — the `$!` each names is a different job, so both are fine.
		`job1 & wait $!; job2 & wait $!`,
		// `wait -n` (unlike `wait $!`) can reap a different job each call, so
		// two of them are still tallied as covering two jobs.
		`job1 & job2 & wait -n; wait -n`,
		`cat a &> b`,
		`bash -c 'echo "R&D"'`,
		"cat <<'EOF'\nx & y\nEOF",
	} {
		if ok, what := Detaches(src); ok {
			t.Errorf("Detaches(%q) = true (%q), want false", src, what)
		}
	}
}
