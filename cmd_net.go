package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/asc/sax/internal/ipc"
	"github.com/asc/sax/internal/vnet"
)

const netUsage = `usage: sax net                                 list routers
       sax net create <router> [--subnet CIDR] [--domain D]
       sax net rm <router>
       sax net inspect <router>
       sax net map <router> <pattern> <target>   append a rule (first match wins)
       sax net unmap <router> <pattern>
       sax net check [-f file]                   validate an env file (default: ./.sax.lan)
       sax net up [-f file] [-d]                 run the apps as tabs of session <router> and attach
                                                 (default file: ./.sax.lan)
                                                 (-d: stay detached; sax -a <router> later)
       sax net down <router> | -f <file>         stop a router's sessions
       sax net publish <router> <host-port> <target>
                                                 forward 127.0.0.1:<host-port> into the router
       sax net unpublish <router> <host-port>
       sax net gateway [--port N]                run the HTTP router (normally auto-started)
       sax net register [domain...]              one sudo: http://<host>.lan in browsers (default: lan)
       sax net unregister [domain...]
       sax -x <name> --net <router>[:<host>] [--ip A] <cmd...>
                                                 run a session inside a router

from the host: http://<host>.<router>.localhost:7480 (HTTP routes) and published ports

rules: bob.lan/api/* -> api:3200   HTTP route, path forwarded unchanged
       db.lan:5432   -> db:5432    TCP mapping (no proxy)
       bob.lan       -> api:3200   TCP mapping on port 80`

// netFlagsWithValue are the sax net flags that consume the next argument.
var netFlagsWithValue = map[string]bool{"--subnet": true, "--domain": true, "-f": true, "--port": true, "--addr": true}

func vnetRoot() string { return filepath.Join(filepath.Dir(ipc.SessionsDir()), "vnet") }

func vnetStore() vnet.Store { return vnet.NewStore(vnetRoot()) }

func ipcSessionAlive(name string) bool { return ipc.IsSessionAlive(name) }

// doNet dispatches `sax net <command>`.
func doNet(args []string) {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
		args = args[1:]
	}
	pos := netPositional(args)

	var err error
	switch sub {
	case "", "ls", "list":
		err = netList(os.Stdout, vnetStore())
	case "create":
		err = netCreate(os.Stdout, vnetStore(), pos, flagValue(args, "--subnet"), flagValue(args, "--domain"))
	case "rm", "remove":
		err = netRemove(os.Stdout, vnetStore(), daemonRunner{}, pos)
	case "inspect", "show":
		err = netInspect(os.Stdout, vnetStore(), pos)
	case "map":
		if err = netMap(os.Stdout, vnetStore(), pos); err == nil {
			err = ensureGateway(os.Stdout, vnetStore(), daemonRunner{})
		}
	case "unmap":
		err = netUnmap(os.Stdout, vnetStore(), pos)
	case "check":
		err = netCheck(os.Stdout, flagValue(args, "-f"))
	case "publish":
		err = netPublish(os.Stdout, vnetStore(), daemonRunner{}, pos)
	case "unpublish":
		err = netUnpublish(os.Stdout, vnetStore(), pos)
	case "gateway":
		err = doNetGateway(args)
	case "register":
		err = netRegister(os.Stdout, vnetStore(), daemonRunner{}, pos)
	case "unregister":
		err = netUnregister(os.Stdout, vnetStore(), pos)
	case "up":
		err = netUp(os.Stdout, vnetStore(), daemonRunner{}, flagValue(args, "-f"), wantsAttach(args))
	case "down":
		var router string
		if router, err = routerFromArgs(pos, flagValue(args, "-f")); err == nil {
			err = netDown(os.Stdout, vnetStore(), daemonRunner{}, router)
		}
	case "help", "-h", "--help":
		fmt.Println(netUsage)
	default:
		err = fmt.Errorf("unknown command %q\n%s", sub, netUsage)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "sax net: %v\n", err)
		os.Exit(1)
	}
}

// netPositional returns args with flags (and their values) removed.
func netPositional(args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		if netFlagsWithValue[args[i]] {
			i++
			continue
		}
		if strings.HasPrefix(args[i], "-") && args[i] != "->" {
			continue
		}
		out = append(out, args[i])
	}
	return out
}

func requireArgs(pos []string, n int, usage string) error {
	if len(pos) < n {
		return fmt.Errorf("usage: sax net %s", usage)
	}
	return nil
}

func netList(w io.Writer, s vnet.Store) error {
	routers, err := s.List()
	if err != nil {
		return err
	}
	if len(routers) == 0 {
		fmt.Fprintln(w, "no routers (create one with: sax net create <name>)")
		fmt.Fprintln(w, registrationStatus(s))
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ROUTER\tSUBNET\tDOMAIN\tSESSIONS\tRULES")
	for _, r := range routers {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\n", r.Name, r.Subnet, dash(r.Domain), len(r.Sessions), len(r.Rules))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(w, "\n%s\n", registrationStatus(s))
	return nil
}

func netCreate(w io.Writer, s vnet.Store, pos []string, subnet, domain string) error {
	if err := requireArgs(pos, 1, "create <router> [--subnet CIDR] [--domain D]"); err != nil {
		return err
	}
	if subnet == "" {
		routers, err := s.List()
		if err != nil {
			return err
		}
		if subnet, err = vnet.PickSubnet(routers); err != nil {
			return err
		}
	}
	r, err := vnet.NewRouter(pos[0], subnet, domain)
	if err != nil {
		return err
	}
	if err := s.Create(r); err != nil {
		return err
	}
	fmt.Fprintf(w, "router %s created (%s)\n", r.Name, r.Subnet)
	warnHostOverlap(w, r.Subnet)
	return nil
}

func netRemove(w io.Writer, s vnet.Store, run sessionRunner, pos []string) error {
	if err := requireArgs(pos, 1, "rm <router>"); err != nil {
		return err
	}
	r, err := s.Get(pos[0])
	if err != nil {
		return err
	}
	if alive := runningOwners(r, run); len(alive) > 0 {
		return fmt.Errorf("router %s still has running sessions %v (sax net down %s first)", r.Name, alive, r.Name)
	}
	if err := s.Remove(pos[0]); err != nil {
		return err
	}
	fmt.Fprintf(w, "router %s removed\n", pos[0])
	return nil
}

func netInspect(w io.Writer, s vnet.Store, pos []string) error {
	if err := requireArgs(pos, 1, "inspect <router>"); err != nil {
		return err
	}
	r, err := s.Get(pos[0])
	if err != nil {
		return err
	}
	_, warnings := r.Tables()
	printRouter(w, r, warnings)
	printHostURLs(w, s, r)
	fmt.Fprintf(w, "\nstate: %s\n", s.RouterDir(r.Name))
	return nil
}

// ruleFromArgs accepts `<pattern> <target>`, `<pattern> -> <target>` or a
// single quoted "pattern -> target".
func ruleFromArgs(args []string) (string, error) {
	switch {
	case len(args) == 1 && strings.Contains(args[0], "->"):
		return args[0], nil
	case len(args) == 2:
		return args[0] + " -> " + args[1], nil
	case len(args) == 3 && args[1] == "->":
		return args[0] + " -> " + args[2], nil
	}
	return "", fmt.Errorf("usage: sax net map <router> <pattern> <target>")
}

func netMap(w io.Writer, s vnet.Store, pos []string) error {
	if err := requireArgs(pos, 2, "map <router> <pattern> <target>"); err != nil {
		return err
	}
	raw, err := ruleFromArgs(pos[1:])
	if err != nil {
		return err
	}
	r, err := s.Update(pos[0], func(r vnet.Router) (vnet.Router, error) { return r.WithRule(raw) })
	if err != nil {
		return err
	}
	added := r.Rules[len(r.Rules)-1]
	fmt.Fprintf(w, "rule %d: %s\n", len(r.Rules), added)
	_, warnings := r.Tables()
	printWarnings(w, warningsAbout(warnings, added))
	return nil
}

func netUnmap(w io.Writer, s vnet.Store, pos []string) error {
	if err := requireArgs(pos, 2, "unmap <router> <pattern>"); err != nil {
		return err
	}
	if _, err := s.Update(pos[0], func(r vnet.Router) (vnet.Router, error) { return r.WithoutRule(pos[1]) }); err != nil {
		return err
	}
	fmt.Fprintf(w, "rule %s removed from %s\n", pos[1], pos[0])
	return nil
}

func netCheck(w io.Writer, path string) error {
	path, err := resolveEnvPath(path)
	if err != nil {
		return err
	}
	e, err := loadEnvFile(path)
	if err != nil {
		return err
	}
	r, warnings, err := e.Plan()
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	printRouter(w, r, warnings)
	warnHostOverlap(w, r.Subnet)
	fmt.Fprintf(w, "\n%s: ok\n", path)
	return nil
}

func printRouter(w io.Writer, r vnet.Router, warnings []string) {
	fmt.Fprintf(w, "router %s  subnet %s  domain %s\n", r.Name, r.Subnet, dash(r.Domain))

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "\nSESSION\tIP")
	for _, name := range r.SortedSessions() {
		fmt.Fprintf(tw, "%s\t%s\n", name, r.Sessions[name])
	}
	if len(r.Sessions) == 0 {
		fmt.Fprintln(tw, "-\t-")
	}

	fmt.Fprintln(tw, "\n#\tRULE\tKIND\tVIP")
	rules, _ := r.ParsedRules()
	for i, rule := range rules {
		kind := "tcp"
		if rule.Kind == vnet.KindHTTP {
			kind = "http"
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\n", i+1, rule, kind, r.VIPs[rule.Host])
	}
	if len(rules) == 0 {
		fmt.Fprintln(tw, "-\t-\t-\t-")
	}
	_ = tw.Flush()
	printWarnings(w, warnings)
}

// warningsAbout keeps the warnings that mention rule, so `map` reports only
// what the new rule caused instead of every pending warning.
func warningsAbout(warnings []string, rule string) []string {
	var out []string
	for _, msg := range warnings {
		if strings.Contains(msg, fmt.Sprintf("%q", rule)) {
			out = append(out, msg)
		}
	}
	return out
}

func printWarnings(w io.Writer, warnings []string) {
	for _, msg := range warnings {
		fmt.Fprintf(w, "warning: %s\n", msg)
	}
}

func warnHostOverlap(w io.Writer, subnet string) {
	p, err := vnet.ParseSubnet(subnet)
	if err != nil {
		return
	}
	for _, h := range vnet.HostOverlaps(p) {
		fmt.Fprintf(w, "warning: %s overlaps host network %s; sessions in this router cannot reach those real hosts\n", subnet, h)
	}
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
