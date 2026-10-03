package gitrepo

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

// One `git cat-file --batch` per repository, kept for the process: a verify reads hundreds of
// blobs by object id, and a git process each is the cost that mattered, not the bytes.

type batcher struct {
	mu  sync.Mutex
	cmd *exec.Cmd
	in  io.WriteCloser
	out *bufio.Reader
}

var (
	batchers  sync.Map // dir -> *batcher
	batchMemo sync.Map // dir\x00oid -> string
)

// BlobByID is the raw bytes of the object oid (no attribute filters), read through one long-lived
// `git cat-file --batch` for dir. Identical ids are read once.
func BlobByID(dir, oid string) (string, error) {
	if !isObjectName(oid) {
		return "", fmt.Errorf("gitrepo: %q is not an object id", oid)
	}
	return blobRaw(dir, oid)
}

// BlobRaw is BlobByID for any object name `git cat-file` accepts, such as <sha>:<path> or
// <sha>^1:<path>: raw bytes, no attribute filters. For callers that compare or scan, never
// ones that hand the text to a check.
func BlobRaw(dir, spec string) (string, error) {
	if strings.ContainsAny(spec, "\n\x00") {
		return "", fmt.Errorf("gitrepo: %q is not an object name", spec)
	}
	return blobRaw(dir, spec)
}

func blobRaw(dir, oid string) (string, error) {
	key := dir + "\x00" + oid
	if v, ok := batchMemo.Load(key); ok {
		return v.(string), nil
	}
	v, _ := batchers.LoadOrStore(dir, &batcher{})
	b := v.(*batcher)
	b.mu.Lock()
	defer b.mu.Unlock()
	content, err := b.read(dir, oid)
	if err != nil {
		b.stop() // a broken stream is not reused
		if content, err = b.read(dir, oid); err != nil {
			b.stop()
			return "", err
		}
	}
	batchMemo.Store(key, content)
	return content, nil
}

func (b *batcher) start(dir string) error {
	if b.cmd != nil {
		return nil
	}
	cmd := exec.Command("git", "-C", dir, "cat-file", "--batch")
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	b.cmd, b.in, b.out = cmd, in, bufio.NewReaderSize(out, 1<<16)
	return nil
}

func (b *batcher) stop() {
	if b.cmd == nil {
		return
	}
	_ = b.in.Close()
	_ = b.cmd.Process.Kill()
	_ = b.cmd.Wait()
	b.cmd, b.in, b.out = nil, nil, nil
}

func (b *batcher) read(dir, oid string) (string, error) {
	if err := b.start(dir); err != nil {
		return "", err
	}
	if _, err := io.WriteString(b.in, oid+"\n"); err != nil {
		return "", err
	}
	header, err := b.out.ReadString('\n')
	if err != nil {
		return "", err
	}
	f := strings.Fields(header)
	if len(f) == 2 && f[1] == "missing" {
		return "", fmt.Errorf("gitrepo: object %s is missing", short(oid))
	}
	if len(f) != 3 {
		return "", fmt.Errorf("gitrepo: unreadable cat-file header %q", header)
	}
	n, err := strconv.Atoi(f[2])
	if err != nil {
		return "", err
	}
	buf := make([]byte, n+1) // the object and the newline git adds
	if _, err := io.ReadFull(b.out, buf); err != nil {
		return "", err
	}
	return string(buf[:n]), nil
}

// CloseBatches ends every long-lived cat-file; the process ending does as well.
func CloseBatches() {
	batchers.Range(func(_, v any) bool {
		b := v.(*batcher)
		b.mu.Lock()
		b.stop()
		b.mu.Unlock()
		return true
	})
}
