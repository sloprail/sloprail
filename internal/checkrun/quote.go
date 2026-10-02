package checkrun

import "strings"

// shellQuote single-quotes s for a shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
