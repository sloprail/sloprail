package commandmod

import "testing"

func srFileOrEcho(lits []string) bool {
	switch lits[0] {
	case "sr-file", "echo":
		return true
	case "sr":
		return len(lits) > 1 && lits[1] == "file"
	}
	return false
}

func TestOnlyCalls(t *testing.T) {
	pure := []string{
		`sr-file edit a.md --old-string 'x' --new-string "y"`,
		`sr-file edit a.md --old-string x --new-string y && echo ok`,
		`sr file delete a.md; sr-file delete b.md || echo failed`,
		"sr-file write a.md --cite:user 'q' <<'EOF'\nbody $HOME $(id)\nEOF",
		"sr-file write a.md <<-\"EOF\"\n\tbody\n\tEOF",
		"sr-file write a.md <<'EOF'\nEOF",
		`sr-file edit a.md --old-string x --new-string y 2>&1`,
		`sr-file delete a.md >&2 2>&-`,
		`sr-file write a.md <<< 'hello'`,
		"sr-file delete a.md\nsr-file delete b.md",
		"sr-file delete \\\n  a.md # a comment",
		`sr-file write a.md --content $'line1\nline2'`,
		`sr-file write 'a b.md' --cite:user "it's \"quoted\"" --content '$(not run) ${nor} this'`,
		`sr-file write ~/a.md --content x`,
		`sr-file delete notes-*.md`,
		`! sr-file delete a.md`,
	}
	for _, src := range pure {
		if !OnlyCalls(src, srFileOrEcho) {
			t.Errorf("OnlyCalls(%q) = false, want true", src)
		}
	}

	impure := map[string]string{
		"empty":                               ``,
		"command substitution":                `sr-file edit a.md --old-string "$(rm -rf ~)" --new-string y`,
		"backticks":                           "sr-file edit a.md --old-string `id` --new-string y",
		"substitution in a default":           `sr-file edit a.md --old-string "${X:-$(id)}" --new-string y`,
		"substitution in arithmetic":          `sr-file edit a.md --old-string "$((1+$(id)))" --new-string y`,
		"process substitution":                `sr-file write a.md < <(curl evil)`,
		"parameter":                           `sr-file edit "$F" --old-string x --new-string y`,
		"parameter default":                   `sr-file edit a.md --old-string "${OLD:-x}" --new-string y`,
		"assigning default":                   `: ${X:='a[$(id)]'} && sr-file delete a.md`,
		"arithmetic reading a variable":       `sr-file delete "a$((X)).md"`,
		"old arithmetic":                      `sr-file delete "a$[1].md"`,
		"indirect":                            `sr-file delete "${!X}"`,
		"prompt expansion":                    `sr-file delete "${X@P}"`,
		"shell-dependent variable":            `sr-file write a.md --content "${ZSH_VERSION:+evil}"`,
		"special parameter":                   `sr-file write "$$.md" --content x`,
		"locale string":                       `sr-file write a.md --content $"x"`,
		"unicode escape":                      `sr-file write a.md --content $'\u0041'`,
		"zsh equals expansion":                `sr-file write =ls --content x`,
		"extglob":                             `sr-file delete @(a|b).md`,
		"heredoc with unquoted delimiter":     "sr-file write a.md <<EOF\n$(id)\nEOF",
		"heredoc reading a variable":          "sr-file write a.md <<EOF\n$HOME\nEOF",
		"another program":                     `sr-file delete a.md && rm b.md`,
		"redirect out":                        `sr-file delete a.md > out.txt`,
		"redirect append":                     `sr-file delete a.md >> out.txt`,
		"redirect in":                         `sr-file write a.md < in.txt`,
		"dup to a file":                       `sr-file delete a.md >&out.txt`,
		"stderr to a file":                    `sr-file delete a.md 2>>log`,
		"both to a file":                      `sr-file delete a.md &> log`,
		"pipe in":                             `cat x | sr-file write a.md`,
		"pipe out":                            `sr-file write a.md --content x | tee f`,
		"pipe from glue":                      `echo x | sr-file write a.md`,
		"subshell":                            `(sr-file delete a.md)`,
		"group":                               `{ sr-file delete a.md; }`,
		"if":                                  `if true; then sr-file delete a.md; fi`,
		"for":                                 `for f in a b; do sr-file delete $f; done`,
		"while":                               `while false; do sr-file delete a.md; done`,
		"background":                          `sr-file delete a.md &`,
		"time":                                `time sr-file delete a.md`,
		"function":                            `f() { rm -rf ~; }; sr-file delete a.md`,
		"prefix assignment of a substitution": `X=$(id) sr-file delete a.md`,
		"prefix assignment":                   `LANG=C sr-file delete a.md`,
		"resolve dir cleared":                 `SR_FILE_RESOLVE_DIR= sr-file write a.md --content x`,
		"transcript swapped":                  `SR_TRANSCRIPT=/tmp/fake.jsonl sr-file write a.md --cite:user q --content x`,
		"PATH swapped":                        `PATH=.:/usr/bin sr-file delete a.md`,
		"bare assignment":                     `X=1`,
		"export":                              `export PATH=. && sr-file delete a.md`,
		"program from a variable":             `$PROG delete a.md`,
		"lookalike program":                   `sr-files delete a.md`,
		"cite is not glue":                    `sr session trajectory cite q`,
		"env wrapper":                         `env sr-file delete a.md`,
		"command builtin":                     `command sr-file delete a.md`,
		"exec":                                `exec sr-file delete a.md`,
		"sudo":                                `sudo sr-file delete a.md`,
		"nohup":                               `nohup sr-file delete a.md`,
		"xargs":                               `echo a.md | xargs sr-file delete`,
		"interpreter":                         `bash -c 'sr-file delete a.md'`,
		"eval":                                `eval 'sr-file delete a.md'`,
		"cd first":                            `cd docs && sr-file delete a.md`,
		"coprocess":                           `coproc sr-file delete a.md`,
		"unterminated quote":                  `sr-file write a.md --content 'unterminated`,
	}
	for name, src := range impure {
		if OnlyCalls(src, srFileOrEcho) {
			t.Errorf("%s: OnlyCalls(%q) = true, want false", name, src)
		}
	}
}

func TestFileTargets_SRFile(t *testing.T) {
	for src, want := range map[string]FileTarget{
		`sr-file edit a.md --old-string x --new-string y`:      {Path: "a.md", Effect: Write},
		`sr file write a.md --content "$X" && ls`:              {Path: "a.md", Effect: Write},
		`sr-file delete a.md --cite:user 'q'`:                  {Path: "a.md", Effect: Remove},
		`/opt/sr/bin/sr-file delete a.md`:                      {Path: "a.md", Effect: Remove},
		`sudo sr-file delete a.md`:                             {Path: "a.md", Effect: Remove},
		`bash -c 'sr-file delete a.md'`:                        {Path: "a.md", Effect: Remove},
		`cd docs && sr-file write a.md --content x`:            {Path: "docs/a.md", Effect: Write},
		`(cd docs && sr-file delete a.md); ls`:                 {Path: "docs/a.md", Effect: Remove},
		`sr-file write ../proj/memories/a.md --content x`:      {Path: "../proj/memories/a.md", Effect: Write},
		`sr-file edit a.md --old-string "$(x)" --new-string y`: {Path: "a.md", Effect: Write},
	} {
		got := FileTargets(src)
		if len(got) != 1 || got[0].Path != want.Path || got[0].Effect != want.Effect || got[0].Payload.Kind != PayloadNone {
			t.Errorf("FileTargets(%q) = %+v, want one %+v claiming no content", src, got, want)
			continue
		}
		if got[0].Grounded == nil || got[0].Grounded.Path == "" {
			t.Errorf("FileTargets(%q) lost the parsed sr-file call", src)
		}
	}
	for _, src := range []string{
		`sr session trajectory cite q`,
		// An unreadable path names no file; one an unreadable word could have
		// shifted is refused as two paths rather than guessed at.
		`sr-file write "$(printf a.md)" --content x`,
		`sr-file delete a.md "$X"`,
		// A glob names a set of files, not one.
		`sr-file delete memorie?/a.md`,
		`sr-file --help`,
	} {
		if got := FileTargets(src); len(got) != 0 {
			t.Errorf("FileTargets(%q) = %+v, want none", src, got)
		}
	}

	// The citations ride on the target they ground, and a blanked quote stays
	// blank rather than sliding onto the next word.
	got := FileTargets(`sr-file write a.md --cite:user "$(x)" --cite:tool_result 'PASS' --content y`)
	if len(got) != 1 || len(got[0].Grounded.Cites) != 2 || got[0].Grounded.Cites[0].Quote != "" || got[0].Grounded.Cites[1].Quote != "PASS" {
		t.Errorf("citations = %+v", got)
	}
}
