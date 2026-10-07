package commandmod

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Every global option in git(1) OPTIONS: known ones never leave the subcommand in doubt on their own,
// the value-taking ones consume their value, and anything unlisted fails closed.
func TestGitGapEarly_GlobalOptionsTable(t *testing.T) {
	noValue := []string{
		"-v", "--version", "-h", "--help", "--exec-path", "--html-path", "--man-path", "--info-path",
		"-p", "--paginate", "-P", "--no-pager", "--no-replace-objects", "--no-lazy-fetch",
		"--no-optional-locks", "--no-advice", "--bare", "--literal-pathspecs", "--glob-pathspecs",
		"--noglob-pathspecs", "--icase-pathspecs",
		"--exec-path=/x", "--git-dir=/x", "--work-tree=/x", "--namespace=n", "--super-prefix=p",
		"--config-env=a=B", "--attr-source=HEAD", "--list-cmds=main",
	}
	withValue := []string{"-C", "-c", "--git-dir", "--work-tree", "--namespace", "--super-prefix", "--config-env", "--attr-source"}
	for _, o := range noValue {
		assert.False(t, gitGapEarly(firstInv(t, `git `+o+` status $R`, "git")), "%s then a literal subcommand", o)
		assert.True(t, gitGapEarly(firstInv(t, `git `+o+` $S`, "git")), "%s then a lost subcommand", o)
	}
	for _, o := range withValue {
		assert.False(t, gitGapEarly(firstInv(t, `git `+o+` v status $R`, "git")), "%s v then a literal subcommand", o)
		assert.True(t, gitGapEarly(firstInv(t, `git `+o+` v $S`, "git")), "%s v then a lost subcommand", o)
		assert.True(t, gitGapEarly(firstInv(t, `git `+o+` $V push`, "git")), "%s then a lost value", o)
	}
	// an option the table does not know may take the next word: the subcommand is in doubt, with or without a gap
	for _, line := range []string{`git --future-opt val push`, `git --future-opt push`, `git -Z push`, `git -pZ status`} {
		assert.True(t, gitGapEarly(firstInv(t, line, "git")), line)
	}
}

// gitGapEarly is what the git gates match on: a git invocation that lost a word among its global
// options or where its subcommand stands. A gap after the subcommand, a program that is known and
// is not git, and the harness's own pre-stop command are never matched.
// sr:proves events/command-undecidable-not-guessed
func TestGitGapEarly(t *testing.T) {
	cases := []struct {
		line string
		want bool
	}{
		{`git $X`, true},
		{`git $X push`, true},
		{`git "$(echo push)" origin`, true},
		{`git -C $D push`, true},
		{`git -C /x $S push`, true},
		{`git -c a=b $S`, true},
		{`timeout $T git push`, true},
		{`env -S "$A" git push`, true},
		{`git --attr-source HEAD $S`, true},
		{`git --attr-source HEAD -c $X push`, true},
		{`git push $REMOTE`, false},
		{`git status "$X"`, false},
		{`git rev-parse --verify -q "$h^{commit}"`, false},
		{`git -C /x log --format="$F"`, false},
		{`git push origin HEAD`, false},
		{`D=/x; git -C $D push`, false},
	}
	for _, tc := range cases {
		t.Run(tc.line, func(t *testing.T) {
			assert.Equal(t, tc.want, gitGapEarly(firstInv(t, tc.line, "git")))
		})
	}
	// the harness's pre-stop line: no invocation of it is matched
	pre := `top=$(git rev-parse --show-toplevel 2>/dev/null); rows=$(printf '%s' "$refs" | jq -r '.[]?'); printf '%s\n' "$rows" | { while IFS="	" read -r f h b sha; do [ -d "$f" ] || continue; (cd "$f" && git rev-parse --verify -q "$h^{commit}" >/dev/null) || h="$sha"; out=$(cd "$f" && CLAUDECODE=1 sr-checks run --base "$b" --head "$h" 2>&1); done; }`
	for _, inv := range ExtractCommand(pre).Invocations {
		assert.False(t, gitGapEarly(inv), "%s %v", inv.Bin, inv.Argv)
	}
	// a program that is known and is not git never matches, gaps or not
	assert.False(t, gitGapEarly(firstInv(t, `sr-checks run --base "$b" --head "$h"`, "sr-checks")))
}
