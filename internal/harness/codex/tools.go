package codex

import (
	"fmt"

	"github.com/sloprail/sloprail/internal/harness"
)

// MapToolRules implements harness.ToolPolicy: the allowed-tools vocabulary
// (harness/toolrules.go) onto what a `codex exec` run can be given.
//
// Codex has no per-tool permission list. What it has is a sandbox, a web-search mode,
// a network switch for the sandbox and feature flags, so each rule becomes one of
// those or is refused:
//
//	Read, Grep, Glob        nothing to add: the sandbox reads the disk (Codex reads through its shell)
//	Write, Edit, MultiEdit, NotebookEdit
//	                        satisfied by the writable directories the run is given (the sandbox
//	                        writes exactly those, which is NARROWER than Claude's unscoped Write);
//	                        refused when the run has none: there is nothing to write
//	Bash                    nothing to add: the shell always exists (it is how Codex reads at all)
//	Bash(<prefix>:*)        refused: Codex cannot allow a shell for some commands and not others
//	WebFetch                `sandbox_workspace_write.network_access=true`: the shell reaches the
//	                        network, which is WIDER than a fetch tool (Codex has no fetch-only
//	                        grant); needs a writable run, since a read-only sandbox has no network
//	WebSearch               `web_search="live"`
//	Agent                   leaves `multi_agent` on; without it the feature is disabled
//	mcp__*, Name(<scope>)   refused: no MCP servers are loaded (--ignore-user-config) and Codex has
//	                        no path or domain scopes
//
// Everything not allowed is switched off explicitly (network, web search, sub-agents)
// instead of left to Codex's defaults: `web_search` defaults to "cached" and
// `multi_agent` to on, so a judge granted nothing would otherwise search and spawn.
// A deny list can only name what Codex can turn off (WebFetch, WebSearch, Agent) or
// what is never on (an mcp__ tool); denying the shell, a file tool or a scoped rule is
// refused.
//
// Verified on codex 0.160.1: the network and search keys are the config schema's
// (codex-rs/config/src/config_toml.rs, protocol WebSearchMode), `multi_agent` is in
// `codex features list`.
func (Harness) MapToolRules(allow, deny []harness.ToolRule, ctx harness.ToolContext) ([]string, error) {
	var network, search, agents bool
	for _, r := range allow {
		switch {
		case r.Scoped:
			return nil, fmt.Errorf("%w: codex has no path or domain scopes (%s)", harness.ErrToolUnsupported, r.Raw)
		case r.Name == harness.ToolBash && r.Prefix != "":
			return nil, fmt.Errorf("%w: codex cannot allow the shell for some commands only (%s); use Bash for a sandboxed shell", harness.ErrToolUnsupported, r.Raw)
		case isFileWriteTool(r.Name):
			if ctx.WritableDirs == 0 {
				return nil, fmt.Errorf("%w: %s asks to write, but the run has no writable directory (give it one with --add-dir)", harness.ErrToolUnsupported, r.Raw)
			}
		case r.Name == harness.ToolRead, r.Name == harness.ToolGrep, r.Name == harness.ToolGlob, r.Name == harness.ToolBash:
		case r.Name == harness.ToolWebFetch:
			if ctx.WritableDirs == 0 {
				return nil, fmt.Errorf("%w: WebFetch needs network access, which Codex gives only in the writable sandbox, and the run has no writable directory", harness.ErrToolUnsupported)
			}
			network = true
		case r.Name == harness.ToolWebSearch:
			search = true
		case r.Name == harness.ToolAgent:
			agents = true
		default:
			return nil, fmt.Errorf("%w: codex has no tool %s", harness.ErrToolUnsupported, r.Raw)
		}
	}
	for _, r := range deny {
		switch {
		case r.Scoped || r.Prefix != "":
			return nil, fmt.Errorf("%w: codex cannot deny %s (no path, domain or command scopes)", harness.ErrToolUnsupported, r.Raw)
		case r.Name == harness.ToolWebFetch:
			if network {
				return nil, fmt.Errorf("%w: WebFetch is both allowed and denied", harness.ErrToolUnsupported)
			}
		case r.Name == harness.ToolWebSearch:
			if search {
				return nil, fmt.Errorf("%w: WebSearch is both allowed and denied", harness.ErrToolUnsupported)
			}
		case r.Name == harness.ToolAgent:
			if agents {
				return nil, fmt.Errorf("%w: Agent is both allowed and denied", harness.ErrToolUnsupported)
			}
		case len(r.Name) > 5 && r.Name[:5] == "mcp__":
			// no MCP server is loaded: nothing to deny
		default:
			return nil, fmt.Errorf("%w: codex cannot deny %s (it can switch off only WebFetch, WebSearch and Agent)", harness.ErrToolUnsupported, r.Raw)
		}
	}

	args := []string{
		"-c", fmt.Sprintf("sandbox_workspace_write.network_access=%t", network),
		"-c", `web_search="disabled"`,
	}
	if search {
		args[3] = `web_search="live"`
	}
	if !agents {
		args = append(args, "--disable", "multi_agent")
	}
	return args, nil
}

func isFileWriteTool(name string) bool {
	switch name {
	case harness.ToolWrite, harness.ToolEdit, harness.ToolMultiEdit, harness.ToolNotebookEdit:
		return true
	}
	return false
}
