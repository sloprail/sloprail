package commandmod

import (
	"testing"
)

// This file is the comprehensive pin for cwd.go, on top of the narrower cases
// already covered inline in filetarget_test.go and payload_test.go. Those two
// stay as they are — each pins one specific claim next to the machinery it
// exercises (a redirection target, a copy payload) — and this file is the
// single table an author can scan to see every SHAPE of `cd`-affected command
// this package resolves, declines, or scopes, with one expected answer each.
//
// Every case renders through the same check(command, want) helper
// filetarget_test.go already defines, so "(nothing)" means declined
// (unknown cwd, or a target this package cannot otherwise be sure of) exactly
// as it does everywhere else in this package — there is no third spelling for
// "unknown" introduced here.
//
// cwdCase and cwdTableCases are package-level (rather than a literal local to
// the test function) for exactly one reason: TestCwd_TableHasAtLeastThirtyCases
// counts the SAME slice this test runs, not a re-typed copy of it, so the two
// tests cannot silently drift apart the way two independent literals could.
type cwdCase struct {
	name    string
	command string
	want    string
}

var cwdTableCases = []cwdCase{
	// -- composition and scoping: absolute and relative cd --------------
	{
		name:    "absolute cd, relative target",
		command: "cd /abs && echo x > rel",
		want:    "write:/abs/rel",
	},
	{
		name:    "relative cd composes onto wherever the line started",
		command: "cd rel && echo x > rel2",
		want:    "write:rel/rel2",
	},
	{
		name:    "chained relative cd's compose",
		command: "cd a && cd b && touch f",
		want:    "write:a/b/f",
	},
	{
		name:    "cd .. from an absolute start",
		command: "cd /a/b && cd .. && touch f",
		want:    "write:/a/f",
	},
	{
		name:    "cd .. from a relative start",
		command: "cd .. && touch f",
		want:    "write:../f",
	},
	{
		name:    "cd ../sibling from a relative start",
		command: "cd ../sibling && touch f",
		want:    "write:../sibling/f",
	},
	{
		name:    "cd .. above an absolute root stays absolute and outside",
		command: "cd / && cd .. && touch f",
		want:    "write:/f",
	},
	{
		name:    "two absolute cd's, the second wins",
		command: "cd /a && cd /b && touch f",
		want:    "write:/b/f",
	},

	// -- separators: &&, ||, ;, newlines ---------------------------------
	{
		name:    "&& sequences",
		command: "cd /a && touch f",
		want:    "write:/a/f",
	},
	{
		name:    "|| sequences (the left side's cd still runs first)",
		command: "cd /a || true; touch f",
		want:    "write:/a/f",
	},
	{
		name:    "; sequences",
		command: "cd /a; touch f",
		want:    "write:/a/f",
	},
	{
		name:    "a newline sequences the same as ;",
		command: "cd /a\ntouch f",
		want:    "write:/a/f",
	},
	{
		name:    "a multi-line command with several statements",
		command: "cd /a\ntouch one\ncd sub\ntouch two",
		want:    "write:/a/one write:/a/sub/two",
	},

	// -- pipes: each side is its own subprocess, cd does not leak -------
	{
		name:    "cd on the left side of a pipe does not leak to the right",
		command: "cd /elsewhere | touch y",
		want:    "write:y",
	},
	{
		// Pipe binds tighter than &&, so this parses as
		// `cd /a && (cd b | touch c) && touch d` — verified against the
		// parser directly, not assumed. `cd b` runs in its own subprocess
		// (relative to /a, so /a/b, but that never escapes it), `touch c`
		// runs in ITS OWN subprocess still at /a (pipe sides do not see each
		// other's cwd changes), and `touch d` runs in the shell that has been
		// at /a the whole time — /a's own `cd`, never the piped one.
		name:    "cd on the left side of a pipe does not leak past the pipeline",
		command: "cd /a && cd b | touch c && touch d",
		want:    "write:/a/c write:/a/d",
	},
	{
		name:    "cd on the right side of a pipe does not leak past it either",
		command: "true | cd /elsewhere && touch y",
		want:    "write:y",
	},

	// -- subshells: scoped; groups and interpreters: not scoped ---------
	{
		name:    "a subshell's cd does not escape it",
		command: "(cd /abs && touch a) && touch b",
		want:    "write:/abs/a write:b",
	},
	{
		name:    "nested subshells each scope their own cd",
		command: "(cd /a && (cd /b && touch x) && touch y) && touch z",
		want:    "write:/a/y write:/b/x write:z",
	},
	{
		name:    "a { } group's cd DOES escape it, unlike a subshell",
		command: "{ cd /a; } && touch b",
		want:    "write:/a/b",
	},
	{
		name:    "bash -c resolves its own payload's cd",
		command: "bash -c 'cd /a && touch b'",
		want:    "write:/a/b",
	},
	{
		name:    "sh -c resolves its own payload's cd",
		command: "sh -c 'cd /a && touch b'",
		want:    "write:/a/b",
	},
	{
		name:    "bash -c's cd does not escape into the outer line",
		command: "bash -c 'cd /a' && touch b",
		want:    "write:b",
	},

	// -- if/for/while/case bodies -----------------------------------------
	{
		name:    "an if whose branches disagree makes the rest unknown",
		command: "if true; then cd /a; fi; touch b",
		want:    "(nothing)",
	},
	{
		name:    "an if whose branches agree is resolved",
		command: "cd /a && if true; then cd sub; else cd sub; fi && touch b",
		want:    "write:/a/sub/b",
	},
	{
		name:    "an if with no cd in either branch is unaffected",
		command: "cd /a && if true; then touch nop; fi && touch b",
		want:    "write:/a/b write:/a/nop",
	},
	{
		name:    "a for loop body with a cd makes the rest unknown",
		command: "for i in 1; do cd /a; done; touch b",
		want:    "(nothing)",
	},
	{
		name:    "a for loop body with no cd is unaffected",
		command: "cd /a && for i in 1 2 3; do touch nop; done && touch b",
		want:    "write:/a/b write:/a/nop",
	},
	{
		name:    "a while loop body with a cd makes the rest unknown",
		command: "while false; do cd /a; done; touch b",
		want:    "(nothing)",
	},
	{
		name:    "a while loop body with no cd is unaffected",
		command: "cd /a && while false; do touch nop; done && touch b",
		want:    "write:/a/b write:/a/nop",
	},
	{
		name:    "a case whose matched item might cd makes the rest unknown",
		command: "cd /a && case $x in y) cd sub;; esac && touch b",
		want:    "(nothing)",
	},
	{
		name:    "a case with no cd in any item is unaffected",
		command: "cd /a && case $x in y) touch nop;; z) touch nop2;; esac && touch b",
		want:    "write:/a/b write:/a/nop write:/a/nop2",
	},

	// -- unknowns: cd itself unresolvable --------------------------------
	{
		name:    "cd with no argument is unknown",
		command: "cd && echo x > rel",
		want:    "(nothing)",
	},
	{
		name:    "cd ~ is unknown (no real $HOME to expand against)",
		command: "cd ~ && echo x > rel",
		want:    "(nothing)",
	},
	{
		name:    "cd into an unresolvable $VAR is unknown",
		command: `cd "$VAR" && echo x > rel`,
		want:    "(nothing)",
	},
	{
		name:    "cd into $(pwd)/x is unknown",
		command: "cd $(pwd)/x && echo x > rel",
		want:    "(nothing)",
	},
	{
		name:    "cd into a glob is unknown (this package never reads the filesystem)",
		command: "cd /a/* && echo x > rel",
		want:    "(nothing)",
	},
	{
		name:    "pushd is recognised but not resolved: unknown",
		command: "pushd /elsewhere && touch a",
		want:    "(nothing)",
	},
	{
		name:    "eval of a literal cd moves what follows, like a block",
		command: "eval 'cd /elsewhere' && echo x > rel",
		want:    "write:/elsewhere/rel",
	},
	{
		name:    "eval of a payload that cannot be read is unknown",
		command: `eval "$SETUP" && echo x > rel`,
		want:    "(nothing)",
	},
	{
		name:    "popd is recognised but not resolved: unknown",
		command: "cd /a && popd && touch a",
		want:    "(nothing)",
	},
	{
		name:    "cd - is unknown, and stays unknown for what follows",
		command: "cd /a && cd - && touch y",
		want:    "(nothing)",
	},
	{
		name:    "a cd that goes unknown does not affect an ABSOLUTE target",
		command: "cd && rm /abs/notes.md",
		want:    "remove:/abs/notes.md",
	},
	{
		name:    "unknown persists across further relative cd's in the same scope",
		command: "cd && cd sub && touch a",
		want:    "(nothing)",
	},

	// -- targets: every write/remove shape gets a cd variant ------------
	{
		name:    "> redirection",
		command: "cd /abs && echo x > rel.txt",
		want:    "write:/abs/rel.txt",
	},
	{
		name:    ">> redirection",
		command: "cd /abs && echo x >> rel.txt",
		want:    "write:/abs/rel.txt",
	},
	{
		name:    "tee",
		command: "cd /abs && tee rel.txt",
		want:    "write:/abs/rel.txt",
	},
	{
		name:    "cp destination",
		command: "cd /abs && cp a.md b.md",
		want:    "write:/abs/b.md",
	},
	{
		name:    "mv source and destination",
		command: "cd /abs && mv a.md b.md",
		want:    "remove:/abs/a.md write:/abs/b.md",
	},
	{
		name:    "rm",
		command: "cd /abs && rm notes.md",
		want:    "remove:/abs/notes.md",
	},
	{
		name:    "mkdir",
		command: "cd /abs && mkdir newdir",
		want:    "write:/abs/newdir",
	},
	{
		name:    "touch",
		command: "cd /abs && touch new.md",
		want:    "write:/abs/new.md",
	},
	{
		name:    "a quoted heredoc into cat",
		command: "cd /abs && cat > f.txt <<'EOF'\nbody\nEOF\n",
		want:    "write:/abs/f.txt",
	},
	{
		name:    "sed -i names its file",
		command: "cd /abs && sed -i '' s/a/b/ f.md",
		want:    "write:/abs/f.md",
	},
	{
		name:    "an absolute target after a cd is unchanged",
		command: "cd /abs && rm /other/notes.md",
		want:    "remove:/other/notes.md",
	},
	{
		name:    "a quoted path with a space, after an absolute cd",
		command: `cd "/abs dir" && echo x > "sub dir/f.txt"`,
		want:    "write:/abs dir/sub dir/f.txt",
	},
	{
		name:    "a quoted cd target with a space",
		command: `cd "/has space" && echo x > f`,
		want:    "write:/has space/f",
	},

	// -- regression: a line with no cd at all is unaffected -------------
	{
		name:    "a plain relative write with no cd",
		command: "echo x > rel.txt",
		want:    "write:rel.txt",
	},
	{
		name:    "a plain rm with no cd",
		command: "rm notes.md",
		want:    "remove:notes.md",
	},
	{
		name:    "a plain cp with no cd",
		command: "cp a.md b.md",
		want:    "write:b.md",
	},

	// -- the real reported cases, verbatim (repo paths made absolute
	//    placeholders standing in for the actual worktree paths) --------
	{
		name:    "the redirect-analytics report, verbatim shape",
		command: "cd /Users/x/ws/sloprail/wt/redirect-analytics && mkdir -p tests && printf 'ok' > public/_redirects",
		want: "write:/Users/x/ws/sloprail/wt/redirect-analytics/public/_redirects " +
			"write:/Users/x/ws/sloprail/wt/redirect-analytics/tests",
	},
	{
		// Two statements, sequenced by the newline after the first heredoc's
		// terminator — the reported line's own shape: a cd into another
		// repo, a command whose own (unquoted) heredoc this package cannot
		// and need not read, and then a SECOND, quoted heredoc creating a
		// file relative to that same repo. `&&` cannot follow a heredoc
		// terminator on the terminator's own line (a real shell rejects that
		// exact spelling too — verified against the parser directly, not
		// assumed), which is why this is two newline-sequenced statements
		// rather than one chained by &&; the cd's effect crosses the newline
		// exactly as it would cross a `;` or an &&.
		name: "the hero-positioning report, verbatim shape (a heredoc create after a cd into another repo)",
		command: "cd /Users/x/ws/sloprail/wt/hero-positioning && python3 - <<EOF\nprint('hi')\nEOF\n" +
			"cat > src/content/sections/rules-first.yaml <<'EOF'\nkey: value\nEOF\n",
		want: "write:/Users/x/ws/sloprail/wt/hero-positioning/src/content/sections/rules-first.yaml",
	},
}

// TestCwd_ComprehensiveTable runs every case in cwdTableCases.
func TestCwd_ComprehensiveTable(t *testing.T) {
	for _, tc := range cwdTableCases {
		t.Run(tc.name, func(t *testing.T) {
			check(t, tc.command, tc.want)
		})
	}
}

// TestCwd_TableHasAtLeastThirtyCases guards cwdTableCases against silently
// shrinking back down under review — the coverage this file exists for is a
// property of its SIZE as much as its content, and a table quietly trimmed to
// "whatever still compiles" during a later edit would not be caught by any
// single case failing on its own.
func TestCwd_TableHasAtLeastThirtyCases(t *testing.T) {
	const minCases = 30
	if got := len(cwdTableCases); got < minCases {
		t.Errorf("cwdTableCases has %d cases, want at least %d", got, minCases)
	}
}
