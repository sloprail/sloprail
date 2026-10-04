package ruletest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/sloprail/sloprail/internal/dispatch"
	"github.com/sloprail/sloprail/internal/subbin"
)

// FileGuardRunner judges the project's file-guards over base..head of the
// repository at repo, as `sr-checks run` does, and returns what refused. It runs in
// the caller's process (the runner puts that process in the sandbox first), which is
// how a case's judges are stubbed on the real `sr-checks run` path.
type FileGuardRunner func(repo, base, head string, stderr io.Writer) (refusals []string, err error)

// Runner runs cases.
type Runner struct {
	// BinDir holds the sr-* binaries under test; "" finds them as the engine does.
	BinDir string
	// Live asks the real judges instead of the canned ones (judge-accuracy runs).
	Live bool
	// Keep leaves each sandbox in place and reports where.
	Keep bool
	// RunFileGuards is the file-guard evaluation (provided by sr-checks).
	RunFileGuards FileGuardRunner
}

// StepResult is what one trajectory step got.
type StepResult struct {
	Step    int    `json:"step"`
	What    string `json:"what"`
	Refused bool   `json:"refused"`
	Reason  string `json:"reason,omitempty"`
}

// CaseResult is the outcome of one case.
type CaseResult struct {
	Rule     string       `json:"rule"`
	Case     string       `json:"case"`
	Pass     bool         `json:"pass"`
	Problems []string     `json:"problems,omitempty"`
	Steps    []StepResult `json:"steps,omitempty"`
	// Verdict and Reason are the verdict the case judged: its range's, or its last
	// event's.
	Verdict Expect `json:"verdict,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Sandbox string `json:"sandbox,omitempty"`
	Elapsed string `json:"elapsed"`
	stderr  bytes.Buffer
}

// Stderr is what the engine wrote to stderr during the case.
func (r *CaseResult) Stderr() string { return r.stderr.String() }

const stepTimeout = 3 * time.Minute

// RunCase runs one case of a rule. all is the tree's rules, for `with:`.
func (r *Runner) RunCase(rule Rule, all []Rule, c Case) (res CaseResult) {
	start := time.Now()
	res = CaseResult{Rule: rule.FQN(), Case: c.Name}
	defer func() {
		res.Pass = len(res.Problems) == 0
		res.Elapsed = time.Since(start).Round(time.Millisecond).String()
	}()
	fail := func(format string, a ...any) { res.Problems = append(res.Problems, fmt.Sprintf(format, a...)) }

	sb, err := NewSandbox(r.BinDir, r.Live)
	if err != nil {
		fail("could not make the sandbox: %v", err)
		return
	}
	if r.Keep {
		sb.Keep()
		res.Sandbox = sb.Dir
	} else {
		defer sb.Cleanup()
	}

	dirs := map[string]string{string(rule.Nature) + "/" + rule.Name: rule.Dir}
	for _, w := range c.With {
		wr, err := find(all, w)
		if err != nil {
			fail("%v", err)
			return
		}
		dirs[string(wr.Nature)+"/"+wr.Name] = wr.Dir
	}
	for _, need := range append([]string(nil), rule.Requires...) {
		if _, have := dirs["context/"+need]; have {
			continue
		}
		cr, err := find(all, "context/"+need)
		if err != nil {
			fail("the rule requires context %q, which is no rule of this tree", need)
			return
		}
		dirs["context/"+need] = cr.Dir
	}
	if err := sb.InstallRules(dirs); err != nil {
		fail("could not install the rules: %v", err)
		return
	}
	if len(c.UserSays) > 0 || len(c.Trajectory) > 0 {
		if err := sb.WriteSessionRecord(c.UserSays); err != nil {
			fail("could not write the session record: %v", err)
			return
		}
	}
	projectRoot := filepath.Dir(rule.Root)
	caseEnv := []string{
		"SR_TEST_CASE_DIR=" + c.Dir, "SR_TEST_RULE_DIR=" + rule.Dir, "SR_TEST_PROJECT_ROOT=" + projectRoot,
	}
	if out, err := sb.Bash("bash "+shellQuote(filepath.Join(c.Dir, "setup.sh")), stepTimeout, caseEnv...); err != nil {
		fail("setup.sh failed: %v", err)
		_ = out
		return
	}

	table := &JudgeTable{Stubs: ExpandJudgeKeys(rule, c.Judges)}
	x := &execution{r: r, sb: sb, table: table, res: &res, caseEnv: caseEnv, c: c}
	if len(c.Trajectory) == 0 {
		x.runRange()
	} else {
		x.runTrajectory()
	}
	if !r.Live {
		for _, p := range table.Problems() {
			fail("%s", p)
		}
	}
	return
}

type execution struct {
	r       *Runner
	sb      *Sandbox
	table   *JudgeTable
	res     *CaseResult
	caseEnv []string
	c       Case
}

func (x *execution) fail(format string, a ...any) {
	x.res.Problems = append(x.res.Problems, fmt.Sprintf(format, a...))
}

// check compares what happened with what was expected.
func (x *execution) check(what string, want Expect, contains []string, refused bool, reason string) {
	got := ExpectPermit
	if refused {
		got = ExpectRefuse
	}
	x.res.Verdict, x.res.Reason = got, reason
	if want == "" {
		return
	}
	if got != want {
		if refused {
			x.fail("%s: expected the engine to %s, but it refused: %s", what, want, oneLine(reason))
		} else {
			x.fail("%s: expected the engine to %s, but it permitted", what, want)
		}
		return
	}
	if x.r.Live {
		return
	}
	for _, s := range contains {
		if !strings.Contains(reason, s) {
			x.fail("%s: the refusal does not contain %q; it says: %s", what, s, oneLine(reason))
		}
	}
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 400 {
		s = s[:400] + "…"
	}
	return s
}

func (x *execution) defaultBase() string {
	if x.c.Base != "" {
		return x.c.Base
	}
	if _, err := x.sb.Git("rev-parse", "--verify", "-q", "refs/tags/base"); err == nil {
		return "base"
	}
	return "HEAD~1"
}

// runRange is a case without a trajectory: the file-guards over base..HEAD.
func (x *execution) runRange() {
	base := x.defaultBase()
	refusals, err := x.fileGuards(base, "HEAD")
	if err != nil {
		x.fail("%v", err)
		return
	}
	x.check("the file-guards over "+base+"..HEAD", x.c.Expect, x.c.ReasonContains, len(refusals) > 0, strings.Join(refusals, "\n"))
}

// fileGuards runs the file-guards in this process, put inside the sandbox.
func (x *execution) fileGuards(base, head string) ([]string, error) {
	if x.r.RunFileGuards == nil {
		return nil, errors.New("internal: no file-guard runner")
	}
	var refusals []string
	err := inSandbox(x.sb, x.c.UserSays, func() error {
		if !x.r.Live {
			defer dispatch.InstallJudgeStub(x.table.Stub())()
		}
		var err error
		refusals, err = x.r.RunFileGuards(x.sb.Repo, base, head, &x.res.stderr)
		return err
	})
	return refusals, err
}

// inSandbox runs fn with this process's environment and working directory
// replaced by the sandbox's, and puts both back.
func inSandbox(sb *Sandbox, userSays []string, fn func() error) error {
	saved := os.Environ()
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	env := sb.Env()
	if sb.Session != "" {
		env = append(env, "CLAUDE_CODE_SESSION_ID="+SessionID)
	}
	os.Clearenv()
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		_ = os.Setenv(k, v)
	}
	defer func() {
		os.Clearenv()
		for _, kv := range saved {
			k, v, _ := strings.Cut(kv, "=")
			_ = os.Setenv(k, v)
		}
		_ = os.Chdir(wd)
	}()
	if err := os.Chdir(sb.Repo); err != nil {
		return err
	}
	return fn()
}

// replay runs one hook on one normalized event in a fresh `sr-session replay`
// process, as a harness runs one hook per call.
func (x *execution) replay(req ReplayRequest) (ReplayResponse, error) {
	var resp ReplayResponse
	bin := ""
	if x.r.BinDir != "" {
		bin = filepath.Join(x.r.BinDir, "sr-session")
	}
	if bin == "" {
		var err error
		if bin, err = subbin.Find("sr-session"); err != nil {
			return resp, err
		}
	}
	req.Live = x.r.Live
	if !x.r.Live {
		req.Judges = &JudgeTable{Stubs: x.table.Stubs}
	}
	body, err := json.Marshal(req)
	if err != nil {
		return resp, err
	}
	extra := append([]string{}, x.caseEnv...)
	if x.sb.Session != "" {
		extra = append(extra, "CLAUDE_CODE_SESSION_ID="+SessionID)
	}
	cmd := exec.Command(bin, "replay", "--sandbox", x.sb.Dir)
	cmd.Dir = x.sb.Repo
	cmd.Env = x.sb.Env(extra...)
	cmd.Stdin = bytes.NewReader(body)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &x.res.stderr
	if err := cmd.Run(); err != nil {
		return resp, fmt.Errorf("sr-session replay: %w", err)
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		return resp, fmt.Errorf("sr-session replay answered something that is not a response: %q", out.String())
	}
	if resp.Error != "" {
		return resp, errors.New(resp.Error)
	}
	if resp.Stderr != "" {
		x.res.stderr.WriteString(resp.Stderr)
	}
	x.table.Record(resp.JudgeCalls)
	return resp, nil
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
