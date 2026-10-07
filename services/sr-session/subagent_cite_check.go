package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/sloprail/sloprail/internal/commandmod"
	"github.com/sloprail/sloprail/internal/grounding"
	"github.com/sloprail/sloprail/internal/transcript"
)

// A sub-agent never sees the user's messages — only the prompt its parent
// wrote — and nothing in its tool calls' environment says it is a sub-agent: a
// Bash it runs has the same session id as the root's, and no agent id. So
// sr-file and `trajectory cite`, run by it, cannot tell it why its --cite:user
// quote did not resolve. The pre-tool hook can: its payload names the agent.
//
// subagentUserCitationRefusal refuses, up front, a sub-agent's command whose
// user-pool citation will not resolve, with the words sr-file or cite would
// print plus what a sub-agent needs to hear (transcript.UnresolvedUserHint). It
// runs whether or not any rule requires a citation, and it only ever refuses
// what would fail anyway, losing nothing:
//
//   - the line is one && chain of exact calls (commandmod.AndChain), so the
//     failing call stops everything after it;
//   - the calls are walked in order and the walk stops at the first one with an
//     effect: an sr-file call is checked (its citations resolve before it writes
//     a byte) and ends the walk; a cite is read-only and the walk goes on; echo,
//     true and : are pure glue; anything else ends it. So nothing that would
//     have run before the failing call is refused with it;
//   - only a citation that names the user pool and fails to RESOLVE refuses —
//     a quote in no entry, in several, or asked of a sub-agent whose session is
//     not found. A request that does not parse, or a record that cannot be read,
//     is left to the real run.
//
// The root's calls are not checked here: the root's own sr-file and cite say
// everything there is to say.
func subagentUserCitationRefusal(p HookPayload) string {
	if !p.IsSubagent() || !commandmod.HarnessCommandTools[p.ToolName] {
		return ""
	}
	var in struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(p.ToolInput, &in) != nil || (!strings.Contains(in.Command, "sr-file") && !strings.Contains(in.Command, "cite")) {
		return "" // no sr-file and no cite: nothing to check
	}
	calls, ok := commandmod.AndChain(in.Command)
	if !ok {
		return ""
	}
	record := ""
	for _, argv := range calls {
		inv, ok, err := grounding.FromArgv(argv)
		if !ok {
			if pureGlue[argv[0]] {
				continue
			}
			return ""
		}
		// A request that does not parse is the real run's to report; a cite
		// pointed elsewhere by --path is not this session's to predict.
		if err != nil || slices.ContainsFunc(argv, func(a string) bool { return a == "--path" || strings.HasPrefix(a, "--path=") }) {
			return ""
		}
		for _, req := range inv.Cites {
			if !slices.Contains(req.SourceTypes, transcript.SourceUser) {
				continue
			}
			if record == "" {
				if record = subagentRecord(p); record == "" {
					return ""
				}
			}
			_, err := transcript.ResolveCitation(record, req)
			var unresolved *transcript.ResolutionError
			if errors.As(err, &unresolved) {
				who := "sr-session trajectory cite"
				if inv.File != nil {
					who = "sr-file " + inv.File.Verb + ": nothing written"
				}
				return fmt.Sprintf("this command cannot succeed — %s: %s", who, unresolved.Msg)
			}
		}
		if inv.File != nil {
			return "" // the first call with an effect: nothing after it is checked
		}
	}
	return ""
}

// subagentRecord is the calling sub-agent's own record when it is on disk, so
// a citation that does not resolve is known to come from a sub-agent; "" when
// it cannot be found.
func subagentRecord(p HookPayload) string {
	path, err := recordOf(p)
	if err != nil || path == "" {
		return ""
	}
	if fi, err := os.Stat(path); err != nil || fi.IsDir() {
		return ""
	}
	return path
}
