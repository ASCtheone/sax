package vnet

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLaunderCommand(t *testing.T) {
	withGOOS(t, "darwin")
	dir := t.TempDir()
	tool := filepath.Join(dir, "nest")
	_ = os.WriteFile(tool, []byte("#!/usr/bin/env node\nconsole.log(1)\n"), 0o755)
	withArg := filepath.Join(dir, "tsx")
	_ = os.WriteFile(withArg, []byte("#!/usr/bin/env node --no-warnings\n"), 0o755)
	plain := filepath.Join(dir, "plain")
	_ = os.WriteFile(plain, []byte("#!/bin/sh\necho hi\n"), 0o755)

	look := func(name string) (string, error) {
		if filepath.IsAbs(name) {
			return name, nil // like exec.LookPath
		}
		switch name {
		case "sh":
			return "/bin/sh", nil
		case "node":
			return "/opt/homebrew/bin/node", nil
		case "nest", "tsx", "plain":
			return filepath.Join(dir, name), nil
		}
		return "", errors.New("not found")
	}
	getenv := func(k string) string {
		if k == EnvShim {
			return "/s/lib.dylib"
		}
		return ""
	}

	cases := []struct {
		in, want []string
	}{
		{[]string{"sh", "-c", "go-api serve"},
			[]string{"sh", "-c", "export DYLD_INSERT_LIBRARIES='/s/lib.dylib'\ngo-api serve"}},
		{[]string{"/bin/sh", "-ec", "x"},
			[]string{"/bin/sh", "-ec", "export DYLD_INSERT_LIBRARIES='/s/lib.dylib'\nx"}},
		{[]string{"nest", "start"}, []string{"/opt/homebrew/bin/node", tool, "start"}},
		{[]string{"tsx", "a.ts"}, []string{"/opt/homebrew/bin/node", "--no-warnings", withArg, "a.ts"}},
		{[]string{"plain"}, []string{"plain"}},               // shebang into SIP shell: nothing to do
		{[]string{"node", "x.js"}, []string{"node", "x.js"}}, // a real binary keeps the shim
		{[]string{"sh", "script.sh"}, []string{"sh", "script.sh"}},
		{[]string{"missing"}, []string{"missing"}},
	}
	for _, c := range cases {
		if got := LaunderCommand(c.in, getenv, look); !reflect.DeepEqual(got, c.want) {
			t.Errorf("LaunderCommand(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	// Outside a router, or on Linux, commands are left alone.
	none := func(string) string { return "" }
	if got := LaunderCommand([]string{"sh", "-c", "x"}, none, look); !reflect.DeepEqual(got, []string{"sh", "-c", "x"}) {
		t.Errorf("no router: %q", got)
	}
	withGOOS(t, "linux")
	if got := LaunderCommand([]string{"sh", "-c", "x"}, getenv, look); !reflect.DeepEqual(got, []string{"sh", "-c", "x"}) {
		t.Errorf("linux: %q", got)
	}
}
