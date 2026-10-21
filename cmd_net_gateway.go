package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/asc/sax/internal/vnet"
	"github.com/asc/sax/internal/vnet/gateway"
	"github.com/asc/sax/internal/vnet/hostdns"
)

// gatewayStateFile records where the running gateway listens, so the CLI can
// print host URLs. It lives next to the routers; the underscore keeps it out
// of the router namespace.
const gatewayStateFile = "_gateway.json"

type gatewayState struct {
	PID    int    `json:"pid"`
	Addr   string `json:"addr"`
	Port80 bool   `json:"port80,omitempty"` // also serving :80 for registered domains
}

func gatewayStatePath() string { return filepath.Join(vnetRoot(), gatewayStateFile) }

// doNetGateway runs the gateway in the foreground (inside the vnet-gateway
// session). --port 80 binds every interface but only serves this machine.
func doNetGateway(args []string) error {
	addr := gateway.DefaultHostAddr
	if port := flagValue(args, "--port"); port != "" {
		addr = "127.0.0.1:" + port
		if port == "80" {
			addr = "0.0.0.0:80" // the only unprivileged way to get :80 on macOS
		}
	}
	if a := flagValue(args, "--addr"); a != "" {
		addr = a
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()

	var state gatewayState
	writeState := func() {
		data, _ := json.Marshal(state)
		if err := os.WriteFile(gatewayStatePath(), data, 0o644); err != nil {
			log.Printf("gateway: write state: %v", err)
		}
	}
	var stateMu sync.Mutex
	g := gateway.New(gateway.Config{
		Store: vnetStore(), HostAddr: addr, Logf: log.Printf,
		HostDNSAddr: hostdns.DefaultDNSAddr,
		OnPort80: func(on bool) {
			stateMu.Lock()
			defer stateMu.Unlock()
			state.Port80 = on
			if state.Addr != "" {
				writeState()
			}
		},
	})
	errc := make(chan error, 1)
	go func() { errc <- g.Run(ctx) }()

	for g.HostAddr() == "" {
		select {
		case err := <-errc:
			return err
		default:
		}
		if ctx.Err() != nil {
			return nil
		}
		sleepBriefly()
	}
	stateMu.Lock()
	state.PID, state.Addr = os.Getpid(), g.HostAddr()
	writeState()
	stateMu.Unlock()
	defer os.Remove(gatewayStatePath())
	fmt.Printf("sax vnet gateway: http://<host>.localhost%s\n", portSuffix(g.HostAddr()))
	return <-errc
}

func sleepBriefly() { time.Sleep(20 * time.Millisecond) }

func splitHostPort(addr string) (string, string, error) { return net.SplitHostPort(addr) }

// readGatewayState returns the running gateway's state (zero if none).
func readGatewayState() gatewayState {
	var st gatewayState
	if data, err := os.ReadFile(gatewayStatePath()); err == nil {
		_ = json.Unmarshal(data, &st)
	}
	return st
}

// gatewayAddr returns the running gateway's host address, or the default.
func gatewayAddr() string {
	if st := readGatewayState(); st.Addr != "" {
		return st.Addr
	}
	return gateway.DefaultHostAddr
}

// portSuffix is ":7480" for a host address, or "" for port 80.
func portSuffix(addr string) string {
	_, port, err := splitHostPort(addr)
	if err != nil || port == "80" {
		return ""
	}
	return ":" + port
}

// hostOS is runtime.GOOS; tests override it.
var hostOS = runtime.GOOS

// needsGateway reports whether any router has HTTP routes or publishes, or
// (on Linux) sessions, whose Go/static programs resolve names through the
// gateway's DNS stub, or whether a domain is registered (its lookups must be
// answered).
func needsGateway(s vnet.Store) bool {
	if reg, err := s.Registration(); err == nil && len(reg.Domains) > 0 {
		return true
	}
	routers, err := s.List()
	if err != nil {
		return false
	}
	for _, r := range routers {
		tables, _ := r.Tables()
		if len(tables.HTTP) > 0 || len(r.Publish) > 0 || (hostOS == "linux" && len(r.Sessions) > 0) {
			return true
		}
	}
	return false
}

// ensureGateway starts the vnet-gateway session when some router needs it.
func ensureGateway(w io.Writer, s vnet.Store, run sessionRunner) error {
	if !needsGateway(s) || run.Alive(gateway.SessionName) {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("start gateway: %w", err)
	}
	if err := run.Launch(gateway.SessionName, []string{exe, "net", "gateway"}, "", nil); err != nil {
		return fmt.Errorf("start gateway: %w", err)
	}
	fmt.Fprintf(w, "started %s (http://<host>.localhost%s)\n", gateway.SessionName, portSuffix(gatewayAddr()))
	return nil
}

func netPublish(w io.Writer, s vnet.Store, run sessionRunner, pos []string) error {
	if err := requireArgs(pos, 3, "publish <router> <host-port> <target>"); err != nil {
		return err
	}
	raw := pos[1] + " -> " + pos[len(pos)-1]
	r, err := s.Update(pos[0], func(r vnet.Router) (vnet.Router, error) { return r.WithPublish(raw) })
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "published 127.0.0.1:%s -> %s in %s\n", pos[1], pos[len(pos)-1], r.Name)
	return ensureGateway(w, s, run)
}

func netUnpublish(w io.Writer, s vnet.Store, pos []string) error {
	if err := requireArgs(pos, 2, "unpublish <router> <host-port>"); err != nil {
		return err
	}
	if _, err := s.Update(pos[0], func(r vnet.Router) (vnet.Router, error) { return r.WithoutPublish(pos[1]) }); err != nil {
		return err
	}
	fmt.Fprintf(w, "unpublished %s from %s\n", pos[1], pos[0])
	return nil
}

// printHostURLs lists how to reach a router's HTTP hosts and publishes from
// the host machine.
func printHostURLs(w io.Writer, s vnet.Store, r vnet.Router) {
	tables, _ := r.Tables()
	pubs, _ := r.ParsedPublish()
	if len(tables.HTTP) == 0 && len(pubs) == 0 {
		return
	}
	fmt.Fprintln(w, "\nfrom the host:")
	reg, _ := s.Registration()
	for _, h := range tables.HTTP {
		fmt.Fprintf(w, "  %s\n", hostURL(reg, h.Host))
	}
	for _, p := range pubs {
		fmt.Fprintf(w, "  127.0.0.1:%d -> %s\n", p.HostPort, p.Target)
	}
}
