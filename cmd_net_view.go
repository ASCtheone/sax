package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/asc/sax/internal/server"
	"github.com/asc/sax/internal/vnet"
	"golang.org/x/term"
)

// envInitTabs hands a daemon the tabs to start with (JSON []server.TabSpec);
// the daemon removes it before panes inherit their environment.
const envInitTabs = "SAX_INIT_TABS"

// initTabsFromEnv reads and clears the tab list a daemon was started with.
func initTabsFromEnv() []server.TabSpec {
	raw := os.Getenv(envInitTabs)
	os.Unsetenv(envInitTabs)
	if raw == "" {
		return nil
	}
	var tabs []server.TabSpec
	if err := json.Unmarshal([]byte(raw), &tabs); err != nil {
		return nil
	}
	return tabs
}

func initTabsEnv(tabs []server.TabSpec) (string, error) {
	data, err := json.Marshal(tabs)
	if err != nil {
		return "", fmt.Errorf("encode tabs: %w", err)
	}
	return envInitTabs + "=" + string(data), nil
}

// wantsAttach reports whether `sax net up` should open the view: not with
// -d/--detach, and only when there is a terminal to attach (MCP, scripts).
func wantsAttach(args []string) bool {
	if containsFlag(args, "-d") || containsFlag(args, "--detach") {
		return false
	}
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

// viewTabs builds one tab per running session of the router; each tab is a
// sax client attached to that session (sax in sax).
func viewTabs(r vnet.Router, run sessionRunner, exe string) []server.TabSpec {
	var tabs []server.TabSpec
	for _, host := range r.SortedSessions() {
		owner, ok := r.Owners[host]
		if !ok || !run.Alive(owner) {
			continue
		}
		tabs = append(tabs, server.TabSpec{Name: host, Cmd: []string{exe, "-a", owner}})
	}
	return tabs
}

// startView (re)creates the router's view session: a session named after
// the router with one tab per app. It never replaces a session it did not
// create.
func startView(w io.Writer, s vnet.Store, run sessionRunner, routerName string) (string, error) {
	r, err := s.Get(routerName)
	if err != nil {
		return "", err
	}
	name := r.Name
	if run.Alive(name) {
		if r.View != name {
			return "", fmt.Errorf("a session named %q already exists and is not this router's view; "+
				"rename it or use -d", name)
		}
		run.Kill(name) // rebuild so the tabs match the current apps
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("start view: %w", err)
	}
	tabs := viewTabs(r, run, exe)
	if len(tabs) == 0 {
		return "", nil
	}
	if err := run.LaunchTabs(name, tabs); err != nil {
		return "", fmt.Errorf("start view: %w", err)
	}
	if _, err := s.Update(r.Name, func(cur vnet.Router) (vnet.Router, error) {
		next := cur.WithView(name)
		return next, nil
	}); err != nil {
		return "", err
	}
	fmt.Fprintf(w, "view: sax -a %s (one tab per app; Ctrl+S d to detach)\n", name)
	return name, nil
}

// stopView closes the router's view session, if sax created one.
func stopView(w io.Writer, s vnet.Store, run sessionRunner, r vnet.Router) error {
	if r.View == "" {
		return nil
	}
	if run.Alive(r.View) {
		run.Kill(r.View)
		fmt.Fprintf(w, "stopped view %s\n", r.View)
	}
	_, err := s.Update(r.Name, func(cur vnet.Router) (vnet.Router, error) { return cur.WithView(""), nil })
	return err
}
