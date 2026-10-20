package main

import (
	"os/exec"
	"testing"
	"time"
)

func TestDaemonTracking(t *testing.T) {
	ok := exec.Command("true")
	if err := ok.Start(); err != nil {
		t.Fatal(err)
	}
	trackDaemon("track-ok", ok)

	bad := exec.Command("false")
	if err := bad.Start(); err != nil {
		t.Fatal(err)
	}
	trackDaemon("track-bad", bad)

	deadline := time.Now().Add(3 * time.Second)
	for !daemonFinishedOK("track-ok") {
		if time.Now().After(deadline) {
			t.Fatal("successful daemon exit not recorded")
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	if daemonFinishedOK("track-bad") {
		t.Error("failed daemon must not count as finished OK")
	}
	if daemonFinishedOK("never-launched") {
		t.Error("unknown daemon must not count as finished OK")
	}
}

func TestLazyStartEnv(t *testing.T) {
	if got := lazyStartEnv(true); len(got) != 1 || got[0] != envLazyStart+"=1" {
		t.Errorf("lazyStartEnv(true) = %v", got)
	}
	if got := lazyStartEnv(false); got != nil {
		t.Errorf("lazyStartEnv(false) = %v", got)
	}
}
