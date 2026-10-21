package vnet

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// GlobalEnvFileName is the projects file in the home folder that `sax net`
// reads next to the current folder's .sax.lan.
const GlobalEnvFileName = ".sax.net"

// GlobalEnvFile returns the path of ~/.sax.net ("" without a home folder).
func GlobalEnvFile() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, GlobalEnvFileName)
}

// Config is a loaded env file. It holds one project (a .sax.lan with
// router:) or several:
//
//	projects:
//	  dev1: { dir: ~/code/dev1, sessions: {...}, routes: [...] }
//	  dev2: { router: other, sessions: {...} }
//
// Each project takes the keys of a .sax.lan; its router defaults to the
// project name.
type Config struct {
	Path  string
	Multi bool // written with projects:
	// Projects by name; a single-project file is keyed by its router.
	Projects map[string]EnvFile
}

type configFile struct {
	EnvFile  `yaml:",inline"`
	Projects map[string]EnvFile `yaml:"projects"`
}

// LoadConfig reads and parses an env file, resolving every project's folders
// (see EnvFile.Dir).
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read env file: %w", err)
	}
	c, err := parseConfig(data)
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	base, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return Config{}, fmt.Errorf("resolve env file dir: %w", err)
	}
	c.Path = path
	for name, e := range c.Projects {
		c.Projects[name] = e.resolvePaths(base)
	}
	return c, nil
}

// parseConfig decodes strictly (unknown keys are errors, at every level).
func parseConfig(data []byte) (Config, error) {
	var raw configFile
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&raw); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("parse env file: %w", err)
	}

	if raw.Projects == nil {
		if raw.Router == "" {
			return Config{}, fmt.Errorf("env file: missing required key \"router\" (or \"projects\")")
		}
		return Config{Projects: map[string]EnvFile{raw.Router: raw.EnvFile}}, nil
	}
	if !reflect.DeepEqual(raw.EnvFile, EnvFile{}) {
		return Config{}, fmt.Errorf("env file: use either router: ... or projects: ..., not both")
	}
	if len(raw.Projects) == 0 {
		return Config{}, fmt.Errorf("env file: projects is empty")
	}
	for name, e := range raw.Projects {
		if err := ValidateName("project", name); err != nil {
			return Config{}, err
		}
		if e.Router == "" {
			e.Router = name
		}
		raw.Projects[name] = e
	}
	return Config{Multi: true, Projects: raw.Projects}, nil
}

// Names returns the project names in order.
func (c Config) Names() []string {
	names := make([]string, 0, len(c.Projects))
	for n := range c.Projects {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Project returns the named project.
func (c Config) Project(name string) (EnvFile, bool) {
	e, ok := c.Projects[name]
	return e, ok
}

// ForDir returns the project whose folders (dir, explicit session cwds)
// contain dir, the deepest folder winning; "" when none does. Two projects
// on the same deepest folder are an error.
func (c Config) ForDir(dir string) (string, error) {
	dir = filepath.Clean(dir)
	best, depth := []string(nil), -1
	for _, name := range c.Names() {
		d := -1
		for _, root := range c.Projects[name].Roots {
			if within(dir, root) && len(root) > d {
				d = len(root)
			}
		}
		switch {
		case d < 0:
		case d > depth:
			best, depth = []string{name}, d
		case d == depth:
			best = append(best, name)
		}
	}
	switch len(best) {
	case 0:
		return "", nil
	case 1:
		return best[0], nil
	}
	return "", fmt.Errorf("%s belongs to several projects of %s (%s): pick one", dir, c.Path, strings.Join(best, ", "))
}

// within reports whether dir is root or inside it.
func within(dir, root string) bool {
	rel, err := filepath.Rel(root, dir)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
