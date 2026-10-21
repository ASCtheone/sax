package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asc/sax/internal/vnet"
)

// runProject runs `sax net project <args>` and returns its output.
func runProject(t *testing.T, run sessionRunner, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := netProject(&out, run, args)
	return out.String(), err
}

func mustProject(t *testing.T, run sessionRunner, args ...string) string {
	t.Helper()
	out, err := runProject(t, run, args...)
	if err != nil {
		t.Fatalf("sax net project %s: %v", strings.Join(args, " "), err)
	}
	return out
}

// The whole flow from an empty home: add a project from its folder, add
// apps with their command, route and port, then use it from elsewhere.
func TestNetProject_Flow(t *testing.T) {
	fakeShim(t)
	home := withGlobal(t, "")
	global := globalEnvFile()
	_ = os.Remove(global) // starts without ~/.sax.net
	run := newFakeRunner()
	projDir := filepath.Join(home, "code", "dev1")
	t.Chdir(projDir)

	out := mustProject(t, run, "add", "dev1", "--domain", "lan")
	if !strings.Contains(out, "dir ~/code/dev1") {
		t.Errorf("dir should default to this folder, as ~/...: %s", out)
	}
	mustProject(t, run, "app", "add", "dev1", "api", "--cmd", "npm run start:dev", "--cwd", "api",
		"--env", "NODE_ENV=development", "--port", "3200", "--route", "dev1.lan/api/*", "--publish", "13200")
	out = mustProject(t, run, "app", "add", "dev1", "web", "--port", "4200", "--route", "dev1.lan/*", "--", "ng", "serve", "--port", "4200")
	if !strings.Contains(out, "route dev1.lan/* -> web:4200") {
		t.Errorf("app add output: %s", out)
	}
	mustProject(t, run, "route", "add", "dev1", "dev1.lan/auth/*", "api:3200", "--at", "1")
	mustProject(t, run, "port", "add", "dev1", "14200", "web:4200")

	t.Chdir(t.TempDir()) // everything else from another folder
	p, err := resolveProject(projectRef{Name: "dev1"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Sessions["api"].Cmd != "npm run start:dev" || p.Sessions["api"].Cwd != filepath.Join(projDir, "api") ||
		p.Sessions["api"].Env["NODE_ENV"] != "development" || p.Sessions["web"].Cmd != "ng serve --port 4200" {
		t.Errorf("apps = %+v", p.Sessions)
	}
	if got := strings.Join(p.Routes, " | "); got != "dev1.lan/auth/* -> api:3200 | dev1.lan/api/* -> api:3200 | dev1.lan/* -> web:4200" {
		t.Errorf("routes = %s", got)
	}
	if got := strings.Join(p.Publish, " | "); got != "13200 -> api:3200 | 14200 -> web:4200" {
		t.Errorf("ports = %s", got)
	}
	if _, _, err := p.Plan(); err != nil {
		t.Errorf("plan: %v", err)
	}

	show := mustProject(t, run, "show", "dev1")
	for _, want := range []string{"project dev1  (~/.sax.net, down)", "api  cmd: npm run start:dev", "dev1.lan/auth/*"} {
		if !strings.Contains(show, want) {
			t.Errorf("show missing %q:\n%s", want, show)
		}
	}

	// Edits a running project hint how to apply them.
	s := vnet.NewStore(t.TempDir())
	if err := netUp(&bytes.Buffer{}, s, run, projectRef{Name: "dev1"}, false); err != nil {
		t.Fatal(err)
	}
	out = mustProject(t, run, "app", "set", "dev1", "web", "--cmd", "ng serve --port 4300", "--unset-env", "NOPE")
	if !strings.Contains(out, "apply with: sax net restart dev1 web") {
		t.Errorf("set on a running project should hint restart: %s", out)
	}
	out = mustProject(t, run, "app", "rm", "dev1", "web")
	if !strings.Contains(out, "dropped dev1.lan/* -> web:4200") || !strings.Contains(out, "dropped 14200 -> web:4200") {
		t.Errorf("rm should list what it dropped: %s", out)
	}
	mustProject(t, run, "route", "rm", "dev1", "dev1.lan/auth/*")
	mustProject(t, run, "port", "rm", "dev1", "13200")
	p, _ = resolveProject(projectRef{Name: "dev1"})
	if len(p.Sessions) != 1 || len(p.Routes) != 1 || len(p.Publish) != 0 {
		t.Errorf("after removals: %+v", p.EnvFile)
	}

	out = mustProject(t, run, "rm", "dev1")
	if !strings.Contains(out, "its router is still up: sax net down dev1") {
		t.Errorf("rm of a running project: %s", out)
	}
}

// Invalid edits are refused and leave the file untouched.
func TestNetProject_RefusesInvalidEdits(t *testing.T) {
	withGlobal(t, globalProjects)
	run := newFakeRunner()
	before, _ := os.ReadFile(globalEnvFile())

	for _, args := range [][]string{
		{"add", "dev1"}, // exists
		{"app", "add", "dev1", "api", "--cmd", "x"},                      // exists
		{"app", "add", "dev1", "db"},                                     // no command
		{"app", "add", "dev1", "db", "--cmd", "x", "--route", "a.lan/*"}, // route without port
		{"route", "add", "dev1", "dev1.lan/*", "ghost:80"},               // unknown app
		{"route", "add", "dev1", "dev1.lan/*", "api:80", "--at", "0"},
		{"port", "add", "dev1", "99999", "api:80"},
		{"app", "set", "nope", "api", "--cmd", "x"},
		{"app", "add", "dev1", "db", "--cmd", "x", "--env", "NOEQUALS"},
		{"app", "add", "dev1", "db", "--cmd", "x", "--", "y"},
		{"add", "dev3", "--subnet", "300.0.0.0/24"},
		{"bogus"},
		{"app", "add", "dev1", "db", "--frobnicate"},
	} {
		if _, err := runProject(t, run, args...); err == nil {
			t.Errorf("%v: expected an error", args)
		}
	}
	after, _ := os.ReadFile(globalEnvFile())
	if !bytes.Equal(before, after) {
		t.Errorf("a refused edit changed the file:\n%s", after)
	}
	if entries, _ := filepath.Glob(globalEnvFile() + ".tmp-*"); len(entries) != 0 {
		t.Errorf("temp files left: %v", entries)
	}
}

// A project in a folder's .sax.lan is edited in that file, from anywhere.
func TestNetProject_EditsTheProjectsOwnFile(t *testing.T) {
	fakeShim(t)
	withGlobal(t, globalProjects)
	projDir := t.TempDir()
	local := filepath.Join(projDir, vnet.EnvFileName)
	if err := os.WriteFile(local, []byte("# v1 here\nrouter: v1\nsessions:\n  api: {cmd: x}\n"), 0o600); err != nil {
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

	t.Chdir(t.TempDir())
	mustProject(t, run, "app", "add", "v1", "web", "--cmd", "ng serve")
	data, _ := os.ReadFile(local)
	if !strings.Contains(string(data), "# v1 here") || !strings.Contains(string(data), "web:") {
		t.Errorf("local file after edit:\n%s", data)
	}
	if st, _ := os.Stat(local); st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want the file's own 0600", st.Mode().Perm())
	}
	if g, _ := os.ReadFile(globalEnvFile()); strings.Contains(string(g), "ng serve") {
		t.Error("the edit went to ~/.sax.net instead of the project's file")
	}
}

func TestProjectDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(home)
	for in, want := range map[string]string{
		"~/x":                         "~/x",
		filepath.Join(home, "code/a"): "~/code/a",
		"code/b":                      "~/code/b", // against this folder
		home:                          "~",
		"/srv/app":                    "/srv/app",
	} {
		if got := projectDir(in); got != want {
			t.Errorf("projectDir(%q) = %q, want %q", in, got, want)
		}
	}
}

// Apps start with exactly one of --cmd, --nx or --npm; up types the matching
// command into the app's tab.
func TestNetProject_NxAndNpmApps(t *testing.T) {
	fakeShim(t)
	withGlobal(t, globalProjects)
	run := newFakeRunner()
	mustProject(t, run, "app", "add", "dev1", "shop", "--nx", "shop")
	mustProject(t, run, "app", "add", "dev1", "admin", "--nx", "admin:serve:development", "--", "--port", "4300")
	mustProject(t, run, "app", "add", "dev1", "docs", "--npm", "docs:dev")
	if _, err := runProject(t, run, "app", "add", "dev1", "x", "--nx", "a", "--npm", "b"); err == nil {
		t.Error("--nx with --npm should fail")
	}
	// Switch api from its cmd to an npm script.
	mustProject(t, run, "app", "set", "dev1", "api", "--npm", "start:dev")

	s := vnet.NewStore(t.TempDir())
	if err := netUp(&bytes.Buffer{}, s, run, projectRef{Name: "dev1"}, false); err != nil {
		t.Fatal(err)
	}
	for app, want := range map[string]string{
		"shop": "nx run shop:serve",
		"docs": "npm run docs:dev",
		"api":  "npm run start:dev",
		"web":  "nx serve", // a plain cmd stays as written
	} {
		if in := tabOf(t, run, "dev1", app).Input; !strings.HasSuffix(in, want) {
			t.Errorf("%s types %q, want ...%q", app, in, want)
		}
	}
	p, _ := resolveProject(projectRef{Name: "dev1"})
	if p.Sessions["api"].Cmd != "" || p.Sessions["admin"].NX != "admin:serve:development --port 4300" {
		t.Errorf("file: api=%+v admin=%+v", p.Sessions["api"], p.Sessions["admin"])
	}
	if in := tabOf(t, run, "dev1", "admin").Input; !strings.HasSuffix(in, "nx run admin:serve:development --port 4300") {
		t.Errorf("admin types %q", in)
	}
}

func TestAppFields_ExtraArgs(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--npm", "start", "--", "--port", "4300"}, "npm=start -- --port 4300"},
		{[]string{"--nx", "api", "--", "--port", "4300"}, "nx=api --port 4300"},
		{[]string{"--", "go", "run", "."}, "cmd=go run ."},
	} {
		cl, _ := parseCLI(c.args, projectFlagsWithValue)
		f, err := appFields(cl)
		got := ""
		switch {
		case f.NPM != nil:
			got = "npm=" + *f.NPM
		case f.NX != nil:
			got = "nx=" + *f.NX
		case f.Cmd != nil:
			got = "cmd=" + *f.Cmd
		}
		if err != nil || got != c.want {
			t.Errorf("%v: %q, %v; want %q", c.args, got, err, c.want)
		}
	}
	cl, _ := parseCLI([]string{"--cmd", "x", "--", "y"}, projectFlagsWithValue)
	if _, err := appFields(cl); err == nil {
		t.Error("--cmd with -- should fail")
	}
}
