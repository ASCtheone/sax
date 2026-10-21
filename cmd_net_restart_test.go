package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asc/sax/internal/ipc"
	"github.com/asc/sax/internal/vnet"
)

func TestNetRestart(t *testing.T) {
	fakeShim(t)
	home := withGlobal(t, globalProjects)
	s := vnet.NewStore(t.TempDir())
	run := newFakeRunner()
	elsewhere := filepath.Join(home, "elsewhere")
	t.Chdir(elsewhere)

	var out bytes.Buffer
	if err := netRestart(&out, s, run, []string{"dev1"}, logOpts{}); err == nil || !strings.Contains(err.Error(), "not up") {
		t.Errorf("restart before up: %v", err)
	}
	if err := netUp(&out, s, run, projectRef{Name: "dev1"}, false); err != nil {
		t.Fatal(err)
	}

	// One app, by project name, from another folder; the file is re-read.
	path := globalEnvFile()
	data, _ := os.ReadFile(path)
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), `"npm start"`, `"npm run dev"`, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := netRestart(&out, s, run, []string{"dev1", "api"}, logOpts{}); err != nil {
		t.Fatal(err)
	}
	if len(run.replaced) != 1 || run.replaced[0].Name != "api" || run.replaced[0].Input != "npm run dev" {
		t.Errorf("replaced = %+v; want api with the edited command", run.replaced)
	}
	if !strings.Contains(out.String(), "api: restarted (2 old processes stopped)") {
		t.Errorf("output:\n%s", out.String())
	}

	// Every app, with the <project>.<app> and bare forms too.
	run.replaced = nil
	for _, pos := range [][]string{{"dev1"}, {"dev1.web"}} {
		if err := netRestart(&out, s, run, pos, logOpts{}); err != nil {
			t.Fatalf("%v: %v", pos, err)
		}
	}
	if len(run.replaced) != 3 {
		t.Errorf("replaced %d tabs, want 2 (all) + 1 (web)", len(run.replaced))
	}
	t.Chdir(filepath.Join(home, "code/dev1"))
	if err := netRestart(&out, s, run, []string{"web"}, logOpts{}); err != nil {
		t.Errorf("bare app of this folder's project: %v", err)
	}

	if err := netRestart(&out, s, run, []string{"dev1", "ghost"}, logOpts{}); err == nil || !strings.Contains(err.Error(), "api, web") {
		t.Errorf("unknown app: %v", err)
	}
	if err := netRestart(&out, s, run, []string{"nope"}, logOpts{}); err == nil {
		t.Error("unknown project should fail")
	}

	run.replaceErr = errReplaceUnsupported
	if err := netRestart(&out, s, run, []string{"dev1", "api"}, logOpts{}); err == nil || !strings.Contains(err.Error(), "sax net up dev1") {
		t.Errorf("old daemon: %v", err)
	}
}

// An app added to the file after up is started by restart, and -f follows
// the restarted apps' logs.
func TestNetRestart_NewAppAndFollow(t *testing.T) {
	fakeShim(t)
	withGlobal(t, globalProjects)
	s := vnet.NewStore(t.TempDir())
	run := newFakeRunner()
	if err := netUp(&bytes.Buffer{}, s, run, projectRef{Name: "dev2"}, false); err != nil {
		t.Fatal(err)
	}
	path := globalEnvFile()
	data, _ := os.ReadFile(path)
	grown := strings.Replace(string(data), `      api: { cmd: "go run ." }`, "      api: { cmd: \"go run .\" }\n      worker: { cmd: \"go run ./worker\" }", 1)
	if err := os.WriteFile(path, []byte(grown), 0o644); err != nil {
		t.Fatal(err)
	}

	var followed ipc.LogsRequest
	orig := streamLogs
	t.Cleanup(func() { streamLogs = orig })
	streamLogs = func(_ string, req ipc.LogsRequest, _ func([]string), _ func(ipc.LogLines) error) (string, error) {
		followed = req
		return "done", nil
	}

	var out bytes.Buffer
	if err := netRestart(&out, s, run, []string{"dev2", "worker"}, logOpts{Follow: true, Lines: 100}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "worker: started") || len(run.tabs["d2"]) != 2 {
		t.Errorf("new app should be started: %s tabs=%v", out.String(), run.tabs["d2"])
	}
	if !followed.Follow || len(followed.Tabs) != 1 || followed.Tabs[0] != "worker" {
		t.Errorf("-f should follow the restarted app: %+v", followed)
	}
}
