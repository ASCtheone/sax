package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/asc/sax/internal/vnet"
)

// globalEnvFile is ~/.sax.net; swapped in tests.
var globalEnvFile = vnet.GlobalEnvFile

// projectRef says which project a net command is about: an explicit file
// (-f), a project name, and the folder the command runs in.
type projectRef struct {
	File string
	Name string
	Dir  string
}

// refFromArgs builds a projectRef from `sax net <cmd> [project] [-f file]`.
func refFromArgs(pos []string, file string) projectRef {
	ref := projectRef{File: file}
	if len(pos) > 0 {
		ref.Name = pos[0]
	}
	ref.Dir, _ = os.Getwd()
	return ref
}

// project is a resolved project and where it came from.
type project struct {
	vnet.EnvFile
	Name   string
	Source string // file path, for messages
	Multi  bool   // from a projects file
}

// resolveProject finds the project to use:
//   - with -f: that file (a projects file needs a name, or a project
//     whose folders contain the current folder)
//   - with a name: that project of ./.sax.lan, else of ~/.sax.net
//   - else ./.sax.lan, else the project of ~/.sax.net covering this folder
func resolveProject(ref projectRef) (project, error) {
	if ref.Dir == "" {
		ref.Dir, _ = os.Getwd()
	}
	if ref.File != "" {
		c, err := loadConfig(ref.File)
		if err != nil {
			return project{}, err
		}
		return pickProject(c, ref)
	}

	configs, err := configsFor(ref.Dir)
	if err != nil {
		return project{}, err
	}
	if ref.Name != "" {
		for _, c := range configs {
			if e, ok := c.Project(ref.Name); ok {
				return project{EnvFile: e, Name: ref.Name, Source: c.Path, Multi: c.Multi}, nil
			}
		}
		return project{}, fmt.Errorf("no project %q (%s)", ref.Name, describeConfigs(configs, ref.Dir))
	}
	if len(configs) == 0 {
		return project{}, fmt.Errorf("no %s in %s and no %s (create one or pass -f <file>)",
			vnet.EnvFileName, ref.Dir, displayPath(globalEnvFile()))
	}
	if c := configs[0]; !isGlobal(c) {
		return pickProject(c, ref)
	}
	c := configs[0]
	name, err := c.ForDir(ref.Dir)
	if err != nil {
		return project{}, err
	}
	if name == "" {
		return project{}, fmt.Errorf("no %s in %s and no project of %s covers it: sax net up <project> (%s)",
			vnet.EnvFileName, ref.Dir, displayPath(c.Path), strings.Join(c.Names(), ", "))
	}
	e, _ := c.Project(name)
	return project{EnvFile: e, Name: name, Source: c.Path, Multi: c.Multi}, nil
}

// pickProject chooses a project of one file: by name, the only one, or the
// one covering ref.Dir.
func pickProject(c vnet.Config, ref projectRef) (project, error) {
	name := ref.Name
	switch {
	case name != "":
	case !c.Multi:
		name = c.Names()[0]
	default:
		var err error
		if name, err = c.ForDir(ref.Dir); err != nil {
			return project{}, err
		}
		if name == "" {
			return project{}, fmt.Errorf("%s has several projects: pick one (%s)", displayPath(c.Path), strings.Join(c.Names(), ", "))
		}
	}
	e, ok := c.Project(name)
	if !ok {
		return project{}, fmt.Errorf("no project %q in %s (%s)", name, displayPath(c.Path), strings.Join(c.Names(), ", "))
	}
	return project{EnvFile: e, Name: name, Source: c.Path, Multi: c.Multi}, nil
}

// configsFor loads the env files a folder sees: its own .sax.lan first,
// then ~/.sax.net. Missing files are skipped; broken ones are errors.
func configsFor(dir string) ([]vnet.Config, error) {
	if dir == "" {
		dir, _ = os.Getwd()
	}
	var out []vnet.Config
	if local, err := vnet.FindEnvFile(dir); err == nil {
		c, err := loadConfig(local)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if global := globalEnvFile(); global != "" {
		if _, err := os.Stat(global); err == nil && !sameFile(global, out) {
			c, err := loadConfig(global)
			if err != nil {
				return nil, err
			}
			out = append(out, c)
		}
	}
	return out, nil
}

func loadConfig(path string) (vnet.Config, error) {
	c, err := vnet.LoadConfig(path)
	if errors.Is(err, os.ErrNotExist) {
		return vnet.Config{}, fmt.Errorf("%s not found", path)
	}
	return c, err
}

func isGlobal(c vnet.Config) bool { return sameFile(c.Path, []vnet.Config{{Path: globalEnvFile()}}) }

// sameFile reports whether path is one of the configs' files.
func sameFile(path string, configs []vnet.Config) bool {
	a, err := os.Stat(path)
	if err != nil {
		return false
	}
	for _, c := range configs {
		if b, err := os.Stat(c.Path); err == nil && os.SameFile(a, b) {
			return true
		}
	}
	return false
}

func describeConfigs(configs []vnet.Config, dir string) string {
	if len(configs) == 0 {
		return fmt.Sprintf("no %s in %s and no %s", vnet.EnvFileName, dir, displayPath(globalEnvFile()))
	}
	parts := make([]string, len(configs))
	for i, c := range configs {
		parts[i] = displayPath(c.Path) + ": " + strings.Join(c.Names(), ", ")
	}
	return strings.Join(parts, "; ")
}

// displayPath shortens the home folder to ~.
func displayPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(p, home+string(os.PathSeparator)) {
		return "~" + p[len(home):]
	}
	return p
}

// netProjects lists the projects this folder can start: ./.sax.lan's and
// ~/.sax.net's, marking the one plain `sax net up` would pick.
func netProjects(w io.Writer, run sessionRunner, dir string) error {
	configs, err := configsFor(dir)
	if err != nil {
		return err
	}
	if len(configs) == 0 {
		fmt.Fprintf(w, "no projects: no %s here and no %s\n", vnet.EnvFileName, displayPath(globalEnvFile()))
		return nil
	}
	here := ""
	if p, err := resolveProject(projectRef{Dir: dir}); err == nil {
		here = p.Source + "\x00" + p.Name
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PROJECT\tROUTER\tAPPS\tSTATE\tFILE")
	for _, c := range configs {
		for _, name := range c.Names() {
			e, _ := c.Project(name)
			state := "-"
			if run.Alive(e.Router) {
				state = "up"
			}
			mark := ""
			if c.Path+"\x00"+name == here {
				mark = "  <- sax net up"
			}
			fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s%s\n", name, e.Router, len(e.Sessions), state, displayPath(c.Path), mark)
		}
	}
	return tw.Flush()
}

// label names the project in messages: its file, plus the project name for
// a projects file.
func (p project) label() string {
	if !p.Multi {
		return displayPath(p.Source)
	}
	return displayPath(p.Source) + " (" + p.Name + ")"
}
