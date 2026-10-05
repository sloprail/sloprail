package prrerun

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const failing = `[{"name":"build","bucket":"pass","link":"https://github.com/o/r/actions/runs/1/job/9"},` +
	`{"name":"sr-checks verify","bucket":"fail","link":"https://github.com/o/r/actions/runs/4242/job/7"}]`

func TestFailedVerifyRun(t *testing.T) {
	id, ok := FailedVerifyRun(failing)
	assert.True(t, ok)
	assert.Equal(t, "4242", id)

	for name, in := range map[string]string{
		"passing":  `[{"name":"sr-checks verify","bucket":"pass","link":"https://x/actions/runs/5/job/1"}]`,
		"pending":  `[{"name":"sr-checks verify","bucket":"pending","link":"https://x/actions/runs/5/job/1"}]`,
		"other":    `[{"name":"build","bucket":"fail","link":"https://x/actions/runs/5/job/1"}]`,
		"no run":   `[{"name":"sr-checks verify","bucket":"fail","link":"https://x/status"}]`,
		"not json": `nope`,
		"empty":    ``,
	} {
		_, ok := FailedVerifyRun(in)
		assert.False(t, ok, name)
	}
}

func TestPRNumbers(t *testing.T) {
	got, err := PRNumbers(`[{"number":7},{"number":9}]`)
	require.NoError(t, err)
	assert.Equal(t, []int{7, 9}, got)
	got, err = PRNumbers(`[]`)
	require.NoError(t, err)
	assert.Empty(t, got)
	_, err = PRNumbers(`x`)
	assert.Error(t, err)
}

// fake answers commands by the prefix of their joined text, recording every call.
type fake struct {
	answers map[string]string
	errs    map[string]error
	calls   []string
}

func (f *fake) run(_, name string, args ...string) (string, error) {
	line := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, line)
	for prefix, err := range f.errs {
		if strings.HasPrefix(line, prefix) {
			return f.answers[prefix], err
		}
	}
	for prefix, out := range f.answers {
		if strings.HasPrefix(line, prefix) {
			return out, nil
		}
	}
	return "", nil
}

func base() *fake {
	return &fake{answers: map[string]string{
		"git remote":      "git@github.com:o/r.git\n",
		"git branch":      "feat/x\n",
		"git rev-parse":   "abc\n",
		"gh pr list":      `[{"number":12}]`,
		"gh pr checks 12": failing,
	}, errs: map[string]error{}}
}

func (f *fake) rerun() bool {
	for _, c := range f.calls {
		if strings.HasPrefix(c, "gh run rerun") {
			return true
		}
	}
	return false
}

func TestRerun_ReRunsTheFailedJob(t *testing.T) {
	f := base()
	var w bytes.Buffer
	Rerun(&w, f.run, "/d", "abc")
	assert.Contains(t, f.calls, "gh run rerun 4242 --failed")
	assert.Contains(t, w.String(), "#12")
}

func TestRerun_ReadsAFailingChecksListEvenWhenGhExitsNonZero(t *testing.T) {
	f := base()
	f.errs["gh pr checks 12"] = errors.New("exit status 1")
	var w bytes.Buffer
	Rerun(&w, f.run, "/d", "abc")
	assert.True(t, f.rerun())
}

func TestRerun_Silent(t *testing.T) {
	for name, tweak := range map[string]func(*fake){
		"no origin":       func(f *fake) { f.errs["git remote"] = errors.New("no") },
		"detached head":   func(f *fake) { f.answers["git branch"] = "\n" },
		"head not tip":    func(f *fake) { f.answers["git rev-parse"] = "def\n" },
		"no pull request": func(f *fake) { f.answers["gh pr list"] = `[]` },
		"verify passing": func(f *fake) {
			f.answers["gh pr checks 12"] = `[{"name":"sr-checks verify","bucket":"pass","link":"https://x/actions/runs/1/job/1"}]`
		},
	} {
		f := base()
		tweak(f)
		var w bytes.Buffer
		Rerun(&w, f.run, "/d", "abc")
		assert.False(t, f.rerun(), name)
		assert.Empty(t, w.String(), name)
	}
}

func TestRerun_GhUnavailableIsOneLine(t *testing.T) {
	for name, tweak := range map[string]func(*fake){
		"list fails":   func(f *fake) { f.errs["gh pr list"] = errors.New("not logged in") },
		"rerun fails":  func(f *fake) { f.errs["gh run rerun"] = errors.New("403") },
		"checks fails": func(f *fake) { f.answers["gh pr checks 12"] = ""; f.errs["gh pr checks 12"] = errors.New("x") },
	} {
		f := base()
		tweak(f)
		var w bytes.Buffer
		Rerun(&w, f.run, "/d", "abc")
		assert.Equal(t, 1, strings.Count(w.String(), "\n"), name)
		assert.Contains(t, w.String(), "gh run rerun <run-id> --failed", name)
	}
}
