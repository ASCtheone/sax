package vnet

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	// EnvFileName is the file `sax net up` looks for when -f is not given.
	EnvFileName = ".sax.lan"
	// LegacyEnvFileName is still picked up when there is no EnvFileName
	// (the default name of sax v1.4 and v1.5).
	LegacyEnvFileName = "sax.net.yaml"
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

// EnvFile is a parsed .sax.lan (YAML): one router, its sessions and its ordered
// routes. Routes are a YAML list so their written order is the match order.
// It is also one project of a projects file (see Config).
type EnvFile struct {
	Router string `yaml:"router"`
	Subnet string `yaml:"subnet"`
	Domain string `yaml:"domain"`
	// Dir is the folder relative session cwds (and the banner file) resolve
	// against; default: the file's folder. "~/" means the home folder.
	Dir      string                 `yaml:"dir"`
	Sessions map[string]SessionSpec `yaml:"sessions"`
	Routes   []string               `yaml:"routes"`
	Publish  []string               `yaml:"publish"`
	Banner   Banner                 `yaml:"banner"`

	// Roots are the folders the file names for this project (dir and
	// explicit session cwds), resolved. Config.ForDir matches against them.
	Roots []string `yaml:"-"`
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

// LoadEnvFile reads a single-project env file. Session working directories
// are resolved relative to the file's directory (or its dir:), defaulting to
// it. A projects file is refused with the list of its projects.
func LoadEnvFile(path string) (EnvFile, error) {
	c, err := LoadConfig(path)
	if err != nil {
		return EnvFile{}, err
	}
	if c.Multi {
		return EnvFile{}, fmt.Errorf("%s has several projects (%s): pick one", path, strings.Join(c.Names(), ", "))
	}
	e, _ := c.Project(c.Names()[0])
	return e, nil
}

// resolvePaths makes the env file's folders absolute: dir against the file's
// folder, then session cwds and the banner file against dir.
func (e EnvFile) resolvePaths(fileDir string) EnvFile {
	base := fileDir
	var roots []string
	if e.Dir != "" {
		base = expandPath(e.Dir, fileDir)
		e.Dir = base
		roots = append(roots, base)
	}
	resolved := make(map[string]SessionSpec, len(e.Sessions))
	for name, spec := range e.Sessions {
		if spec.Cwd == "" {
			spec.Cwd = base
		} else {
			spec.Cwd = expandPath(spec.Cwd, base)
			roots = append(roots, spec.Cwd)
		}
		resolved[name] = spec
	}
	e.Sessions = resolved
	if e.Banner.File != "" {
		e.Banner.File = expandPath(e.Banner.File, base)
	}
	e.Roots = roots
	return e
}

// expandPath resolves p against base, expanding a leading "~/" to the home
// folder.
func expandPath(p, base string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[1:])
		}
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(base, p)
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
		if _, ok := r.Resolve(rule.Target); !ok {
			return Router{}, nil, fmt.Errorf("route %q: unknown target %q (not a session in this file)", rule, rule.Target.Host)
		}
	}
	for _, raw := range e.Publish {
		if r, err = r.WithPublish(raw); err != nil {
			return Router{}, nil, err
		}
		p, _ := ParsePublish(raw)
		if _, ok := r.Resolve(p.Target); !ok {
			return Router{}, nil, fmt.Errorf("publish %q: unknown target %q (not a session in this file)", p, p.Target.Host)
		}
	}

	r = r.WithBanner(e.Banner)
	for _, name := range names {
		r = r.WithDir(name, e.Sessions[name].Cwd)
	}
	_, warnings := r.Tables()
	return r, warnings, nil
}

// FindEnvFile returns the env file to use in dir: .sax.lan, or the legacy
// sax.net.yaml when only that exists.
func FindEnvFile(dir string) (string, error) {
	for _, name := range []string{EnvFileName, LegacyEnvFileName} {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("no %s in %s (create one or pass -f <file>)", EnvFileName, dir)
}
