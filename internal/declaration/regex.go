package declaration

import "regexp"

// compileRegex checks a StructureEntry's regex pattern compiles, so a malformed
// one is refused when the structure gate loads rather than at the moment it
// should have matched a path — the same load-time-refusal contract the match
// compilers enforce for expressions and globs.
//
// The pattern is Go's RE2 (the regexp package's), which is what the dispatch
// slice will match paths with. It is not anchored here: anchoring is a matching
// concern (does this pattern describe a whole path or a substring), and this
// function only answers whether the author wrote a compilable regex at all. The
// spec's own example is written anchored (`^memories/decisions/…$`), so the
// author states the anchors they want; validating would-be anchors is not this
// check's job.
func compileRegex(pattern string) error {
	_, err := regexp.Compile(pattern)
	return err
}
