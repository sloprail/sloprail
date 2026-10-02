package checkcache

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
)

type gitRunner struct{ dir string }

func (g gitRunner) run(stdin []byte, env []string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = g.dir
	cmd.Env = append(cmd.Environ(), env...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return out.Bytes(), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.Bytes(), nil
}

func (g gitRunner) str(args ...string) (string, error) {
	b, err := g.run(nil, nil, args...)
	return strings.TrimSpace(string(b)), err
}

// batch is one long-lived `git cat-file --batch` process.
type batch struct {
	cmd *exec.Cmd
	in  io.WriteCloser
	out *bufio.Reader
}

func (g gitRunner) startBatch() (*batch, error) {
	cmd := exec.Command("git", "cat-file", "--batch")
	cmd.Dir = g.dir
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &batch{cmd: cmd, in: in, out: bufio.NewReaderSize(out, 1<<20)}, nil
}

func (b *batch) read(spec string) ([]byte, error) {
	if _, err := fmt.Fprintln(b.in, spec); err != nil {
		return nil, err
	}
	line, err := b.out.ReadString('\n')
	if err != nil {
		return nil, err
	}
	f := strings.Fields(line)
	if len(f) == 2 && f[1] == "missing" {
		return nil, fmt.Errorf("%w: object %s missing", ErrCorrupt, spec)
	}
	if len(f) != 3 {
		return nil, fmt.Errorf("cat-file: unexpected header %q", line)
	}
	n, err := strconv.Atoi(f[2])
	if err != nil {
		return nil, err
	}
	buf := make([]byte, n+1)
	if _, err := io.ReadFull(b.out, buf); err != nil {
		return nil, err
	}
	return buf[:n], nil
}

func (b *batch) close() {
	b.in.Close()
	_ = b.cmd.Wait()
}
