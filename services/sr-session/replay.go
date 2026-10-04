package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sloprail/sloprail/internal/commandmod"
	dispatchcore "github.com/sloprail/sloprail/internal/dispatch"
	"github.com/sloprail/sloprail/internal/event"
	"github.com/sloprail/sloprail/internal/filemod"
	"github.com/sloprail/sloprail/internal/module"
	"github.com/sloprail/sloprail/internal/module/modules"
	"github.com/sloprail/sloprail/internal/ruletest"
	"github.com/sloprail/sloprail/internal/tagmod"
	"github.com/sloprail/sloprail/internal/tooluse"
)

// `sr-session replay` runs the engine's REAL hooks on normalized events, for
// `sr-checks test`. It is hidden, takes one request on stdin and answers one JSON
// response, and — like a hook — is one process per call, so what survives between
// calls is what the engine itself persists (the session's state.db, its record).
//
// It acts only inside a rule-test sandbox: a directory holding ruletest.MarkerFile
// that is also the process's home, state and working tree. Pointed anywhere else
// it refuses. That is what lets it stub the judges (dispatch.InstallJudgeStub) and
// inject events without being a way to forge a real session's state: it can only
// write to a world made to be thrown away.
func newSessionReplayCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "replay --sandbox <dir>",
		Short:  "Run a hook on a normalized event inside a rule-test sandbox (used by sr-checks test)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE:   runReplay,
	}
	cmd.Flags().String("sandbox", "", "the rule-test sandbox directory (required)")
	return cmd
}

func runReplay(cmd *cobra.Command, _ []string) error {
	resp := ruletest.ReplayResponse{}
	err := replayOnce(cmd, &resp)
	if err != nil {
		resp.Error = err.Error()
	}
	return json.NewEncoder(cmd.OutOrStdout()).Encode(resp)
}

func replayOnce(cmd *cobra.Command, resp *ruletest.ReplayResponse) error {
	dir, _ := cmd.Flags().GetString("sandbox")
	sb, err := ruletest.OpenSandbox(dir)
	if err != nil {
		return err
	}
	var req ruletest.ReplayRequest
	if err := json.NewDecoder(cmd.InOrStdin()).Decode(&req); err != nil {
		return fmt.Errorf("the request on stdin is not JSON: %w", err)
	}
	if err := insideSandbox(sb, req.Live); err != nil {
		return err
	}

	table := req.Judges
	if table == nil {
		table = &ruletest.JudgeTable{}
	}
	if !req.Live {
		defer dispatchcore.InstallJudgeStub(table.Stub())()
	}
	defer func() { resp.JudgeCalls = table.Calls() }()

	reg, err := modules.Registry()
	if err != nil {
		return err
	}
	root := replayPayload(sb, req.Agent)

	switch req.Op {
	case ruletest.OpStart:
		if err := writeRoot(sb); err != nil {
			return err
		}
		_, errs, rerr := replayHook(newSessionStartCmd(), root)
		resp.Stderr = errs
		if rerr != nil {
			return rerr
		}
		// The agent's first tool call: it is when the engine records where the session's
		// folder began (registerStartFolder), and every `run:` step of a trajectory stands
		// for tool calls made after it. A call that names no event dispatches nothing.
		replayInjected = &replayEvents{}
		_, errs, rerr = replayHook(newSessionPreToolCmd(), root)
		resp.Stderr += errs
		return rerr

	case ruletest.OpContexts:
		return replayContexts(cmd, sb, reg, root, resp)

	case ruletest.OpSubagentStart:
		agentPayload, err := replayAgentPayload(sb, req.Agent)
		if err != nil {
			return err
		}
		_, errs, rerr := replayHook(newSessionSubagentStartCmd(), agentPayload)
		resp.Stderr = errs
		return rerr

	case ruletest.OpSubagentStop:
		agentPayload, err := replayAgentPayload(sb, req.Agent)
		if err != nil {
			return err
		}
		injected, err := injectPost(reg, req.Post, sb.Repo)
		if err != nil {
			return err
		}
		replayInjected = injected
		out, errs, rerr := replayHook(newSessionSubagentStopCmd(), agentPayload)
		resp.Stderr = errs
		resp.Refused, resp.Reason = refusalOf(out)
		return rerr

	case ruletest.OpEvent:
		p := root
		if req.Agent != "" {
			if p, err = replayAgentPayload(sb, req.Agent); err != nil {
				return err
			}
		}
		if req.Event.Kind == "Stop" {
			injected, err := injectPost(reg, req.Post, sb.Repo)
			if err != nil {
				return err
			}
			replayInjected = injected
			out, errs, rerr := replayHook(newSessionStopCmd(), p)
			resp.Stderr = errs
			resp.Refused, resp.Reason = refusalOf(out)
			return rerr
		}
		ev, err := ruletest.BuildEvent(reg, req.Event.Kind, req.Event.Fields, sb.Repo)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(ev.Kind, "Pre") {
			return fmt.Errorf("%s is delivered at the next Stop (a Post event is what the cycle left behind), not dispatched on its own", ev.Kind)
		}
		replayInjected = &replayEvents{pre: []event.Event{ev}}
		setPendingCall(&p, ev, sb.Repo)
		// The call is in the session's record before its pre-tool hook runs, as a harness
		// writes it: a rule asking what the agent did (a skill loaded, a file read) sees it.
		var input map[string]any
		_ = json.Unmarshal(p.ToolInput, &input)
		if err := sb.AppendToolUse(req.Agent, p.ToolName, input); err != nil {
			return fmt.Errorf("the call could not be recorded in the session: %w", err)
		}
		out, errs, rerr := replayHook(newSessionPreToolCmd(), p)
		resp.Stderr = errs
		resp.Refused, resp.Reason = refusalOf(out)
		return rerr
	}
	return fmt.Errorf("unknown op %q", req.Op)
}

// insideSandbox refuses to run unless this process lives in the sandbox: its
// working tree is the sandbox's repository, and (outside a live-judge run, which
// keeps the user's home for the harness's login) its home and engine state are the
// sandbox's too.
func insideSandbox(sb *ruletest.Sandbox, live bool) error {
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	if r, err := filepath.EvalSymlinks(wd); err == nil {
		wd = r
	}
	if wd != sb.Repo && !strings.HasPrefix(wd, sb.Repo+string(filepath.Separator)) {
		return fmt.Errorf("replay runs in the sandbox's repository (%s), not in %s", sb.Repo, wd)
	}
	within := func(p string) bool {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			p = r
		}
		return p == sb.Dir || strings.HasPrefix(p, sb.Dir+string(filepath.Separator))
	}
	if !within(os.Getenv("XDG_DATA_HOME")) {
		return fmt.Errorf("replay keeps the engine's state in the sandbox: XDG_DATA_HOME is %q, not under %s", os.Getenv("XDG_DATA_HOME"), sb.Dir)
	}
	if !live && !within(os.Getenv("HOME")) {
		return fmt.Errorf("replay runs under the sandbox's home: HOME is %q, not under %s", os.Getenv("HOME"), sb.Dir)
	}
	return nil
}

// replayPayload is the root session's hook payload: where it is, and its record.
func replayPayload(sb *ruletest.Sandbox, agent string) HookPayload {
	return HookPayload{
		Cwd: sb.Repo, SessionID: ruletest.SessionID, TranscriptPath: sb.SessionRecordPath(), Source: "startup",
	}
}

// replayAgentPayload is a sub-agent's: the session's, plus the agent's own identity
// and record, as a harness reports them.
func replayAgentPayload(sb *ruletest.Sandbox, agent string) (HookPayload, error) {
	p := replayPayload(sb, "")
	p.Source = ""
	rec, err := sb.EnsureSubagentRecord(agent)
	if err != nil {
		return p, err
	}
	p.AgentID, p.AgentTranscriptPath, p.AgentType = agent, rec, "general-purpose"
	return p, nil
}

// writeRoot makes sure the session record exists before the session starts.
func writeRoot(sb *ruletest.Sandbox) error {
	if _, err := os.Stat(sb.SessionRecordPath()); err == nil {
		return nil
	}
	return sb.WriteSessionRecord(nil)
}

// setPendingCall fills the payload's tool call, so what reads the call itself (a
// citation chained in front of a command) sees what a harness would have reported.
func setPendingCall(p *HookPayload, ev event.Event, repo string) {
	input := map[string]any{}
	switch ev.Kind {
	case commandmod.KindPreInvoke:
		p.ToolName = "Bash"
		input["command"], _ = ev.Fields[commandmod.FieldRaw].(string)
	case filemod.KindPreCreate, filemod.KindPreUpdate:
		p.ToolName = "Write"
		path, _ := ev.Fields[filemod.FieldPath].(string)
		input["file_path"] = filepath.Join(repo, path)
		input["content"], _ = ev.Fields[filemod.FieldNewContent].(string)
	case filemod.KindPreDelete:
		p.ToolName = "Bash"
		path, _ := ev.Fields[filemod.FieldPath].(string)
		input["command"] = "rm " + path
	case tooluse.KindPreToolUse:
		p.ToolName, _ = ev.Fields[tooluse.FieldTool].(string)
		input, _ = ev.Fields[tooluse.FieldInput].(map[string]any)
		// A harness reports a file tool's path absolute; a case writes it from the repository.
		if fp, ok := input["file_path"].(string); ok && fp != "" && !filepath.IsAbs(fp) {
			abs := map[string]any{}
			for k, v := range input {
				abs[k] = v
			}
			abs["file_path"] = filepath.Join(repo, fp)
			input = abs
		}
	}
	p.ToolInput, _ = json.Marshal(input)
}

// injectPost builds the cycle's Post events: file kinds and tags, each as the
// module would have produced them.
func injectPost(reg *module.Registry, post []ruletest.ReplayEvent, repo string) (*replayEvents, error) {
	out := &replayEvents{}
	for _, pe := range post {
		ev, err := ruletest.BuildEvent(reg, pe.Kind, pe.Fields, repo)
		if err != nil {
			return nil, err
		}
		switch ev.Kind {
		case tagmod.KindPostTagWrite:
			out.tags = append(out.tags, ev)
		default:
			out.post = append(out.post, ev)
		}
	}
	return out, nil
}

// replayHook runs a hook command on a payload, as the harness would: the payload on
// stdin, the decision on stdout.
func replayHook(c *cobra.Command, p HookPayload) (stdout, stderr string, err error) {
	b, err := json.Marshal(p)
	if err != nil {
		return "", "", err
	}
	var out, errb bytes.Buffer
	c.SetIn(bytes.NewReader(b))
	c.SetOut(&out)
	c.SetErr(&errb)
	err = c.RunE(c, nil)
	return out.String(), errb.String(), err
}

// refusalOf reads a hook's stdout for the decision: a pre-tool deny or a Stop's block.
func refusalOf(stdout string) (bool, string) {
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var m map[string]any
		if json.Unmarshal([]byte(line), &m) != nil {
			continue
		}
		if m["decision"] == "block" {
			r, _ := m["reason"].(string)
			return true, r
		}
		if h, ok := m["hookSpecificOutput"].(map[string]any); ok && h["permissionDecision"] == "deny" {
			r, _ := h["permissionDecisionReason"].(string)
			return true, r
		}
	}
	return false, ""
}

// replayContexts answers each loaded context's state, read from the session's store.
func replayContexts(cmd *cobra.Command, sb *ruletest.Sandbox, reg *module.Registry, p HookPayload, resp *ruletest.ReplayResponse) error {
	var errb bytes.Buffer
	quiet := &cobra.Command{}
	quiet.SetErr(&errb)
	quiet.SetOut(&errb)
	loaded := newNatureDeclarations(quiet, sb.Repo, reg)
	store := natureStore(quiet, p)
	if store != nil {
		defer store.Close()
	}
	m := loadContextMap(quiet, store, loaded.Contexts)
	resp.Contexts = map[string]ruletest.ContextState{}
	for name, st := range m {
		resp.Contexts[name] = ruletest.ContextState{Active: st.Active, Payload: st.Payload}
	}
	resp.Stderr = errb.String()
	return nil
}
