package vnet

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

const (
	// EnvFileName is the file `sax net up` looks for by default.
	EnvFileName = "sax.net.yaml"
	// DefaultSubnet is used when an env file or `sax net create` gives none.
	DefaultSubnet = "10.77.0.0/24"
)

// SessionSpec describes one session of an environment.
type SessionSpec struct {
	IP  string            `yaml:"ip"`
	Cwd string            `yaml:"cwd"`
	Cmd string            `yaml:"cmd"`
	Env map[string]string `yaml:"env"`
}

// EnvFile is a parsed sax.net.yaml: one router, its sessions and its ordered
// routes. Routes are a YAML list so their written order is the match order.
type EnvFile struct {
	Router   string                 `yaml:"router"`
	Subnet   string                 `yaml:"subnet"`
	Domain   string                 `yaml:"domain"`
	Sessions map[string]SessionSpec `yaml:"sessions"`
	Routes   []string               `yaml:"routes"`
}

// ParseEnvFile decodes an env file strictly: unknown keys are errors, so a
// typo never silently drops configuration.
func ParseEnvFile(data []byte) (EnvFile, error) {
	var e EnvFile
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&e); err != nil && !errors.Is(err, io.EOF) {
		return EnvFile{}, fmt.Errorf("parse env file: %w", err)
	}
	if e.Router == "" {
		return EnvFile{}, fmt.Errorf("env file: missing required key \"router\"")
	}
	return e, nil
}

// LoadEnvFile reads and parses path. Session working directories are resolved
// relative to the file's directory, defaulting to that directory.
func LoadEnvFile(path string) (EnvFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return EnvFile{}, fmt.Errorf("read env file: %w", err)
	}
	e, err := ParseEnvFile(data)
	if err != nil {
		return EnvFile{}, fmt.Errorf("%s: %w", path, err)
	}
	base, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return EnvFile{}, fmt.Errorf("resolve env file dir: %w", err)
	}

	resolved := make(map[string]SessionSpec, len(e.Sessions))
	for name, spec := range e.Sessions {
		switch {
		case spec.Cwd == "":
			spec.Cwd = base
		case !filepath.IsAbs(spec.Cwd):
			spec.Cwd = filepath.Join(base, spec.Cwd)
		}
		resolved[name] = spec
	}
	e.Sessions = resolved
	return e, nil
}

// SessionNames returns session names in a stable order.
func (e EnvFile) SessionNames() []string {
	names := make([]string, 0, len(e.Sessions))
	for n := range e.Sessions {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Plan builds the router the env file describes: sessions with explicit IPs
// are placed first, then the rest in name order, then routes in written order.
// Unlike `sax net map`, every route target must be a session of the file (or
// an address in the subnet), because the file is the whole environment.
func (e EnvFile) Plan() (Router, []string, error) {
	subnet := e.Subnet
	if subnet == "" {
		subnet = DefaultSubnet
	}
	r, err := NewRouter(e.Router, subnet, e.Domain)
	if err != nil {
		return Router{}, nil, err
	}

	names := e.SessionNames()
	for _, explicit := range []bool{true, false} {
		for _, name := range names {
			spec := e.Sessions[name]
			if (spec.IP != "") != explicit {
				continue
			}
			if spec.Cmd == "" {
				return Router{}, nil, fmt.Errorf("session %s: missing \"cmd\"", name)
			}
			if r, _, err = r.WithSession(name, spec.IP); err != nil {
				return Router{}, nil, fmt.Errorf("session %s: %w", name, err)
			}
		}
	}

	for _, raw := range e.Routes {
		if r, err = r.WithRule(raw); err != nil {
			return Router{}, nil, err
		}
	}

	rules, _ := r.ParsedRules()
	for _, rule := range rules {
		if _, ok := r.resolve(rule.Target); !ok {
			return Router{}, nil, fmt.Errorf("route %q: unknown target %q (not a session in this file)", rule, rule.Target.Host)
		}
	}

	_, warnings := r.Tables()
	return r, warnings, nil
}
