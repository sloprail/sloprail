package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
)

// agentGitIdentity closes the agent's global git config. It comes last, so it outranks what
// was copied above it: the agent commits as itself, not as the operator, and signs nothing
// (the operator's signing key is not in the agent's HOME, so a commit the operator's
// configuration would sign cannot be made there at all).
const agentGitIdentity = `[user]
	name = sr-eval agent
	email = agent@sr-eval.invalid
[commit]
	gpgsign = false
[tag]
	gpgsign = false
`

// copyGlobalConfig writes the agent's global git config: the operator's (src, which may not
// exist) without the lines that name a path under roots (the operator's home, the checkout,
// the directory sr-eval ran in): a hooksPath, excludesfile or safe.directory entry is a host
// path the agent would read with a plain config listing. Credential helpers and aliases are
// kept; the identity and signing are replaced by agentGitIdentity.
func copyGlobalConfig(src, dst string, roots []string) error {
	body, err := os.ReadFile(src)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var out bytes.Buffer
	for _, line := range strings.SplitAfter(string(body), "\n") {
		if !mentionsRoot(line, roots) {
			out.WriteString(line)
		}
	}
	if out.Len() > 0 && !bytes.HasSuffix(out.Bytes(), []byte("\n")) {
		out.WriteByte('\n')
	}
	out.WriteString(agentGitIdentity)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, out.Bytes(), 0o644)
}
