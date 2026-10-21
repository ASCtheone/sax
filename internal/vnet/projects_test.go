package vnet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const projectsEnv = `projects:
  dev1:
    dir: ~/code/dev1
    sessions:
      api: { cwd: api, cmd: "npm start" }
      web: { cmd: "nx serve" }
    routes:
      - dev1.lan/api/* -> api:3200
      - dev1.lan/*     -> web:4200
    banner: "{branch}"
  dev2:
    router: other
    subnet: 10.88.0.0/24
    dir: work/dev2
    sessions:
      api: { cmd: "go run .", cwd: /srv/dev2-api }
  tools:
    sessions:
      db: { cmd: "postgres" }
`

func writeConfig(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadConfig_Projects(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := t.TempDir()
	c, err := LoadConfig(writeConfig(t, dir, GlobalEnvFileName, projectsEnv))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Multi || strings.Join(c.Names(), ",") != "dev1,dev2,tools" {
		t.Fatalf("multi=%v names=%v", c.Multi, c.Names())
	}

	dev1, ok := c.Project("dev1")
	if !ok {
		t.Fatal("dev1 missing")
	}
	root := filepath.Join(home, "code", "dev1")
	if dev1.Router != "dev1" {
		t.Errorf("router should default to the project name, got %q", dev1.Router)
	}
	if dev1.Sessions["api"].Cwd != filepath.Join(root, "api") || dev1.Sessions["web"].Cwd != root {
		t.Errorf("cwd should resolve against dir (with ~): api=%s web=%s", dev1.Sessions["api"].Cwd, dev1.Sessions["web"].Cwd)
	}
	if _, _, err := dev1.Plan(); err != nil {
		t.Errorf("dev1 should plan like a .sax.lan: %v", err)
	}

	dev2, _ := c.Project("dev2")
	if dev2.Router != "other" || dev2.Subnet != "10.88.0.0/24" {
		t.Errorf("dev2 = %+v", dev2)
	}
	if dev2.Dir != filepath.Join(dir, "work", "dev2") || dev2.Sessions["api"].Cwd != "/srv/dev2-api" {
		t.Errorf("relative dir resolves against the file: dir=%s api=%s", dev2.Dir, dev2.Sessions["api"].Cwd)
	}
	tools, _ := c.Project("tools")
	if tools.Sessions["db"].Cwd != dir {
		t.Errorf("no dir: cwd defaults to the file's folder, got %s", tools.Sessions["db"].Cwd)
	}
	if _, ok := c.Project("ghost"); ok {
		t.Error("unknown project found")
	}
}

func TestLoadConfig_SingleProject(t *testing.T) {
	dir := t.TempDir()
	c, err := LoadConfig(writeConfig(t, dir, EnvFileName, "router: v1\nsessions:\n  api: {cmd: x}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Multi || strings.Join(c.Names(), ",") != "v1" {
		t.Errorf("single file: multi=%v names=%v", c.Multi, c.Names())
	}
	if e, ok := c.Project("v1"); !ok || e.Sessions["api"].Cwd != dir {
		t.Errorf("v1 = %+v", e)
	}
}

func TestLoadConfig_Errors(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"mixed":       "router: v1\nprojects:\n  a: {sessions: {x: {cmd: y}}}\n",
		"unknown key": "projects:\n  a: {sesions: {x: {cmd: y}}}\n",
		"bad name":    "projects:\n  Bad_Name: {sessions: {x: {cmd: y}}}\n",
		"no router":   "sessions:\n  x: {cmd: y}\n",
	} {
		if _, err := LoadConfig(writeConfig(t, dir, "f.yaml", content)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestConfig_ForDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	c, err := LoadConfig(writeConfig(t, home, GlobalEnvFileName, projectsEnv))
	if err != nil {
		t.Fatal(err)
	}
	for dir, want := range map[string]string{
		filepath.Join(home, "code", "dev1"):               "dev1",
		filepath.Join(home, "code", "dev1", "api", "src"): "dev1",
		"/srv/dev2-api/cmd":                               "dev2", // a session's own cwd counts
		filepath.Join(home, "work", "dev2"):               "dev2",
		filepath.Join(home, "code"):                       "",
		home:                                              "", // tools has no folder of its own
	} {
		got, err := c.ForDir(dir)
		if err != nil || got != want {
			t.Errorf("ForDir(%s) = %q, %v; want %q", dir, got, err, want)
		}
	}

	overlap := "projects:\n  a: {dir: /x, sessions: {s: {cmd: y}}}\n  b: {dir: /x, sessions: {s: {cmd: y}}}\n  c: {dir: /x/deeper, sessions: {s: {cmd: y}}}\n"
	c, _ = LoadConfig(writeConfig(t, home, "o.yaml", overlap))
	if _, err := c.ForDir("/x/y"); err == nil || !strings.Contains(err.Error(), "a, b") {
		t.Errorf("two projects on the same folder should be ambiguous: %v", err)
	}
	if got, err := c.ForDir("/x/deeper/z"); err != nil || got != "c" {
		t.Errorf("the deepest folder wins: %q, %v", got, err)
	}
}

func TestLoadEnvFile_RefusesProjectsFile(t *testing.T) {
	path := writeConfig(t, t.TempDir(), "f.yaml", projectsEnv)
	if _, err := LoadEnvFile(path); err == nil || !strings.Contains(err.Error(), "dev1, dev2, tools") {
		t.Errorf("err = %v; should list the projects to pick from", err)
	}
}

// A projects file whose last project was removed is valid (and empty).
func TestLoadConfig_EmptyProjects(t *testing.T) {
	c, err := LoadConfig(writeConfig(t, t.TempDir(), "f.yaml", "projects: {}\n"))
	if err != nil || !c.Multi || len(c.Names()) != 0 {
		t.Errorf("empty projects: %+v, %v", c, err)
	}
	if _, err := c.ForDir("/x"); err != nil {
		t.Error(err)
	}
}
