// Package hostdns plans the one-time, privileged change that sends lookups
// for registered domains (e.g. ".lan") to the sax gateway's DNS server, so
// browsers can open http://app1.lan. Everything else keeps working as before:
// only the registered domains are routed to sax.
//
//	macOS: /etc/resolver/<domain>   (nameserver 127.0.0.1 + port)
//	Linux: systemd-resolved drop-in with DNS=127.0.0.1:<port> Domains=~<domain>
package hostdns

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// DefaultDNSAddr is where the gateway answers host lookups for registered
// domains. It must be fixed: it is written into the system configuration.
const DefaultDNSAddr = "127.0.0.1:7453"

const (
	darwinResolverDir = "/etc/resolver"
	resolvedDropIn    = "/etc/systemd/resolved.conf.d/sax-vnet.conf"
	resolvedStub      = "/run/systemd/resolve/stub-resolv.conf"
	header            = "# Managed by sax (sax net unregister removes it).\n"
)

// ErrUnsupported means this system has no resolver sax knows how to extend.
var ErrUnsupported = errors.New("registering domains is supported on macOS and on Linux with systemd-resolved")

// Env describes the system being configured; tests fill it in.
type Env struct {
	GOOS         string
	ResolvedStub string // systemd-resolved marker file (default resolvedStub)
}

// File is a system file to create or replace.
type File struct {
	Path    string
	Content string
}

// Plan is the privileged change: files to write, files to remove, then
// commands that make the system pick the change up.
type Plan struct {
	Files  []File
	Remove []string
	After  [][]string
}

// RegisterPlan routes the given domains (the full registered set) to dnsAddr.
func RegisterPlan(env Env, domains []string, dnsAddr string) (Plan, error) {
	host, port, err := net.SplitHostPort(dnsAddr)
	if err != nil {
		return Plan{}, fmt.Errorf("dns address %q: %w", dnsAddr, err)
	}
	switch env.GOOS {
	case "darwin":
		var p Plan
		for _, d := range domains {
			p.Files = append(p.Files, File{
				Path:    filepath.Join(darwinResolverDir, d),
				Content: fmt.Sprintf("%snameserver %s\nport %s\n", header, host, port),
			})
		}
		p.After = darwinReload()
		return p, nil
	case "linux":
		if !hasResolved(env) {
			return Plan{}, ErrUnsupported
		}
		return Plan{
			Files: []File{{Path: resolvedDropIn, Content: resolvedConf(domains, dnsAddr)}},
			After: [][]string{{"systemctl", "restart", "systemd-resolved"}},
		}, nil
	}
	return Plan{}, ErrUnsupported
}

// UnregisterPlan stops routing the removed domains; remaining is the set
// that stays registered.
func UnregisterPlan(env Env, removed, remaining []string, dnsAddr string) (Plan, error) {
	switch env.GOOS {
	case "darwin":
		var p Plan
		for _, d := range removed {
			p.Remove = append(p.Remove, filepath.Join(darwinResolverDir, d))
		}
		p.After = darwinReload()
		return p, nil
	case "linux":
		p := Plan{After: [][]string{{"systemctl", "restart", "systemd-resolved"}}}
		if len(remaining) == 0 {
			p.Remove = []string{resolvedDropIn}
		} else {
			p.Files = []File{{Path: resolvedDropIn, Content: resolvedConf(remaining, dnsAddr)}}
		}
		return p, nil
	}
	return Plan{}, ErrUnsupported
}

func darwinReload() [][]string {
	return [][]string{{"killall", "-HUP", "mDNSResponder"}, {"dscacheutil", "-flushcache"}}
}

func resolvedConf(domains []string, dnsAddr string) string {
	routing := make([]string, len(domains))
	for i, d := range domains {
		routing[i] = "~" + d
	}
	return fmt.Sprintf("%s[Resolve]\nDNS=%s\nDomains=%s\n", header, dnsAddr, strings.Join(routing, " "))
}

func hasResolved(env Env) bool {
	stub := env.ResolvedStub
	if stub == "" {
		stub = resolvedStub
	}
	_, err := os.Stat(stub)
	return err == nil
}

// Runner runs one command with the given stdin.
type Runner func(args []string, stdin string) error

// Apply performs a plan through sudo. Reload commands are best effort: the
// configuration change itself already happened.
func Apply(p Plan, run Runner) error {
	dirs := map[string]bool{}
	for _, f := range p.Files {
		dir := filepath.Dir(f.Path)
		if !dirs[dir] {
			dirs[dir] = true
			if err := run([]string{"sudo", "mkdir", "-p", dir}, ""); err != nil {
				return fmt.Errorf("create %s: %w", dir, err)
			}
		}
		if err := run([]string{"sudo", "tee", f.Path}, f.Content); err != nil {
			return fmt.Errorf("write %s: %w", f.Path, err)
		}
	}
	for _, path := range p.Remove {
		if err := run([]string{"sudo", "rm", "-f", path}, ""); err != nil {
			return fmt.Errorf("remove %s: %w", path, err)
		}
	}
	for _, cmd := range p.After {
		_ = run(append([]string{"sudo"}, cmd...), "")
	}
	return nil
}
