package mcp

import (
	"errors"
	"strings"
	"testing"
)

func TestJoinNet(t *testing.T) {
	var gotName, gotNet, gotIP string
	join := func(name, net, ip string) ([]string, error) {
		gotName, gotNet, gotIP = name, net, ip
		return []string{"SAX_VNET_IP=10.0.0.2"}, nil
	}

	env, err := joinNet(join, "api", map[string]interface{}{})
	if err != nil || env != nil {
		t.Errorf("no net: env=%v err=%v", env, err)
	}
	if _, err := joinNet(join, "api", map[string]interface{}{"ip": "10.0.0.2"}); err == nil {
		t.Error("ip without net should fail")
	}
	env, err = joinNet(join, "api", map[string]interface{}{"net": "v1:api", "ip": "10.0.0.2"})
	if err != nil || len(env) != 1 || gotName != "api" || gotNet != "v1:api" || gotIP != "10.0.0.2" {
		t.Errorf("join: env=%v err=%v call=%q %q %q", env, err, gotName, gotNet, gotIP)
	}
	failing := func(string, string, string) ([]string, error) { return nil, errors.New("router not found") }
	if _, err := joinNet(failing, "api", map[string]interface{}{"net": "ghost"}); err == nil ||
		!strings.Contains(err.Error(), "router not found") {
		t.Errorf("err = %v", err)
	}
	if _, err := joinNet(nil, "api", map[string]interface{}{"net": "v1"}); err == nil {
		t.Error("nil joiner should fail")
	}
}

func TestRegisterNetTools(t *testing.T) {
	s := NewServer("sax", "test")
	registerNet(s)
	names := map[string]bool{}
	for _, def := range s.tools {
		names[def.Name] = true
	}
	for _, want := range []string{"sax_net_list", "sax_net_inspect", "sax_net_projects", "sax_net_up", "sax_net_down", "sax_net_map"} {
		if !names[want] {
			t.Errorf("tool %s not registered", want)
		}
	}
	res := s.handlers["sax_net_map"](map[string]interface{}{"router": "v1"})
	if !res.IsError || !strings.Contains(res.Content[0].Text, "pattern is required") {
		t.Errorf("missing params result = %+v", res)
	}
}

func TestNetToolArgs(t *testing.T) {
	byName := map[string]netTool{}
	for _, nt := range netTools {
		byName[nt.name] = nt
	}
	cases := map[string]struct {
		params map[string]interface{}
		want   string
	}{
		"sax_net_up":       {map[string]interface{}{"file": "v1.yaml"}, "up -d -f v1.yaml"},
		"sax_net_projects": {map[string]interface{}{}, "projects"},
		"sax_net_down":     {map[string]interface{}{"router": "v1"}, "down v1"},
		"sax_net_map": {map[string]interface{}{"router": "v1", "pattern": "bob.lan/*", "target": "web:4200"},
			"map v1 bob.lan/* web:4200"},
	}
	for name, c := range cases {
		if got := strings.Join(byName[name].args(c.params), " "); got != c.want {
			t.Errorf("%s args = %q, want %q", name, got, c.want)
		}
	}
	if got := strings.Join(byName["sax_net_up"].args(map[string]interface{}{}), " "); got != "up -d" {
		t.Errorf("up without file = %q", got)
	}
	if got := strings.Join(byName["sax_net_up"].args(map[string]interface{}{"project": "dev1"}), " "); got != "up -d dev1" {
		t.Errorf("up with project = %q", got)
	}
}
