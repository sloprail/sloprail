package gitrepo

import "strings"

// TreeOf is the tree id of rev (`git rev-parse <rev>^{tree}`): two commits with one tree hold
// identical content, whatever their messages or parents.
func TreeOf(dir, rev string) (string, error) {
	out, err := run(dir, "rev-parse", "--verify", "--quiet", rev+"^{tree}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}
