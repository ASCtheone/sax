package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/asc/sax/internal/ipc"
	"github.com/asc/sax/internal/server"
	"github.com/asc/sax/internal/vnet"
	"github.com/asc/sax/internal/vnet/shim"
)

// shimDirName holds compiled shims next to the routers. The underscore keeps
// it out of the router namespace (router names are DNS labels).
const shimDirName = "_lib"

// sessionRunner starts and stops sax sessions; tests swap in a fake.
type sessionRunner interface {
	Alive(name string) bool
	Launch(name string, command []string, workDir string, env []string) error
	// LaunchTabs starts a session with one named tab per command.
	LaunchTabs(name string, tabs []server.TabSpec) error
	// AddTab adds a tab to a running session unless one with that name
	// exists (existed reports which).
	AddTab(name string, tab server.TabSpec) (existed bool, err error)
	// Attach opens the session in this terminal until the user detaches.
	Attach(name string)
	Kill(name string)
}

type daemonRunner struct{}

func (daemonRunner) Alive(name string) bool { return ipcSessionAlive(name) }
func (daemonRunner) Launch(name string, command []string, workDir string, env []string) error {
	if err := launchDaemon(name, command, workDir, env...); err != nil {
		return err
	}
	if !waitForSession(name) {
		return fmt.Errorf("timed out waiting for session %q", name)
	}
	return nil
}
func (r daemonRunner) LaunchTabs(name string, tabs []server.TabSpec) error {
	env, err := initTabsEnv(tabs)
	if err != nil {
		return err
	}
	return r.Launch(name, nil, "", []string{env})
}
func (daemonRunner) AddTab(name string, tab server.TabSpec) (bool, error) {
	data, err := json.Marshal(tab)
	if err != nil {
		return false, fmt.Errorf("encode tab: %w", err)
	}
	reply := ipc.QuerySessionTyped(name, "addtab", string(data))
	if reply == nil {
		return false, fmt.Errorf("session %q did not answer", name)
	}
	if _, understood := reply["ok"]; !understood {
		return false, errAddTabUnsupported // a session from an older sax
	}
	if ok, _ := reply["ok"].(bool); !ok {
		msg, _ := reply["error"].(string)
		return false, fmt.Errorf("add tab %s: %s", tab.Name, msg)
	}
	existed, _ := reply["existed"].(bool)
	return existed, nil
}
func (daemonRunner) Attach(name string) { connectClient(name) }
func (daemonRunner) Kill(name string)   { killSessionQuiet(name) }

// errAddTabUnsupported means the session predates adding tabs over IPC
// (e.g. a v1.6 view whose tabs were nested sax clients).
var errAddTabUnsupported = errors.New("session cannot add tabs")

// installShim is swapped in tests to avoid compiling the C shim.
var installShim = func() (shim.Paths, error) {
	return shim.Install(filepath.Join(vnetRoot(), shimDirName))
}

// supervisionAvailable is swapped in tests.
var supervisionAvailable = shim.SupervisionAvailable

// supervisionWarning explains, on Linux, when binaries that skip libc will
// not get virtual addresses; "" when everything is supported.
func supervisionWarning(p shim.Paths) string {
	if p.Supervisor == "" || supervisionAvailable(p) {
		return ""
	}
	return "seccomp supervision is not permitted here (Yama ptrace_scope >= 2 or a container seccomp " +
		"profile); using LD_PRELOAD, so Go and other static binaries will not get virtual addresses"
}

// netJoin reserves an address in a router for a sax session started with
// --net <router>[:<name>] and returns the env for its daemon.
func netJoin(s vnet.Store, saxName, netFlag, ip string) ([]string, vnet.Member, error) {
	routerName, host, err := vnet.ParseNetFlag(netFlag)
	if err != nil {
		return nil, vnet.Member{}, err
	}
	if host == "" {
		if host, err = vnet.HostFromSessionName(saxName); err != nil {
			return nil, vnet.Member{}, err
		}
	}
	paths, err := installShim()
	if err != nil {
		return nil, vnet.Member{}, err
	}
	if w := supervisionWarning(paths); w != "" {
		fmt.Fprintf(os.Stderr, "sax: warning: %s\n", w)
	}

	var assigned string
	r, err := s.Update(routerName, func(r vnet.Router) (vnet.Router, error) {
		if owner, taken := r.Owners[host]; taken && owner != saxName {
			return r, fmt.Errorf("%s is already used by session %q in router %s", host, owner, r.Name)
		}
		next, got, err := r.WithSession(host, ip)
		if err != nil {
			return r, err
		}
		assigned = got
		dir, _ := os.Getwd()
		return next.WithOwner(host, saxName).WithDir(host, dir), nil
	})
	if errors.Is(err, vnet.ErrNotFound) {
		return nil, vnet.Member{}, fmt.Errorf("%w (create it with: sax net create %s)", err, routerName)
	}
	if err != nil {
		return nil, vnet.Member{}, err
	}

	m := vnet.Member{Router: r.Name, Host: host, IP: assigned, Subnet: r.Subnet, Dir: s.RouterDir(r.Name)}
	return vnet.DaemonEnv(m, paths), m, nil
}

// netUp brings up the environment of a project (see resolveProject): it
// creates or updates the router (the file is the source of truth for domain
// and routes) and runs every app as a tab of the router's session, adding
// tabs for apps that are not running yet. With attach it then opens that
// session.
func netUp(w io.Writer, s vnet.Store, run sessionRunner, ref projectRef, attach bool) error {
	p, err := resolveProject(ref)
	if err != nil {
		return err
	}
	e := p.EnvFile
	plan, warnings, err := e.Plan()
	if err != nil {
		return fmt.Errorf("%s: %w", p.label(), err)
	}
	if src, err := filepath.Abs(p.Source); err == nil {
		plan = plan.WithSource(src, p.Name)
	}
	paths, err := installShim()
	if err != nil {
		return err
	}
	if w := supervisionWarning(paths); w != "" {
		warnings = append(warnings, w)
	}
	stopLegacyApps(w, s, run, plan)
	r, err := applyPlan(s, plan, explicitIPs(e))
	if err != nil {
		return err
	}

	tabs := make([]server.TabSpec, 0, len(e.Sessions))
	for _, host := range e.SessionNames() {
		tabs = append(tabs, appTab(s, r, paths, host, e.Sessions[host]))
	}
	status, err := startView(s, run, r, tabs)
	if err != nil {
		return err
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "APP\tIP\tSTATUS")
	for _, host := range e.SessionNames() {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", host, r.Sessions[host], status[host])
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	printWarnings(w, warnings)
	printHostURLs(w, s, r)
	if err := ensureGateway(w, s, run); err != nil {
		return err
	}
	fmt.Fprintf(w, "session %s: one tab per app (Ctrl+S n/p or 1-9 to switch, Ctrl+S d to detach; "+
		"later: sax -a %s)\n", r.Name, r.Name)
	if attach {
		run.Attach(r.Name)
	}
	return nil
}

// appTab is an app's tab: an interactive shell inside the app's network with
// the app command typed in, so Ctrl+C stops the app but keeps the tab (and
// its output), and Up+Enter starts it again.
func appTab(s vnet.Store, r vnet.Router, paths shim.Paths, host string, spec vnet.SessionSpec) server.TabSpec {
	m := vnet.Member{Router: r.Name, Host: host, IP: r.Sessions[host], Subnet: r.Subnet, Dir: s.RouterDir(r.Name)}
	env := vnet.DaemonEnv(m, paths)
	// oh-my-zsh's "update? [Y/n]" prompt would swallow the typed app
	// command; the file's env can still turn updates back on.
	env = append(env, "DISABLE_AUTO_UPDATE=true")
	keys := make([]string, 0, len(spec.Env))
	for k := range spec.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env = append(env, k+"="+spec.Env[k])
	}

	vars := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		vars[k] = v
	}
	getenv := func(k string) string {
		if v, ok := vars[k]; ok {
			return v
		}
		return os.Getenv(k) // the user's own NODE_OPTIONS, ZDOTDIR, ...
	}
	env = append(env, vnet.PaneEnv(getenv)...)

	cmd := []string{userShell()}
	if wrapper := vnet.PaneWrapper(getenv); wrapper != nil {
		cmd = append(append([]string{}, wrapper...), cmd...)
	}
	return server.TabSpec{Name: host, Cmd: cmd, Dir: spec.Cwd, Env: env, Input: spec.Cmd}
}

// stopLegacyApps stops per-app sessions started by sax v1.6 ("<router>.<app>"),
// which the router's tabs now replace.
func stopLegacyApps(w io.Writer, s vnet.Store, run sessionRunner, plan vnet.Router) {
	cur, err := s.Get(plan.Name)
	if err != nil {
		return
	}
	for _, host := range cur.SortedSessions() {
		owner := cur.Owners[host]
		if owner == vnet.SessionName(cur.Name, host) && run.Alive(owner) {
			run.Kill(owner)
			fmt.Fprintf(w, "stopped %s (apps now run as tabs of %s)\n", owner, cur.Name)
		}
	}
}

// explicitIPs lists the apps whose address the env file fixes with ip:.
func explicitIPs(e vnet.EnvFile) map[string]bool {
	out := map[string]bool{}
	for host, spec := range e.Sessions {
		if spec.IP != "" {
			out[host] = true
		}
	}
	return out
}

// applyPlan creates the router if needed and makes its domain, sessions and
// rules match the plan, keeping sessions that joined by hand. Apps that
// already have an address keep it unless the file fixes another one, so
// adding an app never moves running ones.
func applyPlan(s vnet.Store, plan vnet.Router, explicit map[string]bool) (vnet.Router, error) {
	if _, err := s.Get(plan.Name); errors.Is(err, vnet.ErrNotFound) {
		fresh, err := vnet.NewRouter(plan.Name, plan.Subnet, plan.Domain)
		if err != nil {
			return vnet.Router{}, err
		}
		if err := s.Create(fresh); err != nil && !errors.Is(err, vnet.ErrExists) {
			return vnet.Router{}, err
		}
	} else if err != nil {
		return vnet.Router{}, err
	}

	return s.Update(plan.Name, func(cur vnet.Router) (vnet.Router, error) {
		if cur.Subnet != plan.Subnet {
			return cur, fmt.Errorf("router %s already exists with subnet %s, file says %s (sax net rm %s first)",
				cur.Name, cur.Subnet, plan.Subnet, cur.Name)
		}
		next, err := vnet.NewRouter(cur.Name, cur.Subnet, plan.Domain)
		if err != nil {
			return cur, err
		}
		next = next.WithView(cur.View).WithBanner(plan.Banner).WithSource(plan.Source, plan.Project)
		// Hand-joined sessions keep their addresses; planned ones are placed
		// after them so a clash is reported instead of silently moved.
		for _, host := range cur.SortedSessions() {
			if _, planned := plan.Sessions[host]; planned {
				continue
			}
			next, _, _ = next.WithSession(host, cur.Sessions[host])
			if owner, ok := cur.Owners[host]; ok {
				next = next.WithOwner(host, owner)
			}
			next = next.WithDir(host, cur.Dirs[host])
		}
		// Fixed addresses first, then the ones apps already have, then new apps.
		hosts := plan.SortedSessions()
		place := func(host, ip string) error {
			var err error
			if next, _, err = next.WithSession(host, ip); err != nil {
				return fmt.Errorf("session %s: %w", host, err)
			}
			next = next.WithOwner(host, cur.Name).WithDir(host, plan.Dirs[host]) // a tab of the router's session
			return nil
		}
		for _, host := range hosts {
			if explicit[host] {
				if err := place(host, plan.Sessions[host]); err != nil {
					return cur, err
				}
			}
		}
		for _, host := range hosts {
			if ip, had := cur.Sessions[host]; had && !explicit[host] {
				if err := place(host, ip); err != nil {
					return cur, err
				}
			}
		}
		for _, host := range hosts {
			if _, placed := next.Sessions[host]; !placed {
				if err := place(host, ""); err != nil {
					return cur, err
				}
			}
		}
		for _, raw := range plan.Rules {
			if next, err = next.WithRule(raw); err != nil {
				return cur, err
			}
		}
		for _, raw := range plan.Publish {
			if next, err = next.WithPublish(raw); err != nil {
				return cur, err
			}
		}
		return next, nil
	})
}

func userShell() string {
	if sh := os.Getenv("SHELL"); sh != "" {
		return sh
	}
	return "/bin/sh"
}

// netDown stops the sessions a router owns and releases their addresses.
// Rules stay, so the next `up` restores the same names.
func netDown(w io.Writer, s vnet.Store, run sessionRunner, routerName string) error {
	if routerName == "" {
		return fmt.Errorf("usage: sax net down <router> | -f <file>")
	}
	r, err := s.Get(routerName)
	if err != nil {
		return err
	}
	if err := stopView(w, s, run, r); err != nil {
		return err
	}
	hosts := r.SortedSessions()
	stopped := map[string]bool{}
	for _, host := range hosts {
		owner, ok := r.Owners[host]
		if ok && !stopped[owner] && run.Alive(owner) {
			run.Kill(owner)
			stopped[owner] = true
			fmt.Fprintf(w, "stopped %s\n", owner)
		}
	}
	_, err = s.Update(routerName, func(cur vnet.Router) (vnet.Router, error) {
		next := cur
		for _, host := range hosts {
			next = next.WithoutSession(host)
		}
		return next, nil
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "router %s is down (%d sessions released)\n", routerName, len(hosts))
	return nil
}

// routerFromArgs resolves the router for `down`: a router name, a project
// name, or the project an env file (or this folder) gives.
func routerFromArgs(s vnet.Store, ref projectRef) (string, error) {
	if ref.Name != "" && ref.File == "" {
		return routerFor(s, ref.Name)
	}
	p, err := resolveProject(ref)
	if err != nil {
		return "", err
	}
	return p.Router, nil
}

// runningOwners lists sax sessions of a router that are still alive.
func runningOwners(r vnet.Router, run sessionRunner) []string {
	var alive []string
	for _, host := range r.SortedSessions() {
		if owner, ok := r.Owners[host]; ok && run.Alive(owner) {
			alive = append(alive, owner)
		}
	}
	return alive
}

// netEnvFor handles --net/--ip for `sax -x` and `sax -c`: it joins the router
// and returns the daemon env, exiting with a message on failure.
func netEnvFor(saxName, netFlag, ip string) []string {
	if netFlag == "" {
		if ip != "" {
			fmt.Fprintln(os.Stderr, "sax: --ip requires --net <router>")
			os.Exit(1)
		}
		return nil
	}
	env, m, err := netJoin(vnetStore(), saxName, netFlag, ip)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sax: --net: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Session %q joins router %s as %s (%s)\n", saxName, m.Router, m.Host, m.IP)
	if err := ensureGateway(os.Stdout, vnetStore(), daemonRunner{}); err != nil {
		fmt.Fprintf(os.Stderr, "sax: --net: %v\n", err)
	}
	return env
}

// mcpNetJoin adapts netJoin for the MCP sax_exec/sax_create_session tools.
func mcpNetJoin(saxName, netFlag, ip string) ([]string, error) {
	env, _, err := netJoin(vnetStore(), saxName, netFlag, ip)
	if err != nil {
		return nil, err
	}
	return env, ensureGateway(io.Discard, vnetStore(), daemonRunner{})
}
