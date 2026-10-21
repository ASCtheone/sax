//go:build !windows

package server

import (
	"bufio"
	"bytes"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// killTree stops the processes rooted at roots and all their descendants
// (an app tab's shell, the dev server it runs, that server's workers): it
// sends SIGTERM and SIGHUP (interactive shells ignore SIGTERM), waits up to
// grace for them to exit, then SIGKILLs what is left. It returns how many
// processes it signalled. Processes that left the tree (daemonized) are not
// found.
func killTree(roots []int, grace time.Duration) int {
	pids := descendants(roots)
	if len(pids) == 0 {
		return 0
	}
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGHUP} {
		for _, pid := range pids {
			_ = syscall.Kill(pid, sig)
		}
	}
	if waitGone(pids, grace) {
		return len(pids)
	}
	for _, pid := range pids {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	waitGone(pids, time.Second)
	return len(pids)
}

type procInfo struct {
	ppid   int
	zombie bool
}

// processTable reads every process's parent and whether it is a zombie.
func processTable() map[int]procInfo {
	out, err := exec.Command("ps", "-A", "-o", "pid=,ppid=,stat=").Output()
	if err != nil {
		return nil
	}
	table := map[int]procInfo{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 3 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		if err1 != nil || err2 != nil {
			continue
		}
		table[pid] = procInfo{ppid: ppid, zombie: strings.HasPrefix(f[2], "Z")}
	}
	return table
}

// descendants returns the live roots and every live process below them.
func descendants(roots []int) []int {
	table := processTable()
	children := map[int][]int{}
	for pid, p := range table {
		children[p.ppid] = append(children[p.ppid], pid)
	}
	var out []int
	seen := map[int]bool{}
	queue := append([]int{}, roots...)
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if pid <= 1 || seen[pid] {
			continue
		}
		seen[pid] = true
		if p, ok := table[pid]; ok && !p.zombie {
			out = append(out, pid)
		}
		queue = append(queue, children[pid]...)
	}
	return out
}

// waitGone waits until none of pids is running (zombies count as gone).
func waitGone(pids []int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		table := processTable()
		alive := false
		for _, pid := range pids {
			if p, ok := table[pid]; ok && !p.zombie {
				alive = true
				break
			}
		}
		if !alive {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}
