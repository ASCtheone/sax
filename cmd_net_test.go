package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/asc/sax/internal/vnet"
)

func TestNetPositional(t *testing.T) {
	got := netPositional([]string{"v1", "--subnet", "10.0.0.0/24", "bob.lan/*", "->", "web:80", "--domain", "lan"})
	want := []string{"v1", "bob.lan/*", "->", "web:80"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("netPositional = %v, want %v", got, want)
	}
}

func TestRuleFromArgs(t *testing.T) {
	cases := map[string][]string{
		"two args":   {"bob.lan/*", "web:4200"},
		"with arrow": {"bob.lan/*", "->", "web:4200"},
		"one quoted": {"bob.lan/* -> web:4200"},
	}
	for label, args := range cases {
		got, err := ruleFromArgs(args)
		if err != nil || got != "bob.lan/* -> web:4200" {
			t.Errorf("%s: got %q, %v", label, got, err)
		}
	}
	if _, err := ruleFromArgs([]string{"a", "b", "c", "d"}); err == nil {
		t.Error("expected usage error")
	}
}

func TestNetCommands_Flow(t *testing.T) {
	s := vnet.NewStore(t.TempDir())
	var out bytes.Buffer

	if err := netList(&out, s); err != nil || !strings.Contains(out.String(), "no routers") {
		t.Fatalf("empty list: %q, %v", out.String(), err)
	}
	out.Reset()

	if err := netCreate(&out, s, []string{"v1"}, "10.12.12.0/24", "lan"); err != nil {
		t.Fatal(err)
	}
	if err := netCreate(&out, s, nil, "", ""); err == nil {
		t.Error("create without name should fail")
	}

	for _, rule := range [][]string{
		{"v1", "bob.lan/api/*", "api:3200"},
		{"v1", "bob.lan/*", "->", "web:4200"},
		{"v1", "db.lan:5432 -> db:5432"},
	} {
		if err := netMap(&out, s, rule); err != nil {
			t.Fatalf("map %v: %v", rule, err)
		}
	}
	if err := netMap(&out, s, []string{"v1", "bob.lan", "api:3200"}); err == nil {
		t.Error("conflicting :80 TCP rule should be rejected")
	}

	out.Reset()
	if err := netInspect(&out, s, []string{"v1"}); err != nil {
		t.Fatal(err)
	}
	inspect := out.String()
	for _, want := range []string{
		"router v1  subnet 10.12.12.0/24  domain lan",
		"1  bob.lan/api/* -> api:3200  http  10.12.12.254",
		"2  bob.lan/* -> web:4200      http  10.12.12.254",
		"3  db.lan:5432 -> db:5432     tcp   10.12.12.253",
		"not running in this router yet",
	} {
		if !strings.Contains(inspect, want) {
			t.Errorf("inspect missing %q:\n%s", want, inspect)
		}
	}

	if err := netUnmap(&out, s, []string{"v1", "db.lan:5432"}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := netList(&out, s); err != nil || !strings.Contains(out.String(), "v1      10.12.12.0/24  lan     0         2") {
		t.Errorf("list after unmap:\n%s", out.String())
	}

	if err := netRemove(&out, s, newFakeRunner(), []string{"v1"}); err != nil {
		t.Fatal(err)
	}
	if err := netRemove(&out, s, newFakeRunner(), []string{"v1"}); err == nil {
		t.Error("removing twice should fail")
	}
}

func TestNetCreate_AutoSubnet(t *testing.T) {
	s := vnet.NewStore(t.TempDir())
	var out bytes.Buffer
	if err := netCreate(&out, s, []string{"a"}, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := netCreate(&out, s, []string{"b"}, "", ""); err != nil {
		t.Fatal(err)
	}
	a, _ := s.Get("a")
	b, _ := s.Get("b")
	if a.Subnet == b.Subnet {
		t.Errorf("auto subnets should differ: %s %s", a.Subnet, b.Subnet)
	}
}

func TestNetCheck(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "v1.yaml")
	env := "router: v1\nsubnet: 10.12.12.0/24\nsessions:\n  api: {cmd: x}\n  web: {cmd: y}\n" +
		"routes:\n  - bob.lan/* -> web:4200\n  - bob.lan/api/* -> api:3200\n"
	if err := os.WriteFile(path, []byte(env), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := netCheck(&out, projectRef{File: path}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "never matches") || !strings.Contains(out.String(), "ok") {
		t.Errorf("check output:\n%s", out.String())
	}
	if err := netCheck(&out, projectRef{File: filepath.Join(dir, "nope.yaml")}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("missing file err = %v", err)
	}
}

func TestNetMap_OnlyReportsNewRuleWarnings(t *testing.T) {
	s := vnet.NewStore(t.TempDir())
	var out bytes.Buffer
	_ = netCreate(&out, s, []string{"v1"}, "10.12.12.0/24", "")
	_ = netMap(&out, s, []string{"v1", "a.lan/*", "api:1"})
	out.Reset()
	if err := netMap(&out, s, []string{"v1", "b.lan/*", "web:2"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "a.lan") {
		t.Errorf("map repeated unrelated warnings:\n%s", out.String())
	}
}
