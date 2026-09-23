// Package clidoc walks a Cobra command tree into a JSON shape the docs render,
// so the CLI reference is generated from the real commands, flags and help text
// rather than hand-listed. Each binary has a build-tagged generator that calls
// Emit with its own root; a script runs them and merges the output.
package clidoc

import (
	"encoding/json"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Flag is one flag on a command.
type Flag struct {
	Name    string `json:"name"`
	Short   string `json:"short,omitempty"`
	Default string `json:"default,omitempty"`
	Usage   string `json:"usage"`
}

// Command is one node of the tree.
type Command struct {
	Path     string    `json:"path"`  // e.g. "sr session start"
	Use      string    `json:"use"`   // the Use string as authored
	Short    string    `json:"short"` // one-line summary
	Long     string    `json:"long,omitempty"`
	Flags    []Flag    `json:"flags,omitempty"`
	Children []Command `json:"children,omitempty"`
}

// Emit writes the command tree rooted at cmd as indented JSON.
func Emit(root *cobra.Command, w io.Writer) error {
	tree := walk(root, root.Name())
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(tree)
}

func walk(cmd *cobra.Command, path string) Command {
	c := Command{
		Path:  path,
		Use:   cmd.Use,
		Short: cmd.Short,
		Long:  strings.TrimSpace(cmd.Long),
	}
	cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
		c.Flags = append(c.Flags, Flag{
			Name:    f.Name,
			Short:   f.Shorthand,
			Default: f.DefValue,
			Usage:   f.Usage,
		})
	})
	for _, sub := range cmd.Commands() {
		if sub.Hidden || sub.Name() == "help" || sub.Name() == "completion" {
			continue
		}
		c.Children = append(c.Children, walk(sub, path+" "+sub.Name()))
	}
	return c
}
