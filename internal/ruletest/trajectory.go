package ruletest

import (
	"fmt"
	"sort"
	"strings"
)

// isPostKind says whether an event kind is delivered at a Stop: what the cycle left
// behind (a Post file event, the tags the agent wrote) rather than a call about to run.
func isPostKind(kind string) bool { return strings.HasPrefix(kind, "Post") }

// runTrajectory plays trajectory.yaml against the engine's real hooks.
//
// The session begins first (the SessionStart hook: its baseline is the world
// setup.sh built), then each step in order. A Pre event is dispatched at once, as a
// harness asks before every tool call; a Post event is held and delivered with the
// next Stop of the agent it came from, as the engine delivers a cycle's Post events;
// `run:` steps change the repository between events; Stop runs the real Stop path,
// commits included.
func (x *execution) runTrajectory() {
	if _, err := x.replay(ReplayRequest{Op: OpStart}); err != nil {
		x.fail("the session could not be started: %v", err)
		return
	}
	last := lastVerdictStep(x.c.Trajectory)
	if x.c.Expect != "" {
		if last < 0 {
			x.fail("case.yaml `expect:` judges the trajectory's last event, and it has none")
			return
		}
		if x.c.Trajectory[last].Expect != "" {
			x.fail("case.yaml `expect:` and the last step's own `expect:` both judge the same step: keep one")
			return
		}
	}

	posts := map[string][]ReplayEvent{} // by agent ("" is the root)
	for i, st := range x.c.Trajectory {
		what := describeStep(i, st)
		want, contains := st.Expect, st.ReasonContains
		if i == last && x.c.Expect != "" {
			want, contains = x.c.Expect, x.c.ReasonContains
		}
		switch st.Type {
		case StepRun:
			if out, err := x.sb.Bash(st.Run, stepTimeout, x.caseEnv...); err != nil {
				x.fail("%s failed: %v", what, err)
				return
			} else if strings.TrimSpace(out) != "" {
				x.res.stderr.WriteString(out)
			}

		case StepChecksRun:
			base := st.ChecksBase
			if base == "" {
				base = x.defaultChecksBase()
			}
			refusals, err := x.fileGuards(base, "HEAD")
			if err != nil {
				x.fail("%s: %v", what, err)
				return
			}
			x.step(i, what, len(refusals) > 0, strings.Join(refusals, "\n"))
			x.check(what, want, contains, len(refusals) > 0, strings.Join(refusals, "\n"))

		case StepSubagent:
			op := OpSubagentStart
			req := ReplayRequest{Agent: st.ID}
			if st.Action == "stop" {
				op = OpSubagentStop
				req.Post = posts[st.ID]
			}
			req.Op = op
			resp, err := x.replay(req)
			if err != nil {
				x.fail("%s: %v", what, err)
				return
			}
			if st.Action == "stop" {
				if !resp.Refused {
					delete(posts, st.ID)
				}
				x.step(i, what, resp.Refused, resp.Reason)
				x.check(what, want, contains, resp.Refused, resp.Reason)
			}

		case StepEvent:
			if isPostKind(st.Kind) {
				if want != "" {
					x.fail("%s: a Post event is not dispatched on its own, it is delivered with the next Stop: put `expect:` on the Stop", what)
					return
				}
				posts[st.Agent] = append(posts[st.Agent], ReplayEvent{Kind: st.Kind, Fields: st.Event})
				break
			}
			req := ReplayRequest{Op: OpEvent, Event: ReplayEvent{Kind: st.Kind, Fields: st.Event}, Agent: st.Agent}
			if st.Kind == "Stop" {
				req.Post = posts[st.Agent]
			}
			resp, err := x.replay(req)
			if err != nil {
				x.fail("%s: %v", what, err)
				return
			}
			if st.Kind == "Stop" && !resp.Refused {
				delete(posts, st.Agent)
			}
			x.step(i, what, resp.Refused, resp.Reason)
			x.check(what, want, contains, resp.Refused, resp.Reason)
		}
		if len(st.Contexts) > 0 {
			x.assertContexts(fmt.Sprintf("after %s", what), st.Contexts)
		}
	}
	if len(x.c.Contexts) > 0 {
		x.assertContexts("after the trajectory", x.c.Contexts)
	}
}

func (x *execution) step(i int, what string, refused bool, reason string) {
	x.res.Steps = append(x.res.Steps, StepResult{Step: i + 1, What: what, Refused: refused, Reason: reason})
}

// lastVerdictStep is the last step that has a verdict: an event, a sub-agent stop or
// a checks_run. -1 when none does.
func lastVerdictStep(steps []Step) int {
	for i := len(steps) - 1; i >= 0; i-- {
		s := steps[i]
		switch {
		case s.Type == StepEvent && !isPostKind(s.Kind), s.Type == StepChecksRun, s.Type == StepSubagent && s.Action == "stop":
			return i
		}
	}
	return -1
}

func (x *execution) defaultChecksBase() string {
	if x.c.Base != "" {
		return x.c.Base
	}
	if _, err := x.sb.Git("rev-parse", "--verify", "-q", "refs/tags/base"); err == nil {
		return "base"
	}
	return "rules"
}

func (x *execution) assertContexts(when string, want map[string]string) {
	resp, err := x.replay(ReplayRequest{Op: OpContexts})
	if err != nil {
		x.fail("%s: the contexts could not be read: %v", when, err)
		return
	}
	names := make([]string, 0, len(want))
	for n := range want {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		st, ok := resp.Contexts[n]
		if !ok {
			x.fail("%s: context %q is not loaded here (list its rule under `with:`)", when, n)
			continue
		}
		got := "inactive"
		if st.Active {
			got = "active"
		}
		if got != want[n] {
			x.fail("%s: context %q is %s, expected %s", when, n, got, want[n])
		}
	}
}

func describeStep(i int, st Step) string {
	n := i + 1
	switch st.Type {
	case StepRun:
		first := strings.TrimSpace(strings.SplitN(st.Run, "\n", 2)[0])
		return fmt.Sprintf("step %d (run: %s)", n, first)
	case StepChecksRun:
		return fmt.Sprintf("step %d (checks_run)", n)
	case StepSubagent:
		return fmt.Sprintf("step %d (subagent %s %s)", n, st.Action, st.ID)
	case StepEvent:
		d := st.Kind
		if p, ok := st.Event["path"].(string); ok {
			d += " " + p
		} else if c, ok := st.Event["command"].(string); ok {
			d += " `" + c + "`"
		}
		if st.Agent != "" {
			d += " from " + st.Agent
		}
		return fmt.Sprintf("step %d (%s)", n, d)
	}
	return fmt.Sprintf("step %d", n)
}
