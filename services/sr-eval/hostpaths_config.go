package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
)

// copyGlobalConfig copies the operator's global VCS config without the lines that name a
// path under roots (the operator's home, the checkout, the directory sr-eval ran in): a
// hooksPath, excludesfile or safe.directory entry is a host path the agent would read with
// a plain config listing. Identity, credential helpers and aliases are kept.
func copyGlobalConfig(src, dst string, roots []string) error {
	body, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	var out bytes.Buffer
	for _, line := range strings.SplitAfter(string(body), "\n") {
		if !mentionsRoot(line, roots) {
			out.WriteString(line)
		}
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, out.Bytes(), 0o644)
}
