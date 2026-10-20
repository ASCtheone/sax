package main

import (
	"os/exec"
	"sync"
)

// envLazyStart asks a daemon to start its session on first attach instead of
// right away. Attach launches (-xa, -ca) use it so a short command's output
// is still there when the client connects.
const envLazyStart = "SAX_LAZY_START"

func lazyStartEnv(attach bool) []string {
	if !attach {
		return nil
	}
	return []string{envLazyStart + "=1"}
}

// finishedDaemons records daemons launched by this process that have exited,
// and whether they exited cleanly. A detached command can finish (and its
// session close) before waitForSession first looks; that still counts as a
// successful start.
var finishedDaemons sync.Map // name -> bool (exit status 0)

// trackDaemon reaps a launched daemon in the background and records its exit.
func trackDaemon(name string, cmd *exec.Cmd) {
	finishedDaemons.Delete(name)
	go func() {
		err := cmd.Wait()
		finishedDaemons.Store(name, err == nil)
	}()
}

// daemonFinishedOK reports whether a daemon launched by this process has
// already exited with status 0.
func daemonFinishedOK(name string) bool {
	ok, found := finishedDaemons.Load(name)
	return found && ok.(bool)
}
