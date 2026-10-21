package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/asc/sax/internal/ipc"
	"github.com/asc/sax/internal/vnet"
)

func TestLogLine(t *testing.T) {
	if got := logLine("api", 5, "\x1b[31mboom\x1b[0m", false); got != "api   | boom" {
		t.Errorf("plain = %q", got)
	}
	colored := logLine("api", 3, "\x1b[31mboom", true)
	if !strings.HasPrefix(colored, "\x1b[") || !strings.Contains(colored, "api | \x1b[0m\x1b[31mboom") || !strings.HasSuffix(colored, "\x1b[0m") {
		t.Errorf("colored = %q", colored)
	}
	if logLine("api", 3, "x", true)[:5] != logLine("api", 9, "y", true)[:5] {
		t.Error("an app should keep its color")
	}
}

func TestLogOptsFromArgs(t *testing.T) {
	o, err := logOptsFromArgs([]string{"dev1", "-f", "-n", "20", "--no-color"})
	if err != nil || !o.Follow || o.Lines != 20 || o.Color {
		t.Errorf("opts = %+v, %v", o, err)
	}
	if o, _ := logOptsFromArgs(nil); o.Follow || o.Lines != defaultLogLines {
		t.Errorf("defaults = %+v", o)
	}
	if o, _ := logOptsFromArgs([]string{"-n", "all"}); o.Lines < 1000 {
		t.Errorf("-n all = %d", o.Lines)
	}
	if _, err := logOptsFromArgs([]string{"-n", "lots"}); err == nil {
		t.Error("-n lots should fail")
	}
	if got := netPositional([]string{"dev1", "-n", "20", "api", "-f"}); !reflect.DeepEqual(got, []string{"dev1", "api"}) {
		t.Errorf("positional = %v (the -n value must not become an app)", got)
	}
}

// Every form of `sax net logs` resolves from any folder.
func TestLogsTarget(t *testing.T) {
	home := withGlobal(t, globalProjects)
	s := vnet.NewStore(t.TempDir())
	r, _ := vnet.NewRouter("v1", "10.12.12.0/24", "")
	_ = s.Create(r)
	t.Chdir(filepath.Join(home, "code/dev1"))

	cases := []struct {
		pos    []string
		router string
		apps   []string
	}{
		{nil, "dev1", nil},            // this folder's project
		{[]string{"dev2"}, "d2", nil}, // project name, other router name
		{[]string{"dev2", "api"}, "d2", []string{"api"}},
		{[]string{"dev2.api"}, "d2", []string{"api"}},
		{[]string{"v1", "web"}, "v1", []string{"web"}}, // a router name
		{[]string{"web"}, "dev1", []string{"web"}},     // an app of this folder's project
	}
	for _, c := range cases {
		router, apps, err := logsTarget(s, c.pos)
		if err != nil || router != c.router || !reflect.DeepEqual(apps, c.apps) {
			t.Errorf("%v: got %q %v, %v; want %q %v", c.pos, router, apps, err, c.router, c.apps)
		}
	}
	if _, _, err := logsTarget(s, []string{"ghost"}); err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Errorf("unknown: %v", err)
	}
}

func TestNetLogs(t *testing.T) {
	withGlobal(t, globalProjects)
	s := vnet.NewStore(t.TempDir())
	run := newFakeRunner()

	var gotReq ipc.LogsRequest
	var gotSession string
	orig := streamLogs
	t.Cleanup(func() { streamLogs = orig })
	streamLogs = func(session string, req ipc.LogsRequest, onStart func([]string), onLines func(ipc.LogLines) error) (string, error) {
		gotSession, gotReq = session, req
		onStart([]string{"api", "worker"})
		_ = onLines(ipc.LogLines{Tab: "api", Lines: []string{"\x1b[32mready\x1b[0m"}})
		_ = onLines(ipc.LogLines{Tab: "worker", Lines: []string{"job 1"}, Skipped: 3})
		return "session ended", nil
	}

	var out bytes.Buffer
	if err := netLogs(&out, s, run, []string{"dev2"}, logOpts{Lines: 5}); err == nil || !strings.Contains(err.Error(), "not up") {
		t.Errorf("router down: %v", err)
	}

	run.alive["d2"] = true
	if err := netLogs(&out, s, run, []string{"dev2", "api"}, logOpts{Lines: 5, Follow: true}); err != nil {
		t.Fatal(err)
	}
	want := "api    | ready\nworker | … 3 lines skipped (scrollback full)\nworker | job 1\n[d2: session ended]\n"
	if out.String() != want {
		t.Errorf("output:\n%q\nwant\n%q", out.String(), want)
	}
	if gotSession != "d2" || !reflect.DeepEqual(gotReq, ipc.LogsRequest{Tabs: []string{"api"}, Lines: 5, Follow: true}) {
		t.Errorf("request = %s %+v", gotSession, gotReq)
	}

	streamLogs = func(string, ipc.LogsRequest, func([]string), func(ipc.LogLines) error) (string, error) {
		return "", ipc.ErrLogsUnsupported
	}
	if err := netLogs(&out, s, run, []string{"dev2"}, logOpts{}); err == nil || !strings.Contains(err.Error(), "sax net down d2") {
		t.Errorf("old daemon: %v", err)
	}
}

// A project brought up from a .sax.lan elsewhere is found by name from any
// folder, through the router's recorded source.
func TestKnownProjectFromAnyFolder(t *testing.T) {
	fakeShim(t)
	withGlobal(t, "projects:\n  other: {sessions: {x: {cmd: y}}}\n")
	projDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(projDir, vnet.EnvFileName), []byte(upEnv), 0o644); err != nil {
		t.Fatal(err)
	}
	s := vnet.NewStore(t.TempDir())
	orig := knownRouters
	knownRouters = func() []vnet.Router { rs, _ := s.List(); return rs }
	t.Cleanup(func() { knownRouters = orig })

	run := newFakeRunner()
	if err := netUp(&bytes.Buffer{}, s, run, projectRef{Dir: projDir}, false); err != nil {
		t.Fatal(err)
	}
	r, _ := s.Get("v1")
	if r.Source != filepath.Join(projDir, vnet.EnvFileName) || r.Project != "v1" {
		t.Fatalf("router source = %q project %q", r.Source, r.Project)
	}

	elsewhere := t.TempDir()
	p, err := resolveProject(projectRef{Name: "v1", Dir: elsewhere})
	if err != nil || len(p.Sessions) != 2 {
		t.Fatalf("v1 from elsewhere: %+v, %v", p, err)
	}
	var out bytes.Buffer
	if err := netProjects(&out, run, elsewhere); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "v1") || !strings.Contains(out.String(), "other") {
		t.Errorf("projects from elsewhere should list v1 (known) and other (global):\n%s", out.String())
	}
	if err := netCheck(&out, projectRef{Name: "v1", Dir: elsewhere}); err != nil {
		t.Errorf("check v1 from elsewhere: %v", err)
	}
}
