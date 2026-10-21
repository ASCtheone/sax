//go:build windows

package server

import (
	"os/exec"
	"strconv"
	"time"
)

// killTree stops the processes rooted at roots and their descendants
// (taskkill /T). grace is unused: Windows has no polite signal for them.
func killTree(roots []int, _ time.Duration) int {
	n := 0
	for _, pid := range roots {
		if pid <= 0 {
			continue
		}
		if exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run() == nil {
			n++
		}
	}
	return n
}
