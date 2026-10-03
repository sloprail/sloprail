package commandmod

import "testing"

func gitStdin(t *testing.T, raw string) *string {
	t.Helper()
	for _, inv := range ExtractCommand(raw).Invocations {
		if inv.Bin == "git" {
			return inv.Stdin
		}
	}
	t.Fatalf("no git invocation in %q", raw)
	return nil
}

func strp(s string) *string { return &s }

func TestStdin(t *testing.T) {
	for _, c := range []struct {
		raw  string
		want *string
	}{
		{"git commit -F - <<'EOF'\na $x\nEOF\n", strp("a $x\n")},
		{"git commit -F - <<EOF\nplain\nEOF\n", strp("plain\n")},
		{"git commit -F - <<-EOF\n\tindented\n\tEOF\n", strp("indented\n")},
		{"git commit -F - <<<'msg'", strp("msg\n")},
		{"git commit -F - <<EOF\n$x\nEOF\n", nil},
		{"git commit -F - <<EOF\n$(cmd)\nEOF\n", nil},
		{"echo hi | git commit -F -", nil},
		{"cat <<EOF | git commit -F -\nx\nEOF\n", nil},
		{"git commit -F - < msg.txt", nil},
		{"git commit -F - <<<\"$m\"", nil},
		{"git commit -m x", nil},
	} {
		got := gitStdin(t, c.raw)
		if (got == nil) != (c.want == nil) || (got != nil && *got != *c.want) {
			t.Errorf("%q: got %q want %q", c.raw, deref(got), deref(c.want))
		}
	}
}

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}
