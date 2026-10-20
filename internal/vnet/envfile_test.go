package vnet

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const sampleEnv = `
router: v1
subnet: 10.12.12.0/24
domain: lan

sessions:
  web: { cwd: ./web, cmd: "ng serve" }
  api: { ip: 10.12.12.4, cwd: ./api, cmd: "npm run start:dev", env: { NODE_ENV: development } }
  db:  { cmd: "postgres -D ./data" }

routes:                              # first match wins
  - bob.lan/api/*  -> api:3200
  - bob.lan/*      -> web:4200
  - db.lan:5432    -> db:5432
`

func TestParseEnvFile(t *testing.T) {
	e, err := ParseEnvFile([]byte(sampleEnv))
	if err != nil {
		t.Fatal(err)
	}
	if e.Router != "v1" || e.Subnet != "10.12.12.0/24" || e.Domain != "lan" {
		t.Errorf("header = %+v", e)
	}
	if got := e.SessionNames(); !reflect.DeepEqual(got, []string{"api", "db", "web"}) {
		t.Errorf("SessionNames = %v", got)
	}
	if e.Sessions["api"].Env["NODE_ENV"] != "development" {
		t.Errorf("env not parsed: %+v", e.Sessions["api"])
	}
	if len(e.Routes) != 3 || e.Routes[0] != "bob.lan/api/*  -> api:3200" {
		t.Errorf("routes = %q", e.Routes)
	}
}

func TestParseEnvFile_Errors(t *testing.T) {
	cases := map[string]string{
		"unknown key":    "router: v1\nsubnet: 10.0.0.0/24\nbogus: 1\n",
		"missing router": "subnet: 10.0.0.0/24\n",
		"bad yaml":       "router: [\n",
		"route not str":  "router: v1\nroutes:\n  - {a: b}\n",
	}
	for label, data := range cases {
		if _, err := ParseEnvFile([]byte(data)); err == nil {
			t.Errorf("%s: expected error", label)
		}
	}
}

func TestEnvFile_Plan(t *testing.T) {
	e, err := ParseEnvFile([]byte(sampleEnv))
	if err != nil {
		t.Fatal(err)
	}
	r, warnings, err := e.Plan()
	if err != nil {
		t.Fatal(err)
	}
	// Explicit IPs are placed first, then the rest in name order.
	want := map[string]string{"api": "10.12.12.4", "db": "10.12.12.2", "web": "10.12.12.3"}
	if !reflect.DeepEqual(r.Sessions, want) {
		t.Errorf("sessions = %v, want %v", r.Sessions, want)
	}
	if len(r.Rules) != 3 || r.Rules[1] != "bob.lan/* -> web:4200" {
		t.Errorf("rules = %v", r.Rules)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
}

func TestEnvFile_PlanErrors(t *testing.T) {
	cases := map[string]string{
		"bad subnet":     "router: v1\nsubnet: 10.0.0.1/24\n",
		"bad route":      "router: v1\nsubnet: 10.0.0.0/24\nroutes: [\"nope\"]\n",
		"unknown target": "router: v1\nsubnet: 10.0.0.0/24\nroutes: [\"a.lan/* -> ghost:80\"]\n",
		"duplicate ip":   "router: v1\nsubnet: 10.0.0.0/24\nsessions:\n  a: {ip: 10.0.0.5, cmd: x}\n  b: {ip: 10.0.0.5, cmd: x}\n",
		"missing cmd":    "router: v1\nsubnet: 10.0.0.0/24\nsessions:\n  a: {cwd: .}\n",
	}
	for label, data := range cases {
		e, err := ParseEnvFile([]byte(data))
		if err != nil {
			continue // rejected even earlier: also fine
		}
		if _, _, err := e.Plan(); err == nil {
			t.Errorf("%s: expected Plan error", label)
		}
	}
}

func TestEnvFile_DefaultSubnet(t *testing.T) {
	e, err := ParseEnvFile([]byte("router: v1\nsessions:\n  a: {cmd: x}\n"))
	if err != nil {
		t.Fatal(err)
	}
	r, _, err := e.Plan()
	if err != nil {
		t.Fatal(err)
	}
	if r.Subnet != DefaultSubnet {
		t.Errorf("subnet = %s, want %s", r.Subnet, DefaultSubnet)
	}
}

func TestLoadEnvFile_ResolvesCwd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, EnvFileName)
	if err := os.WriteFile(path, []byte(sampleEnv), 0o644); err != nil {
		t.Fatal(err)
	}
	e, err := LoadEnvFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := e.Sessions["api"].Cwd; got != filepath.Join(dir, "api") {
		t.Errorf("api cwd = %s", got)
	}
	if got := e.Sessions["db"].Cwd; got != dir {
		t.Errorf("db cwd should default to the file's dir, got %s", got)
	}

	if _, err := LoadEnvFile(filepath.Join(dir, "missing.yaml")); err == nil ||
		!strings.Contains(err.Error(), "missing.yaml") {
		t.Errorf("missing file error should name the file: %v", err)
	}
}

func TestFindEnvFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := FindEnvFile(dir); err == nil || !strings.Contains(err.Error(), EnvFileName) {
		t.Errorf("empty dir: err = %v (should name %s)", err, EnvFileName)
	}

	legacy := filepath.Join(dir, LegacyEnvFileName)
	_ = os.WriteFile(legacy, []byte("router: v1\n"), 0o644)
	if got, err := FindEnvFile(dir); err != nil || got != legacy {
		t.Errorf("legacy only = %q, %v", got, err)
	}

	current := filepath.Join(dir, EnvFileName)
	_ = os.WriteFile(current, []byte("router: v1\n"), 0o644)
	if got, err := FindEnvFile(dir); err != nil || got != current {
		t.Errorf("both present = %q, %v; want %s first", got, err, EnvFileName)
	}
}
