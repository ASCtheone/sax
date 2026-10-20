package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"text/tabwriter"

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
func (daemonRunner) Kill(name string) { killSessionQuiet(name) }

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
		return next.WithOwner(host, saxName), nil
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

// netUp brings up the environment described by an env file: it creates or
// updates the router (the file is the source of truth for domain and routes)
// and starts every session that is not already running.
func netUp(w io.Writer, s vnet.Store, run sessionRunner, path string) error {
	e, err := loadEnvFile(path)
	if err != nil {
		return err
	}
	plan, warnings, err := e.Plan()
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	paths, err := installShim()
	if err != nil {
		return err
	}
	if w := supervisionWarning(paths); w != "" {
		warnings = append(warnings, w)
	}
	r, err := applyPlan(s, plan)
	if err != nil {
		return err
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SESSION\tIP\tSAX SESSION\tSTATUS")
	for _, host := range e.SessionNames() {
		saxName := vnet.SessionName(r.Name, host)
		status := "running"
		if !run.Alive(saxName) {
			if err := startEnvSession(run, s, r, paths, host, saxName, e.Sessions[host]); err != nil {
				_ = tw.Flush()
				return fmt.Errorf("start %s: %w", saxName, err)
			}
			status = "started"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", host, r.Sessions[host], saxName, status)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	printWarnings(w, warnings)
	printHostURLs(w, s, r)
	return ensureGateway(w, s, run)
}

func loadEnvFile(path string) (vnet.EnvFile, error) {
	if path == "" {
		path = vnet.EnvFileName
	}
	e, err := vnet.LoadEnvFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return vnet.EnvFile{}, fmt.Errorf("%s not found (pass -f <file>)", path)
	}
	return e, err
}

// applyPlan creates the router if needed and makes its domain, sessions and
// rules match the plan, keeping sessions that joined by hand.
func applyPlan(s vnet.Store, plan vnet.Router) (vnet.Router, error) {
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
		}
		for _, host := range plan.SortedSessions() {
			if next, _, err = next.WithSession(host, plan.Sessions[host]); err != nil {
				return cur, fmt.Errorf("session %s: %w", host, err)
			}
			next = next.WithOwner(host, vnet.SessionName(cur.Name, host))
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

func startEnvSession(run sessionRunner, s vnet.Store, r vnet.Router, paths shim.Paths,
	host, saxName string, spec vnet.SessionSpec) error {
	m := vnet.Member{Router: r.Name, Host: host, IP: r.Sessions[host], Subnet: r.Subnet, Dir: s.RouterDir(r.Name)}
	env := vnet.DaemonEnv(m, paths)
	keys := make([]string, 0, len(spec.Env))
	for k := range spec.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env = append(env, k+"="+spec.Env[k])
	}

	// The command runs through the user's shell; on macOS the daemon
	// re-exports the shim inside it (vnet.LaunderCommand).
	command := []string{userShell(), "-c", spec.Cmd}
	return run.Launch(saxName, command, spec.Cwd, env)
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
	hosts := r.SortedSessions()
	for _, host := range hosts {
		if owner, ok := r.Owners[host]; ok && run.Alive(owner) {
			run.Kill(owner)
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

// routerFromArgs resolves the router for `down`: a positional name or the
// router named in an env file.
func routerFromArgs(pos []string, file string) (string, error) {
	if len(pos) > 0 {
		return pos[0], nil
	}
	e, err := loadEnvFile(file)
	if err != nil {
		return "", err
	}
	return e.Router, nil
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
