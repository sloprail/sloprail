package changeset

// Env is what a check's process is told about the commits it judges, beside its
// stdin: where the read-only snapshot of head is, and the range's two ends.
//
// A check reads SR_TREE, never the working tree, which may hold a half-finished
// edit. SR_BASE and SR_HEAD are SHAs for a check that wants to ask git something
// itself (`git -C "$SR_TREE" diff "$SR_BASE" "$SR_HEAD"`). They are not part of
// the fingerprint, so a check must not let a verdict depend on the SHAs
// themselves — only on what they select.
func Env(tree, base, head string) []string {
	return []string{"SR_TREE=" + tree, "SR_BASE=" + base, "SR_HEAD=" + head}
}
