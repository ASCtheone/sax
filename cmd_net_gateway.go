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
	"syscall"
	"time"

	"github.com/asc/sax/internal/vnet"
	"github.com/asc/sax/internal/vnet/gateway"
)

// gatewayStateFile records where the running gateway listens, so the CLI can
// print host URLs. It lives next to the routers; the underscore keeps it out
// of the router namespace.
const gatewayStateFile = "_gateway.json"

type gatewayState struct {
	PID  int    `json:"pid"`
	Addr string `json:"addr"`
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

	g := gateway.New(gateway.Config{Store: vnetStore(), HostAddr: addr, Logf: log.Printf})
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
	state, _ := json.Marshal(gatewayState{PID: os.Getpid(), Addr: g.HostAddr()})
	if err := os.WriteFile(gatewayStatePath(), state, 0o644); err != nil {
		log.Printf("gateway: write state: %v", err)
	}
	defer os.Remove(gatewayStatePath())
	fmt.Printf("sax vnet gateway: http://<host>.<router>.localhost%s\n", portSuffix(g.HostAddr()))
	return <-errc
}

func sleepBriefly() { time.Sleep(20 * time.Millisecond) }

func splitHostPort(addr string) (string, string, error) { return net.SplitHostPort(addr) }

// gatewayAddr returns the running gateway's host address, or the default.
func gatewayAddr() string {
	data, err := os.ReadFile(gatewayStatePath())
	if err != nil {
		return gateway.DefaultHostAddr
	}
	var st gatewayState
	if json.Unmarshal(data, &st) != nil || st.Addr == "" {
		return gateway.DefaultHostAddr
	}
	return st.Addr
}

// portSuffix is ":7480" for a host address, or "" for port 80.
func portSuffix(addr string) string {
	_, port, err := splitHostPort(addr)
	if err != nil || port == "80" {
		return ""
	}
	return ":" + port
}

// needsGateway reports whether any router has HTTP routes or publishes.
func needsGateway(s vnet.Store) bool {
	routers, err := s.List()
	if err != nil {
		return false
	}
	for _, r := range routers {
		tables, _ := r.Tables()
		if len(tables.HTTP) > 0 || len(r.Publish) > 0 {
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
	fmt.Fprintf(w, "started %s (http://<host>.<router>.localhost%s)\n", gateway.SessionName, portSuffix(gatewayAddr()))
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
func printHostURLs(w io.Writer, r vnet.Router) {
	tables, _ := r.Tables()
	pubs, _ := r.ParsedPublish()
	if len(tables.HTTP) == 0 && len(pubs) == 0 {
		return
	}
	fmt.Fprintln(w, "\nfrom the host:")
	suffix := portSuffix(gatewayAddr())
	for _, h := range tables.HTTP {
		fmt.Fprintf(w, "  http://%s.%s.localhost%s\n", h.Host, r.Name, suffix)
	}
	for _, p := range pubs {
		fmt.Fprintf(w, "  127.0.0.1:%d -> %s\n", p.HostPort, p.Target)
	}
}
