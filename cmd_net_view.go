package main

import (
	"encoding/json"
	"errors"
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

// startView runs the router's apps as tabs of one session named after the
// router: it starts the session, or adds tabs for apps it does not have yet
// (running apps are left alone). It never touches a session of that name it
// did not create. Returns each app's status ("started" or "running").
func startView(s vnet.Store, run sessionRunner, r vnet.Router, tabs []server.TabSpec) (map[string]string, error) {
	status := make(map[string]string, len(tabs))
	if len(tabs) == 0 {
		return status, nil
	}
	name := r.Name
	if run.Alive(name) {
		if r.View != name {
			return nil, fmt.Errorf("a session named %q already exists and was not started by sax net up; "+
				"rename it or stop it first", name)
		}
		replaced := false
		for _, tab := range tabs {
			existed, err := run.AddTab(name, tab)
			if errors.Is(err, errAddTabUnsupported) {
				run.Kill(name) // an older view: replace it with app tabs
				replaced = true
				break
			}
			if err != nil {
				return nil, err
			}
			status[tab.Name] = "started"
			if existed {
				status[tab.Name] = "running"
			}
		}
		if !replaced {
			return status, nil
		}
	}

	if err := run.LaunchTabs(name, tabs); err != nil {
		return nil, fmt.Errorf("start %s: %w", name, err)
	}
	for _, tab := range tabs {
		status[tab.Name] = "started"
	}
	_, err := s.Update(r.Name, func(cur vnet.Router) (vnet.Router, error) { return cur.WithView(name), nil })
	return status, err
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
