package main

import "github.com/spf13/cobra"

// addAgent wires `sr-test agent` (internal/srtest/agent.Command, part 3) into the root.
// The coordinator adds: root.AddCommand(agent.Command()).
func addAgent(root *cobra.Command) {}
