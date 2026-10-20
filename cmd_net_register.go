package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/asc/sax/internal/vnet"
	"github.com/asc/sax/internal/vnet/gateway"
	"github.com/asc/sax/internal/vnet/hostdns"
)

// defaultDomain is registered when `sax net register` gets no argument.
const defaultDomain = "lan"

const (
	verifyAttempts = 10
	verifyPause    = 300 * time.Millisecond
	probeTimeout   = time.Second
)

// runPrivileged runs a command for hostdns.Apply; sudo asks for the password
// on the terminal. Tests replace it.
var runPrivileged hostdns.Runner = func(args []string, stdin string) error {
	cmd := exec.Command(args[0], args[1:]...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	} else {
		cmd.Stdin = os.Stdin
	}
	cmd.Stdout = io.Discard // tee echoes the file
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// systemResolves reports whether the OS resolver answers name with
// 127.0.0.1, i.e. the registration is live. Tests replace it.
var systemResolves = func(name string) bool {
	var cmd *exec.Cmd
	if hostOS == "darwin" {
		cmd = exec.Command("dscacheutil", "-q", "host", "-a", "name", name)
	} else {
		cmd = exec.Command("getent", "hosts", name)
	}
	out, err := cmd.Output()
	return err == nil && strings.Contains(string(out), "127.0.0.1")
}

// hostDNSAnswers reports whether a gateway with host DNS is listening.
var hostDNSAnswers = func(domain string) bool {
	res := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "udp", hostdns.DefaultDNSAddr)
	}}
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	ips, err := res.LookupHost(ctx, "sax-check."+domain)
	return err == nil && len(ips) > 0 && ips[0] == "127.0.0.1"
}

// netRegister routes lookups for the domains to sax (one sudo prompt), so
// browsers can open http://app1.lan.
func netRegister(w io.Writer, s vnet.Store, run sessionRunner, pos []string) error {
	domains := pos
	if len(domains) == 0 {
		domains = []string{defaultDomain}
	}
	reg, err := s.Registration()
	if err != nil {
		return err
	}
	next, err := reg.With(domains...)
	if err != nil {
		return err
	}
	plan, err := hostdns.RegisterPlan(hostdns.Env{GOOS: hostOS}, next.Domains, hostdns.DefaultDNSAddr)
	if errors.Is(err, hostdns.ErrUnsupported) {
		return fmt.Errorf("%w; keep using http://<host>.<router>.localhost%s", err, portSuffix(gateway.DefaultHostAddr))
	}
	if err != nil {
		return err
	}

	fmt.Fprintf(w, "sax needs sudo once to send %s lookups to sax:\n", dotted(domains))
	for _, f := range plan.Files {
		fmt.Fprintf(w, "  write %s\n", f.Path)
	}
	if err := hostdns.Apply(plan, runPrivileged); err != nil {
		return err
	}
	if err := s.SaveRegistration(next); err != nil {
		return err
	}
	if err := ensureHostDNSGateway(w, s, run, domains[0]); err != nil {
		return err
	}

	name := "sax-check." + domains[0]
	for i := 0; i < verifyAttempts && !systemResolves(name); i++ {
		time.Sleep(verifyPause)
	}
	if !systemResolves(name) {
		fmt.Fprintf(w, "warning: the system does not resolve %s yet; new browser windows may need a moment\n", name)
	}
	fmt.Fprintf(w, "registered %s: apps are now at http://<host>%s (e.g. http://app1.%s)\n",
		dotted(domains), portSuffixFor80(), domains[0])
	return nil
}

// netUnregister undoes netRegister for the given domains (default: all).
func netUnregister(w io.Writer, s vnet.Store, pos []string) error {
	reg, err := s.Registration()
	if err != nil {
		return err
	}
	removed := pos
	if len(removed) == 0 {
		removed = reg.Domains
	}
	if len(removed) == 0 {
		fmt.Fprintln(w, "no domains are registered")
		return nil
	}
	next := reg.Without(removed...)
	plan, err := hostdns.UnregisterPlan(hostdns.Env{GOOS: hostOS}, removed, next.Domains, hostdns.DefaultDNSAddr)
	if err != nil {
		return err
	}
	if err := hostdns.Apply(plan, runPrivileged); err != nil {
		return err
	}
	if err := s.SaveRegistration(next); err != nil {
		return err
	}
	fmt.Fprintf(w, "unregistered %s; apps stay reachable at http://<host>.<router>.localhost%s\n",
		dotted(removed), portSuffix(gatewayAddr()))
	return nil
}

// ensureHostDNSGateway starts the gateway, restarting one that predates
// registered domains (it would not answer their lookups).
func ensureHostDNSGateway(w io.Writer, s vnet.Store, run sessionRunner, domain string) error {
	if run.Alive(gateway.SessionName) && !hostDNSAnswers(domain) {
		run.Kill(gateway.SessionName)
		fmt.Fprintf(w, "restarting %s to serve .%s\n", gateway.SessionName, domain)
	}
	if err := ensureGateway(w, s, run); err != nil {
		return err
	}
	for i := 0; i < verifyAttempts && !hostDNSAnswers(domain); i++ {
		time.Sleep(verifyPause)
	}
	return nil
}

func dotted(domains []string) string {
	out := make([]string, len(domains))
	for i, d := range domains {
		out[i] = "." + d
	}
	return strings.Join(out, " ")
}

// hostURL is how a router's HTTP host is reached from the host machine:
// http://app1.lan when its domain is registered (app1.v2.lan if several
// routers have app1.lan), otherwise http://app1.lan.v2.localhost:7480.
func hostURL(s vnet.Store, reg vnet.Registration, r vnet.Router, host string) string {
	domain, ok := reg.Covers(host)
	if !ok {
		return fmt.Sprintf("http://%s.%s.localhost%s", host, r.Name, portSuffix(gatewayAddr()))
	}
	name := host
	if sharedHost(s, host) {
		name = gateway.WithRouter(host, r.Name, domain)
	}
	return "http://" + name + portSuffixFor80()
}

// sharedHost reports whether more than one router has the HTTP host.
func sharedHost(s vnet.Store, host string) bool {
	routers, err := s.List()
	if err != nil {
		return false
	}
	n := 0
	for _, r := range routers {
		tables, _ := r.Tables()
		for _, h := range tables.HTTP {
			if h.Host == host {
				n++
				break
			}
		}
	}
	return n > 1
}

// portSuffixFor80 is "" when the gateway serves :80, else its port.
func portSuffixFor80() string {
	if st := readGatewayState(); st.Port80 {
		return ""
	}
	return portSuffix(gatewayAddr())
}

// registrationStatus is the `sax net ls` footer about host names.
func registrationStatus(s vnet.Store) string {
	reg, err := s.Registration()
	if err != nil || len(reg.Domains) == 0 {
		return fmt.Sprintf("host names: http://<host>.<router>.localhost%s (run `sax net register` for http://<host>.%s)",
			portSuffix(gatewayAddr()), defaultDomain)
	}
	return fmt.Sprintf("host names: %s registered -> http://<host>%s, or http://<host>.<router>.%s when shared",
		dotted(reg.Domains), portSuffixFor80(), reg.Domains[0])
}
