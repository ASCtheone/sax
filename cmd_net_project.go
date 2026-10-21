package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/asc/sax/internal/vnet"
)

const projectUsage = `usage: sax net project ls
       sax net project add <name> [--dir D] [--router R] [--subnet CIDR] [--domain D]
                                   add to ~/.sax.net (-f file); dir defaults to this folder
       sax net project set <name> [--dir D] [--router R] [--subnet CIDR] [--domain D]
       sax net project rm <name>
       sax net project show <name>
       sax net project app add <project> <app> (--cmd "..." | --nx <project[:target]> | --npm <script>
                                   | -- <command...>)    exactly one; --nx api runs nx run api:serve,
                                   --npm start:dev runs npm run start:dev (words after -- are
                                   passed to the nx task or npm script)
                                   [--cwd D] [--ip A] [--env K=V]...
                                   [--port N [--route PATTERN]... [--publish HOST-PORT]...]
       sax net project app set <project> <app> [same flags] [--unset-env K]...
       sax net project app rm <project> <app>        also drops its routes and ports
       sax net project route add <project> <pattern> <app:port> [--at N]
       sax net project route rm <project> <pattern>
       sax net project port add <project> <host-port> <app:port>
       sax net project port rm <project> <host-port>

Edits go to the file the project lives in (from any folder), keep its
comments, and are checked before they are written. Apply them with
sax net up <project> (new apps, routes) or sax net restart <project> <app>.`

// projectFlagsWithValue are the sax net project flags that take a value.
var projectFlagsWithValue = map[string]bool{
	"-f": true, "--dir": true, "--router": true, "--subnet": true, "--domain": true,
	"--cmd": true, "--nx": true, "--npm": true, "--cwd": true, "--ip": true, "--env": true, "--unset-env": true,
	"--port": true, "--route": true, "--publish": true, "--at": true,
}

// cliArgs are parsed arguments: positionals, flag values (repeatable) and
// everything after "--".
type cliArgs struct {
	pos   []string
	flags map[string][]string
	rest  []string
}

func parseCLI(args []string, valued map[string]bool) (cliArgs, error) {
	c := cliArgs{flags: map[string][]string{}}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			c.rest = args[i+1:]
			return c, nil
		case valued[a]:
			if i+1 >= len(args) {
				return c, fmt.Errorf("%s needs a value", a)
			}
			c.flags[a] = append(c.flags[a], args[i+1])
			i++
		case strings.HasPrefix(a, "-") && a != "->":
			return c, fmt.Errorf("unknown flag %s", a)
		default:
			c.pos = append(c.pos, a)
		}
	}
	return c, nil
}

// one returns a flag's value as a set-pointer (nil when not given).
func (c cliArgs) one(flag string) *string {
	if v := c.flags[flag]; len(v) > 0 {
		s := v[len(v)-1]
		return &s
	}
	return nil
}

// netProject dispatches `sax net project ...`.
func netProject(w io.Writer, run sessionRunner, args []string) error {
	c, err := parseCLI(args, projectFlagsWithValue)
	if err != nil {
		return fmt.Errorf("%v\n%s", err, projectUsage)
	}
	if len(c.pos) == 0 || c.pos[0] == "ls" || c.pos[0] == "list" {
		dir, _ := os.Getwd()
		return netProjects(w, run, dir)
	}
	cmd, pos := c.pos[0], c.pos[1:]
	switch cmd {
	case "add", "set", "rm", "remove", "show":
		if len(pos) != 1 {
			return fmt.Errorf("usage: sax net project %s <name>\n%s", cmd, projectUsage)
		}
		return projectCmd(w, run, cmd, pos[0], c)
	case "app":
		return projectAppCmd(w, run, pos, c)
	case "route":
		return projectRouteCmd(w, run, pos, c)
	case "port":
		return projectPortCmd(w, run, pos, c)
	case "help", "-h", "--help":
		fmt.Fprintln(w, projectUsage)
		return nil
	}
	return fmt.Errorf("unknown command %q\n%s", cmd, projectUsage)
}

func projectCmd(w io.Writer, run sessionRunner, cmd, name string, c cliArgs) error {
	fields := vnet.ProjectFields{Router: c.one("--router"), Subnet: c.one("--subnet"), Domain: c.one("--domain")}
	if d := c.one("--dir"); d != nil {
		dir := projectDir(*d)
		fields.Dir = &dir
	}
	switch cmd {
	case "add":
		if fields.Dir == nil {
			cwd, _ := os.Getwd()
			dir := projectDir(cwd)
			fields.Dir = &dir
		}
		path := targetFile(c)
		if err := editConfig(path, name, func(d *vnet.ConfigDoc) error { return d.AddProject(name, fields) }); err != nil {
			return err
		}
		fmt.Fprintf(w, "project %s added to %s (dir %s)\nnext: sax net project app add %s <app> --cmd \"...\"\n",
			name, displayPath(path), *fields.Dir, name)
		return nil
	case "set":
		path, err := projectFile(c, name)
		if err != nil {
			return err
		}
		if err := editConfig(path, name, func(d *vnet.ConfigDoc) error { return d.SetProject(name, fields) }); err != nil {
			return err
		}
		fmt.Fprintf(w, "project %s updated in %s\n", name, displayPath(path))
		applyHint(w, run, name, "sax net down %[1]s && sax net up %[1]s")
		return nil
	case "rm", "remove":
		path, err := projectFile(c, name)
		if err != nil {
			return err
		}
		p, _ := resolveProject(projectRef{File: path, Name: name})
		if err := editConfig(path, "", func(d *vnet.ConfigDoc) error { return d.RemoveProject(name) }); err != nil {
			return err
		}
		fmt.Fprintf(w, "project %s removed from %s\n", name, displayPath(path))
		if p.Router != "" && run.Alive(p.Router) {
			fmt.Fprintf(w, "its router is still up: sax net down %s\n", p.Router)
		}
		return nil
	}
	return projectShow(w, run, name, c)
}

func projectShow(w io.Writer, run sessionRunner, name string, c cliArgs) error {
	p, err := resolveProject(projectRef{File: c.flagFile(), Name: name})
	if err != nil {
		return err
	}
	state := "down"
	if run.Alive(p.Router) {
		state = "up"
	}
	fmt.Fprintf(w, "project %s  (%s, %s)\n", name, displayPath(p.Source), state)
	if p.Dir != "" {
		fmt.Fprintf(w, "dir %s\n", displayPath(p.Dir))
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "\nAPP\tRUNS\tCWD")
	for _, app := range p.SessionNames() {
		spec := p.Sessions[app]
		kind, value, _ := spec.Run()
		fmt.Fprintf(tw, "%s\t%s: %s\t%s\n", app, kind, value, displayPath(spec.Cwd))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	r, warnings, err := p.Plan()
	if err != nil {
		return fmt.Errorf("%s: %w", p.label(), err)
	}
	fmt.Fprintln(w)
	printRouter(w, r, warnings)
	return nil
}

func projectAppCmd(w io.Writer, run sessionRunner, pos []string, c cliArgs) error {
	if len(pos) != 3 || (pos[0] != "add" && pos[0] != "set" && pos[0] != "rm" && pos[0] != "remove") {
		return fmt.Errorf("usage: sax net project app add|set|rm <project> <app> ...\n%s", projectUsage)
	}
	action, project, app := pos[0], pos[1], pos[2]
	path, err := projectFile(c, project)
	if err != nil {
		return err
	}

	if action == "rm" || action == "remove" {
		var dropped []string
		err := editConfig(path, project, func(d *vnet.ConfigDoc) error {
			var err error
			dropped, err = d.RemoveApp(project, app)
			return err
		})
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "app %s removed from project %s\n", app, project)
		for _, x := range dropped {
			fmt.Fprintf(w, "  dropped %s\n", x)
		}
		applyHint(w, run, project, "sax net down %[1]s && sax net up %[1]s -d") // up and restart keep a removed app running
		return nil
	}

	fields, err := appFields(c)
	if err != nil {
		return err
	}
	routes, ports := c.flags["--route"], c.flags["--publish"]
	port := c.one("--port")
	if port == nil && (len(routes) > 0 || len(ports) > 0) {
		return fmt.Errorf("--route and --publish need --port (the port %s listens on)", app)
	}
	var added []string
	err = editConfig(path, project, func(d *vnet.ConfigDoc) error {
		if err := d.SetApp(project, app, fields, action == "add"); err != nil {
			return err
		}
		for _, pattern := range routes {
			r, err := d.AddRoute(project, pattern, app+":"+*port, 0)
			if err != nil {
				return err
			}
			added = append(added, "route "+r)
		}
		for _, hostPort := range ports {
			p, err := d.AddPublish(project, hostPort, app+":"+*port)
			if err != nil {
				return err
			}
			added = append(added, "port "+p)
		}
		return nil
	})
	if err != nil {
		return err
	}
	verb := map[string]string{"add": "added to", "set": "updated in"}[action]
	fmt.Fprintf(w, "app %s %s project %s (%s)\n", app, verb, project, displayPath(path))
	for _, x := range added {
		fmt.Fprintf(w, "  %s\n", x)
	}
	if action == "add" {
		applyHint(w, run, project, "sax net up %[1]s -d")
	} else {
		applyHint(w, run, project, "sax net restart %[1]s "+app)
	}
	return nil
}

// appFields reads --cmd (or the words after --), --cwd, --ip, --env K=V and
// --unset-env K.
func appFields(c cliArgs) (vnet.AppFields, error) {
	f := vnet.AppFields{Cmd: c.one("--cmd"), NX: c.one("--nx"), NPM: c.one("--npm"),
		Cwd: c.one("--cwd"), IP: c.one("--ip"), UnsetEnv: c.flags["--unset-env"]}
	if len(c.rest) > 0 { // the command, or extra arguments for --nx/--npm
		extra := strings.Join(c.rest, " ")
		switch {
		case f.Cmd != nil:
			return f, fmt.Errorf("give the command with --cmd or after --, not both")
		case f.NX != nil:
			v := *f.NX + " " + extra
			f.NX = &v
		case f.NPM != nil:
			v := *f.NPM + " -- " + extra // npm run <script> -- <args>
			f.NPM = &v
		default:
			f.Cmd = &extra
		}
	}
	for _, kv := range c.flags["--env"] {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return f, fmt.Errorf("--env wants KEY=VALUE, got %q", kv)
		}
		if f.Env == nil {
			f.Env = map[string]string{}
		}
		f.Env[k] = v
	}
	return f, nil
}

func projectRouteCmd(w io.Writer, run sessionRunner, pos []string, c cliArgs) error {
	switch {
	case len(pos) == 4 && pos[0] == "add":
		path, err := projectFile(c, pos[1])
		if err != nil {
			return err
		}
		at := 0
		if v := c.one("--at"); v != nil {
			if at, err = strconv.Atoi(*v); err != nil || at < 1 {
				return fmt.Errorf("--at wants a position from 1, got %q", *v)
			}
		}
		var route string
		if err := editConfig(path, pos[1], func(d *vnet.ConfigDoc) error {
			var err error
			route, err = d.AddRoute(pos[1], pos[2], pos[3], at)
			return err
		}); err != nil {
			return err
		}
		fmt.Fprintf(w, "route %s added to project %s\n", route, pos[1])
	case len(pos) == 3 && (pos[0] == "rm" || pos[0] == "remove"):
		path, err := projectFile(c, pos[1])
		if err != nil {
			return err
		}
		if err := editConfig(path, pos[1], func(d *vnet.ConfigDoc) error { return d.RemoveRoute(pos[1], pos[2]) }); err != nil {
			return err
		}
		fmt.Fprintf(w, "route %s removed from project %s\n", pos[2], pos[1])
	default:
		return fmt.Errorf("usage: sax net project route add <project> <pattern> <app:port> [--at N] | rm <project> <pattern>")
	}
	applyHint(w, run, pos[1], "sax net up %[1]s -d")
	return nil
}

func projectPortCmd(w io.Writer, run sessionRunner, pos []string, c cliArgs) error {
	switch {
	case len(pos) == 4 && pos[0] == "add":
		path, err := projectFile(c, pos[1])
		if err != nil {
			return err
		}
		var entry string
		if err := editConfig(path, pos[1], func(d *vnet.ConfigDoc) error {
			var err error
			entry, err = d.AddPublish(pos[1], pos[2], pos[3])
			return err
		}); err != nil {
			return err
		}
		fmt.Fprintf(w, "port %s added to project %s (127.0.0.1:%s on this machine)\n", entry, pos[1], pos[2])
	case len(pos) == 3 && (pos[0] == "rm" || pos[0] == "remove"):
		path, err := projectFile(c, pos[1])
		if err != nil {
			return err
		}
		if err := editConfig(path, pos[1], func(d *vnet.ConfigDoc) error { return d.RemovePublish(pos[1], pos[2]) }); err != nil {
			return err
		}
		fmt.Fprintf(w, "port %s removed from project %s\n", pos[2], pos[1])
	default:
		return fmt.Errorf("usage: sax net project port add <project> <host-port> <app:port> | rm <project> <host-port>")
	}
	applyHint(w, run, pos[1], "sax net up %[1]s -d")
	return nil
}

func (c cliArgs) flagFile() string {
	if f := c.one("-f"); f != nil {
		return *f
	}
	return ""
}

// targetFile is -f, or ~/.sax.net for new projects.
func targetFile(c cliArgs) string {
	if f := c.flagFile(); f != "" {
		return f
	}
	return globalEnvFile()
}

// projectFile is the file an existing project lives in: -f, or wherever
// sax finds it by name (./.sax.lan, ~/.sax.net, a file brought up before).
func projectFile(c cliArgs, name string) (string, error) {
	if f := c.flagFile(); f != "" {
		return f, nil
	}
	p, err := resolveProject(projectRef{Name: name})
	if err != nil {
		return "", err
	}
	return p.Source, nil
}

// projectDir makes a --dir absolute (against this folder, not the file's)
// and writes the home folder as ~ so the file reads well and travels.
func projectDir(d string) string {
	if d == "~" || strings.HasPrefix(d, "~/") {
		return d
	}
	if abs, err := filepath.Abs(d); err == nil {
		d = abs
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if d == home {
			return "~"
		}
		if strings.HasPrefix(d, home+string(os.PathSeparator)) {
			return "~" + d[len(home):]
		}
	}
	return d
}

// editConfig applies edit to the env file at path and writes it back only
// if the whole file still loads and project (when it exists) still plans.
// The write is atomic and keeps the file's mode.
func editConfig(path, project string, edit func(*vnet.ConfigDoc) error) error {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	mode := os.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	d, err := vnet.ParseConfigDoc(data)
	if err != nil {
		return fmt.Errorf("%s: %w", displayPath(path), err)
	}
	if err := edit(d); err != nil {
		return err
	}
	out, err := d.Bytes()
	if err != nil {
		return err
	}

	tmp := fmt.Sprintf("%s.tmp-%d", path, os.Getpid())
	if err := os.WriteFile(tmp, out, mode); err != nil {
		return err
	}
	if err := checkEdited(tmp, path, project); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func checkEdited(tmp, path, project string) error {
	fix := func(err error) error {
		return errors.New(strings.ReplaceAll(err.Error(), tmp, displayPath(path)))
	}
	c, err := vnet.LoadConfig(tmp)
	if err != nil {
		return fix(err)
	}
	if e, ok := c.Project(project); ok {
		if _, _, err := e.Plan(); err != nil {
			return fmt.Errorf("not saved: project %s would be invalid: %w", project, err)
		}
	}
	return nil
}

// applyHint tells how to apply an edit when the project's router is up.
func applyHint(w io.Writer, run sessionRunner, project, format string) {
	p, err := resolveProject(projectRef{Name: project})
	if err != nil || !run.Alive(p.Router) {
		return
	}
	fmt.Fprintf(w, "%s is up; apply with: %s\n", project, fmt.Sprintf(format, project))
}
