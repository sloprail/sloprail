// Command split-require moves `require: skill` / `require: context` entries off
// file-guards and onto gates (issue #162).
//
//	go run ./internal/declaration/cmd/split-require [-n] <dir>...
//
// Each <dir> is searched recursively for `file-guard/<name>/file-guard.yaml`
// (a `.sloprail` root, a plugin, a repository). -n prints the plan and writes
// nothing. Idempotent.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/sloprail/sloprail/internal/declaration/splitrequire"
)

func main() {
	dry := flag.Bool("n", false, "dry run: print the plan, change nothing")
	flag.Parse()
	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: split-require [-n] <dir>...")
		os.Exit(2)
	}
	actions, err := splitrequire.Run(flag.Args(), !*dry, os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, a := range actions {
		if a.Err != nil {
			os.Exit(1)
		}
	}
}
