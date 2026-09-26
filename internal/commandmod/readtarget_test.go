package commandmod

import "testing"

// TestReadsFile_KnownWholeFileReaders pins the vocabulary half: every binary
// readBins names, reading the exact path it operates on.
func TestReadsFile_KnownWholeFileReaders(t *testing.T) {
	for _, bin := range []string{"cat", "head", "tail", "less", "more", "bat"} {
		if !ReadsFile(bin+" /a/SKILL.md", "/a/SKILL.md") {
			t.Errorf("ReadsFile(%q, ...) = false, want true", bin+" /a/SKILL.md")
		}
	}
}

// TestReadsFile_FlagsDoNotHideTheOperand pins that a binary's own flags — a
// line count, a follow flag — do not prevent the operand from being read as
// the file.
func TestReadsFile_FlagsDoNotHideTheOperand(t *testing.T) {
	if !ReadsFile("head -n 40 /a/SKILL.md", "/a/SKILL.md") {
		t.Error("head -n 40 should still read the operand")
	}
	if !ReadsFile("tail -f /a/SKILL.md", "/a/SKILL.md") {
		t.Error("tail -f should still read the operand")
	}
	if !ReadsFile("cat -n /a/SKILL.md", "/a/SKILL.md") {
		t.Error("cat -n should still read the operand")
	}
}

// TestReadsFile_ADifferentPathIsNotAMatch pins the exact-path requirement: a
// command reading some OTHER file must not report reading the one asked
// about.
func TestReadsFile_ADifferentPathIsNotAMatch(t *testing.T) {
	if ReadsFile("cat /a/other.md", "/a/SKILL.md") {
		t.Error("cat of a different file must not match")
	}
	if ReadsFile("cat /a/SKILL.md.bak", "/a/SKILL.md") {
		t.Error("a similarly-named file must not match")
	}
}

// TestReadsFile_WritingCommandsAreNotReads pins the other direction: a command
// that WRITES the path (rm, a redirection into it) is not a read of it, even
// though it names the same path.
func TestReadsFile_WritingCommandsAreNotReads(t *testing.T) {
	if ReadsFile("rm /a/SKILL.md", "/a/SKILL.md") {
		t.Error("rm must not be read as a read")
	}
	if ReadsFile("echo hi > /a/SKILL.md", "/a/SKILL.md") {
		t.Error("a truncating redirection must not be read as a read")
	}
	if ReadsFile("sed -i s/a/b/ /a/SKILL.md", "/a/SKILL.md") {
		t.Error("an in-place edit must not be read as a read")
	}
}

// TestReadsFile_InputRedirectionIsSyntaxNotVocabulary pins the redirection
// half — `<` and `<>` are shell grammar, need no binary vocabulary, and work
// for a program this package has never heard of.
func TestReadsFile_InputRedirectionIsSyntaxNotVocabulary(t *testing.T) {
	if !ReadsFile("grep foo < /a/SKILL.md", "/a/SKILL.md") {
		t.Error("`<` should be read as reading the file")
	}
	if !ReadsFile("some-unknown-tool < /a/SKILL.md", "/a/SKILL.md") {
		t.Error("`<` should work regardless of the program in front of it")
	}
	// A heredoc/here-string is a literal BODY the line carries, not a file, so
	// neither names a path to match against.
	if ReadsFile("cat <<EOF\n/a/SKILL.md\nEOF", "/a/SKILL.md") {
		t.Error("a heredoc body is not a file path, however file-shaped its text looks")
	}
}

// TestReadsFile_WrappersDoNotHideIt pins that the same unwrapping
// FileTargets relies on for writes applies here too — a rule about reading a
// file must not be evadable by a `sudo` in front of the read.
func TestReadsFile_WrappersDoNotHideIt(t *testing.T) {
	if !ReadsFile("sudo cat /a/SKILL.md", "/a/SKILL.md") {
		t.Error("sudo should not hide the read")
	}
	if !ReadsFile("timeout 5 cat /a/SKILL.md", "/a/SKILL.md") {
		t.Error("timeout should not hide the read")
	}
}

// TestReadsFile_AnUncertainPathIsNotGuessedAt pins the same certainty floor
// FileTargets applies: a variable that is not resolvable in the empty
// environment names no path this can be sure of.
func TestReadsFile_AnUncertainPathIsNotGuessedAt(t *testing.T) {
	if ReadsFile("cat $F", "/a/SKILL.md") {
		t.Error("an unresolvable variable must not be guessed at")
	}
	if ReadsFile("cat $(ls)", "/a/SKILL.md") {
		t.Error("a command substitution must not be guessed at")
	}
}

// TestReadsFile_EmptyPathIsNeverAMatch pins the degenerate input: asking
// whether a command reads the empty path is always false, never a panic or a
// false positive on a command whose operand also happens to be empty.
func TestReadsFile_EmptyPathIsNeverAMatch(t *testing.T) {
	if ReadsFile("cat /a/SKILL.md", "") {
		t.Error("the empty path must never match")
	}
}

// TestReadsFile_UnparseableLineYieldsFalse pins the same answer FileTargets
// gives for a line nobody can read: no guess, just false.
func TestReadsFile_UnparseableLineYieldsFalse(t *testing.T) {
	if ReadsFile("cat /a/SKILL.md &&", "/a/SKILL.md") {
		t.Error("an unparseable line must not match")
	}
}
