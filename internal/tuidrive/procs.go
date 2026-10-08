package tuidrive

import (
	"os/exec"
	"strconv"
	"strings"
)

// descendants are the pids of every process below root, from `ps -A -o pid=,ppid=` (the form
// macOS and Linux share). A ps that cannot run yields none: the driver then has the record
// and the screen to go on, and never reads that as a process still running.
func descendants(root int) map[int]bool {
	out, err := exec.Command("ps", "-A", "-o", "pid=,ppid=").Output()
	if err != nil {
		return nil
	}
	children := map[int][]int{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		pid, e1 := strconv.Atoi(f[0])
		ppid, e2 := strconv.Atoi(f[1])
		if e1 != nil || e2 != nil {
			continue
		}
		children[ppid] = append(children[ppid], pid)
	}
	found := map[int]bool{}
	for queue := []int{root}; len(queue) > 0; {
		p := queue[0]
		queue = queue[1:]
		for _, c := range children[p] {
			if !found[c] {
				found[c] = true
				queue = append(queue, c)
			}
		}
	}
	return found
}

// newSince reports whether the tree below root holds a process that is not in known.
func newSince(root int, known map[int]bool) bool {
	for pid := range descendants(root) {
		if !known[pid] {
			return true
		}
	}
	return false
}
