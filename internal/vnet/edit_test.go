package vnet

import (
	"strings"
	"testing"
)

func ptr(s string) *string { return &s }

const editSample = `# my projects
projects:
  dev1: # the main app
    dir: ~/code/dev1
    sessions:
      api: { cwd: api, cmd: "npm start" }
    routes:
      - dev1.lan/api/* -> api:3200 # keep this comment
`

func mustDoc(t *testing.T, data string) *ConfigDoc {
	t.Helper()
	d, err := ParseConfigDoc([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func mustBytes(t *testing.T, d *ConfigDoc) string {
	t.Helper()
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseConfig(out); err != nil {
		t.Fatalf("edited file does not parse: %v\n%s", err, out)
	}
	return string(out)
}

func TestConfigDoc_ProjectsAndComments(t *testing.T) {
	d := mustDoc(t, editSample)
	if err := d.AddProject("dev2", ProjectFields{Dir: ptr("~/code/dev2"), Router: ptr("two")}); err != nil {
		t.Fatal(err)
	}
	if err := d.AddProject("dev1", ProjectFields{}); err == nil {
		t.Error("adding an existing project should fail")
	}
	if err := d.AddProject("Bad_Name", ProjectFields{}); err == nil {
		t.Error("invalid name should fail")
	}
	if err := d.SetProject("dev1", ProjectFields{Domain: ptr("lan"), Dir: ptr("~/src/dev1")}); err != nil {
		t.Fatal(err)
	}
	out := mustBytes(t, d)
	for _, want := range []string{"# my projects", "# the main app", "# keep this comment", "dir: ~/src/dev1", "domain: lan", "router: two"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Join(d.ProjectNames(), ",") != "dev1,dev2" {
		t.Errorf("names = %v", d.ProjectNames())
	}

	if err := d.SetProject("dev1", ProjectFields{Domain: ptr("")}); err != nil || strings.Contains(mustBytes(t, d), "domain:") {
		t.Errorf("empty value should remove the key: %v", err)
	}
	if err := d.RemoveProject("dev2"); err != nil || strings.Contains(mustBytes(t, d), "dev2") {
		t.Errorf("remove: %v", err)
	}
	if err := d.RemoveProject("ghost"); err == nil {
		t.Error("removing a missing project should fail")
	}
}

func TestConfigDoc_EmptyFile(t *testing.T) {
	d := mustDoc(t, "")
	if err := d.AddProject("dev1", ProjectFields{Dir: ptr("/x")}); err != nil {
		t.Fatal(err)
	}
	if err := d.SetApp("dev1", "api", AppFields{Cmd: ptr("go run .")}, true); err != nil {
		t.Fatal(err)
	}
	c, err := parseConfig([]byte(mustBytes(t, d)))
	if err != nil || c.Projects["dev1"].Sessions["api"].Cmd != "go run ." {
		t.Errorf("config = %+v, %v", c, err)
	}
}

func TestConfigDoc_Apps(t *testing.T) {
	d := mustDoc(t, editSample)
	err := d.SetApp("dev1", "web", AppFields{Cmd: ptr("ng serve --port 4200"), Cwd: ptr("web"), Env: map[string]string{"NODE_ENV": "dev"}}, true)
	if err != nil {
		t.Fatal(err)
	}
	out := mustBytes(t, d)
	if !strings.Contains(out, "web: {cmd: ng serve --port 4200, cwd: web, env: {NODE_ENV: dev}}") {
		t.Errorf("a new app should follow its siblings' { } style:\n%s", out)
	}
	if err := d.SetApp("dev1", "web", AppFields{Cmd: ptr("x")}, true); err == nil {
		t.Error("adding an existing app should fail")
	}
	if err := d.SetApp("dev1", "ghost", AppFields{Cwd: ptr("x")}, false); err == nil {
		t.Error("setting a missing app should fail")
	}
	if err := d.SetApp("dev1", "db", AppFields{}, true); err == nil {
		t.Error("a new app needs a command")
	}

	if err := d.SetApp("dev1", "web", AppFields{Env: map[string]string{"PORT": "4200"}, UnsetEnv: []string{"NODE_ENV"}, Cwd: ptr("")}, false); err != nil {
		t.Fatal(err)
	}
	c, _ := parseConfig([]byte(mustBytes(t, d)))
	web := c.Projects["dev1"].Sessions["web"]
	if web.Env["PORT"] != "4200" || web.Env["NODE_ENV"] != "" || web.Cwd != "" || web.Cmd != "ng serve --port 4200" {
		t.Errorf("web after set = %+v", web)
	}
}

func TestConfigDoc_RoutesAndPorts(t *testing.T) {
	d := mustDoc(t, editSample)
	_ = d.SetApp("dev1", "web", AppFields{Cmd: ptr("ng serve")}, true)
	if _, err := d.AddRoute("dev1", "dev1.lan/*", "web:4200", 0); err != nil {
		t.Fatal(err)
	}
	if got, err := d.AddRoute("dev1", "dev1.lan/auth/*", "api:3200", 1); err != nil || got != "dev1.lan/auth/* -> api:3200" {
		t.Fatalf("insert at 1: %q, %v", got, err)
	}
	if _, err := d.AddRoute("dev1", "dev1.lan/api/*", "web:1", 0); err == nil {
		t.Error("a pattern already routed should fail")
	}
	if _, err := d.AddRoute("dev1", "dev1.lan/x", "nope", 0); err == nil {
		t.Error("a bad target should fail")
	}
	c, _ := parseConfig([]byte(mustBytes(t, d)))
	if got := strings.Join(c.Projects["dev1"].Routes, " | "); got != "dev1.lan/auth/* -> api:3200 | dev1.lan/api/* -> api:3200 | dev1.lan/* -> web:4200" {
		t.Errorf("route order = %s", got)
	}

	if _, err := d.AddPublish("dev1", "13200", "api:3200"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddPublish("dev1", "13200", "web:4200"); err == nil {
		t.Error("a host port published twice should fail")
	}
	if err := d.RemoveRoute("dev1", "dev1.lan/auth/*"); err != nil {
		t.Fatal(err)
	}
	if err := d.RemoveRoute("dev1", "dev1.lan/auth/*"); err == nil {
		t.Error("removing a missing route should fail")
	}

	dropped, err := d.RemoveApp("dev1", "api")
	if err != nil || len(dropped) != 2 {
		t.Errorf("removing api should drop its route and port: %v, %v", dropped, err)
	}
	c, _ = parseConfig([]byte(mustBytes(t, d)))
	dev1 := c.Projects["dev1"]
	if len(dev1.Routes) != 1 || len(dev1.Publish) != 0 || len(dev1.Sessions) != 1 {
		t.Errorf("after rm api: %+v", dev1)
	}
	if _, _, err := dev1.Plan(); err != nil {
		t.Errorf("the edited project must still plan: %v", err)
	}
	if err := d.RemovePublish("dev1", "13200"); err == nil {
		t.Error("publish already dropped with its app")
	}
}

func TestConfigDoc_SingleProjectFile(t *testing.T) {
	d := mustDoc(t, "router: v1\nsessions:\n  api: {cmd: x}\n")
	if err := d.SetApp("v1", "web", AppFields{Cmd: ptr("y")}, true); err != nil {
		t.Fatalf("single-project file, its own router: %v", err)
	}
	if err := d.AddProject("v2", ProjectFields{}); err == nil {
		t.Error("cannot add projects to a single-project file")
	}
	if err := d.SetApp("v2", "web", AppFields{Cmd: ptr("y")}, true); err == nil {
		t.Error("unknown project in a single-project file")
	}
}

func TestConfigDoc_RunKinds(t *testing.T) {
	d := mustDoc(t, editSample)
	if err := d.SetApp("dev1", "web", AppFields{NX: ptr("web:serve:development")}, true); err != nil {
		t.Fatal(err)
	}
	if err := d.SetApp("dev1", "both", AppFields{NX: ptr("a"), NPM: ptr("b")}, true); err == nil {
		t.Error("two run kinds at once should fail")
	}
	if err := d.SetApp("dev1", "none", AppFields{Cwd: ptr("x")}, true); err == nil || !strings.Contains(err.Error(), "--nx") {
		t.Errorf("a new app needs one run kind: %v", err)
	}
	// Switching api from cmd to npm drops cmd.
	if err := d.SetApp("dev1", "api", AppFields{NPM: ptr("start:dev")}, false); err != nil {
		t.Fatal(err)
	}
	if err := d.SetApp("dev1", "api", AppFields{NPM: ptr("")}, false); err != nil {
		t.Fatal(err) // "" with nothing else given is not a switch; npm stays
	}
	c, err := parseConfig([]byte(mustBytes(t, d)))
	if err != nil {
		t.Fatal(err)
	}
	api, web := c.Projects["dev1"].Sessions["api"], c.Projects["dev1"].Sessions["web"]
	if api.Cmd != "" || api.NPM != "start:dev" || web.NX != "web:serve:development" {
		t.Errorf("api = %+v, web = %+v", api, web)
	}
	if _, _, err := c.Projects["dev1"].Plan(); err != nil {
		t.Errorf("plan: %v", err)
	}
}
