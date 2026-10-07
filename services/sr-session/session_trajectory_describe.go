package main

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/harness"
	"github.com/sloprail/sloprail/internal/transcript"
)

// TrajectoryDescription is what `describe` answers about a trajectory — settled
// by its path and the session's records, not by walking its entries.
//
// The field tags are the wire contract the spec names. subagentPaths is a
// non-optional string[]: it is always present, empty when there are none, so a
// consumer reads `.subagentPaths` without first testing for its existence.
// parentPath is omitempty because a root has none, and its absence is the
// meaningful "this is not a sub-agent, or its parent could not be named".
type TrajectoryDescription struct {
	// IsSubagent reports whether this trajectory is a sub-agent's rather than the
	// main line's.
	IsSubagent bool `json:"isSubagent"`

	// ParentPath is the path of the trajectory that spawned this one, when this
	// is a sub-agent's — the IMMEDIATE parent, which may itself be a sub-agent.
	// Absent when this is a root, or when a sub-agent's dispatching call could
	// not be located.
	ParentPath string `json:"parentPath,omitempty"`

	// SubagentPaths are the paths of the sub-agent trajectories this session
	// spawned, for the harnesses that write them to their own files. Empty when
	// there are none, or when the harness inlines sub-agent entries instead.
	SubagentPaths []string `json:"subagentPaths"`
}

// newSessionTrajectoryDescribeCmd answers facts ABOUT a trajectory: whether it
// is a sub-agent's, which trajectory spawned it, and which it spawned.
//
// These feed straight back into the other commands: parentPath and each of
// subagentPaths is a `--path` for `cite` or `normalize`. A rule reading from
// inside a sub-agent names its root this way; one at the root enumerates the
// sub-agents this way, then re-reads each — the enumeration a session-directory
// glob used to stand in for.
func newSessionTrajectoryDescribeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "describe",
		Short: "Facts about a trajectory — is it a sub-agent's, its parent, its sub-agents",
		Long: `Facts about a trajectory, not what is in it.

Answers "what IS this file" — whether it belongs to a sub-agent, which
trajectory spawned it, and which other trajectories belong to the same run.
Settled by the path and the session's own records, not by walking the entries.

  isSubagent      whether this trajectory is a sub-agent's
  parentPath      the immediate parent trajectory, when this is a sub-agent's
  subagentPaths   the sub-agent trajectories this session spawned

The paths feed back into ` + "`trajectory cite --path`" + ` and ` + "`trajectory normalize\n--path`" + `: the root read from inside a sub-agent, or the sub-agents enumerated
from the root and each re-read.

Defaults to the trajectory the hook was invoked for; --path describes another.`,
		Args: cobra.NoArgs,
		RunE: runSessionTrajectoryDescribe,
	}
	cmd.Flags().String("path", "",
		"Which trajectory to describe; defaults to the one the hook was invoked for")
	return cmd
}

func runSessionTrajectoryDescribe(cmd *cobra.Command, _ []string) error {
	path, p, err := resolveTrajectory(cmd)
	if err != nil {
		return err
	}
	if path == "" {
		return errNoTrajectory()
	}

	desc := TrajectoryDescription{
		IsSubagent:    transcript.IsSubagentTranscript(path),
		SubagentPaths: []string{},
	}

	// The sub-agents this trajectory spawned — the inverse correlation, and the
	// one a root asks. Present for a sub-agent too, since a sub-agent may have
	// dispatched sub-agents of its own.
	desc.SubagentPaths = subagentRecords(path)

	// The immediate parent — derived only when this is a sub-agent whose meta
	// file names the dispatching call. A root, or a sub-agent whose parent cannot
	// be located, leaves parentPath absent rather than guessed.
	if desc.IsSubagent {
		parent, err := transcript.ParentPath(path, searchDirFor(path, p))
		if err != nil {
			return err
		}
		desc.ParentPath = parent
	}

	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetEscapeHTML(false)
	return enc.Encode(desc)
}

// subagentRecords is the sub-agent session records the current harness can tie to
// the trajectory at path, sorted — whatever its layout (Claude Code nests them under
// <session>/subagents/, Codex writes a rollout per sub-agent naming its parent). A
// harness with no way to tie them (it does not implement harness.SubagentLocator)
// has none to list. Companion files (meta.json) are not records and are left out.
func subagentRecords(path string) []string {
	out := []string{}
	l, ok := harness.Current().Transcripts().(harness.SubagentLocator)
	if !ok {
		return out
	}
	for _, f := range l.SubagentFiles(path) {
		if strings.HasSuffix(f.Path, ".jsonl") {
			out = append(out, f.Path)
		}
	}
	sort.Strings(out)
	return out
}
