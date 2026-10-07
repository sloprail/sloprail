package codex

import (
	"strings"
)

// Codex writes files with one tool, apply_patch, whose argument is a patch in its own
// envelope (recorded: harness-mocks codex-mock/snapshots/runs/file-tools, whose
// PreToolUse payload carries it as tool_input.command):
//
//	*** Begin Patch
//	*** Add File: <path>          every following line is "+<line>"
//	*** Delete File: <path>
//	*** Update File: <path>
//	*** Move to: <path>           optional, right after Update File
//	@@ <optional context line>    opens a hunk
//	 <context>  -<removed>  +<added>
//	*** End of File               optional, closes a hunk anchored at the file's end
//	*** End Patch
//
// A patch can touch several files, which no Write/Edit-shaped argument can say, so
// the adapter reports its effects (harness.FileEffect) rather than translating it.

type opKind int

const (
	opAdd opKind = iota
	opDelete
	opUpdate
)

type fileOp struct {
	kind   opKind
	path   string
	moveTo string
	add    []string // an added file's lines
	hunks  []hunk
}

type hunk struct {
	context string
	old     []string
	new     []string
	atEOF   bool
}

// parsePatch reads a patch envelope. ok is false when the text is not one.
func parsePatch(text string) (ops []fileOp, ok bool) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	i := 0
	for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
		i++
	}
	if i >= len(lines) || strings.TrimSpace(lines[i]) != "*** Begin Patch" {
		return nil, false
	}
	i++
	var cur *fileOp
	var h *hunk
	flush := func() {
		if cur != nil {
			if h != nil {
				cur.hunks = append(cur.hunks, *h)
				h = nil
			}
			ops = append(ops, *cur)
			cur = nil
		}
	}
	ended := false
	for ; i < len(lines); i++ {
		l := lines[i]
		switch {
		case strings.TrimSpace(l) == "*** End Patch":
			flush()
			ended = true
		case strings.HasPrefix(l, "*** Add File: "):
			flush()
			cur = &fileOp{kind: opAdd, path: strings.TrimSpace(strings.TrimPrefix(l, "*** Add File: "))}
		case strings.HasPrefix(l, "*** Delete File: "):
			flush()
			cur = &fileOp{kind: opDelete, path: strings.TrimSpace(strings.TrimPrefix(l, "*** Delete File: "))}
		case strings.HasPrefix(l, "*** Update File: "):
			flush()
			cur = &fileOp{kind: opUpdate, path: strings.TrimSpace(strings.TrimPrefix(l, "*** Update File: "))}
		case cur == nil:
			// text outside any file section: not part of the grammar
		case strings.HasPrefix(l, "*** Move to: ") && cur.kind == opUpdate:
			cur.moveTo = strings.TrimSpace(strings.TrimPrefix(l, "*** Move to: "))
		case cur.kind == opAdd:
			if strings.HasPrefix(l, "+") {
				cur.add = append(cur.add, l[1:])
			}
		case cur.kind == opUpdate:
			switch {
			case strings.HasPrefix(l, "@@"):
				if h != nil {
					cur.hunks = append(cur.hunks, *h)
				}
				h = &hunk{context: strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(l, "@@"), " "))}
			case l == "*** End of File":
				if h != nil {
					h.atEOF = true
				}
			default:
				if h == nil {
					h = &hunk{}
				}
				switch {
				case strings.HasPrefix(l, "+"):
					h.new = append(h.new, l[1:])
				case strings.HasPrefix(l, "-"):
					h.old = append(h.old, l[1:])
				case strings.HasPrefix(l, " "):
					h.old = append(h.old, l[1:])
					h.new = append(h.new, l[1:])
				case l == "":
					h.old = append(h.old, "")
					h.new = append(h.new, "")
				}
			}
		}
	}
	if !ended {
		flush()
	}
	return ops, len(ops) > 0
}

// apply returns the file the update leaves behind, and false when a hunk does not
// find its place (so the result cannot be worked out here).
func (o fileOp) apply(before string) (string, bool) {
	lines := strings.Split(before, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	pos := 0
	for _, h := range o.hunks {
		if h.context != "" {
			idx := seek(lines, []string{h.context}, pos, false)
			if idx < 0 {
				return "", false
			}
			pos = idx + 1
		}
		if len(h.old) == 0 {
			lines = append(lines, h.new...)
			pos = len(lines)
			continue
		}
		idx := seek(lines, h.old, pos, h.atEOF)
		if idx < 0 {
			return "", false
		}
		next := append([]string{}, lines[:idx]...)
		next = append(next, h.new...)
		lines = append(next, lines[idx+len(h.old):]...)
		pos = idx + len(h.new)
	}
	return strings.Join(lines, "\n") + "\n", true
}

// seek finds want in lines at or after from: exactly, then ignoring trailing
// whitespace, then ignoring surrounding whitespace (the order Codex itself relaxes
// in). fromEnd tries the file's end first, for a hunk marked End of File.
func seek(lines, want []string, from int, fromEnd bool) int {
	norm := []func(string) string{
		func(s string) string { return s },
		func(s string) string { return strings.TrimRight(s, " \t\r") },
		strings.TrimSpace,
	}
	for _, n := range norm {
		if fromEnd {
			if at := len(lines) - len(want); at >= from && match(lines, want, at, n) {
				return at
			}
		}
		for at := from; at+len(want) <= len(lines); at++ {
			if match(lines, want, at, n) {
				return at
			}
		}
	}
	return -1
}

func match(lines, want []string, at int, norm func(string) string) bool {
	for j, w := range want {
		if norm(lines[at+j]) != norm(w) {
			return false
		}
	}
	return true
}
