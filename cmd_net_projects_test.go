package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asc/sax/internal/vnet"
)

// No test may read the developer's real ~/.sax.net.
func TestMain(m *testing.M) {
	globalEnvFile = func() string { return filepath.Join(os.TempDir(), "sax-test-no-global.net") }
	knownRouters = func() []vnet.Router { return nil }
	os.Exit(m.Run())
}

const globalProjects = `projects:
  dev1:
    dir: code/dev1
    sessions:
      api: { cmd: "npm start", cwd: api }
      web: { cmd: "nx serve" }
  dev2:
    router: d2
    dir: code/dev2
    subnet: 10.88.0.0/24
    sessions:
      api: { cmd: "go run ." }
`

// withGlobal installs a ~/.sax.net in a fresh home folder and returns home.
func withGlobal(t *testing.T, content string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, vnet.GlobalEnvFileName)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	orig := globalEnvFile
	globalEnvFile = func() string { return path }
	t.Cleanup(func() { globalEnvFile = orig })
	for _, d := range []string{"code/dev1/api/src", "code/dev2", "elsewhere"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func TestResolveProject(t *testing.T) {
	home := withGlobal(t, globalProjects)
	local := filepath.Join(home, "elsewhere")
	in := func(rel string) string { return filepath.Join(home, rel) }

	cases := []struct {
		name    string
		ref     projectRef
		project string
		router  string
		errHas  string
	}{
		{"by name from anywhere", projectRef{Name: "dev2", Dir: local}, "dev2", "d2", ""},
		{"by folder", projectRef{Dir: in("code/dev1/api/src")}, "dev1", "dev1", ""},
		{"by folder 2", projectRef{Dir: in("code/dev2")}, "dev2", "d2", ""},
		{"nothing covers the folder", projectRef{Dir: local}, "", "", "sax net up <project> (dev1, dev2)"},
		{"unknown name", projectRef{Name: "ghost", Dir: local}, "", "", "dev1, dev2"},
		{"explicit projects file needs a pick", projectRef{File: globalEnvFile(), Dir: local}, "", "", "pick one"},
		{"explicit projects file by name", projectRef{File: globalEnvFile(), Name: "dev1", Dir: local}, "dev1", "dev1", ""},
	}
	for _, c := range cases {
		p, err := resolveProject(c.ref)
		if c.errHas != "" {
			if err == nil || !strings.Contains(err.Error(), c.errHas) {
				t.Errorf("%s: err = %v, want %q", c.name, err, c.errHas)
			}
			continue
		}
		if err != nil || p.Name != c.project || p.Router != c.router {
			t.Errorf("%s: got %q/%q, %v", c.name, p.Name, p.Router, err)
		}
	}

	p, _ := resolveProject(projectRef{Name: "dev1", Dir: local})
	if p.Sessions["api"].Cwd != in("code/dev1/api") || p.Sessions["web"].Cwd != in("code/dev1") {
		t.Errorf("dev1 cwds = %+v", p.Sessions)
	}

	// A local .sax.lan wins for a plain `sax net up`, and for its own name;
	// other names still reach ~/.sax.net.
	if err := os.WriteFile(filepath.Join(local, vnet.EnvFileName), []byte("router: dev1\nsessions:\n  only: {cmd: x}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []projectRef{{Dir: local}, {Name: "dev1", Dir: local}} {
		if p, err := resolveProject(ref); err != nil || len(p.Sessions) != 1 || p.Source != filepath.Join(local, vnet.EnvFileName) {
			t.Errorf("%+v: local file should win, got %+v, %v", ref, p, err)
		}
	}
	if p, err := resolveProject(projectRef{Name: "dev2", Dir: local}); err != nil || p.Router != "d2" {
		t.Errorf("dev2 from the global file next to a local one: %+v, %v", p, err)
	}
}

func TestResolveProject_NoFiles(t *testing.T) {
	_, err := resolveProject(projectRef{Dir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "no .sax.lan") || !strings.Contains(err.Error(), "sax-test-no-global.net") {
		t.Errorf("err = %v", err)
	}
}

func TestNetUpDownByProject(t *testing.T) {
	fakeShim(t)
	home := withGlobal(t, globalProjects)
	s := vnet.NewStore(t.TempDir())
	run := newFakeRunner()
	var out bytes.Buffer

	if err := netUp(&out, s, run, projectRef{Name: "dev2", Dir: home}, false); err != nil {
		t.Fatal(err)
	}
	if err := netUp(&out, s, run, projectRef{Dir: filepath.Join(home, "code/dev1")}, false); err != nil {
		t.Fatal(err)
	}
	if len(run.tabs["d2"]) != 1 || len(run.tabs["dev1"]) != 2 {
		t.Fatalf("tabs = %v", run.tabs)
	}
	if dir := tabOf(t, run, "dev1", "api").Dir; dir != filepath.Join(home, "code/dev1/api") {
		t.Errorf("api dir = %s", dir)
	}

	// down takes a router or a project name.
	for _, name := range []string{"dev2", "dev1"} {
		router, err := routerFromArgs(s, projectRef{Name: name, Dir: home})
		if err != nil {
			t.Fatal(err)
		}
		if err := netDown(&out, s, run, router); err != nil {
			t.Fatal(err)
		}
	}
	if run.alive["d2"] || run.alive["dev1"] {
		t.Errorf("still alive: %v", run.alive)
	}
}

func TestNetProjectsAndCheck(t *testing.T) {
	home := withGlobal(t, globalProjects)
	run := newFakeRunner()
	run.alive["d2"] = true
	var out bytes.Buffer
	if err := netProjects(&out, run, filepath.Join(home, "code/dev1")); err != nil {
		t.Fatal(err)
	}
	var rows []string
	for _, line := range strings.Split(out.String(), "\n") {
		rows = append(rows, strings.Join(strings.Fields(line), " "))
	}
	got := strings.Join(rows, "\n")
	for _, want := range []string{"dev1 dev1 2 - ~/.sax.net <- sax net up", "dev2 d2 1 up ~/.sax.net\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("projects output missing %q:\n%s", want, got)
		}
	}

	out.Reset()
	if err := netCheck(&out, projectRef{Dir: filepath.Join(home, "elsewhere")}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "~/.sax.net (dev1): ok") || !strings.Contains(out.String(), "~/.sax.net (dev2): ok") {
		t.Errorf("check should validate every project:\n%s", out.String())
	}
}
