package mcp

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

var (
	netProperty = map[string]interface{}{
		"type": "string",
		"description": "Optional vnet router to run the session in, as <router> or <router>:<host>. " +
			"The session gets its own virtual IP, so several versions of an app can use the same port.",
	}
	ipProperty = map[string]interface{}{
		"type":        "string",
		"description": "Optional fixed virtual IP inside the router (requires net)",
	}
)

// joinNet handles the optional net/ip tool parameters.
func joinNet(netJoin NetJoinFunc, name string, params map[string]interface{}) ([]string, error) {
	net, _ := params["net"].(string)
	ip, _ := params["ip"].(string)
	if net == "" {
		if ip != "" {
			return nil, fmt.Errorf("ip requires net")
		}
		return nil, nil
	}
	if netJoin == nil {
		return nil, fmt.Errorf("vnet is not available in this build")
	}
	env, err := netJoin(name, net, ip)
	if err != nil {
		return nil, fmt.Errorf("net: %w", err)
	}
	return env, nil
}

// netTool describes a sax_net_* tool that runs `sax net <verb> ...`.
type netTool struct {
	name, description string
	props             map[string]interface{}
	required          []string
	args              func(params map[string]interface{}) []string
}

func stringProp(desc string) map[string]interface{} {
	return map[string]interface{}{"type": "string", "description": desc}
}

func str(params map[string]interface{}, key string) string {
	v, _ := params[key].(string)
	return v
}

var netTools = []netTool{
	{
		name:        "sax_net_list",
		description: "List vnet routers (virtual networks for running app versions side by side).",
		props:       map[string]interface{}{},
		args:        func(map[string]interface{}) []string { return []string{"ls"} },
	},
	{
		name:        "sax_net_inspect",
		description: "Show a vnet router: sessions and their IPs, ordered rules, host URLs and published ports.",
		props:       map[string]interface{}{"router": stringProp("Router name")},
		required:    []string{"router"},
		args:        func(p map[string]interface{}) []string { return []string{"inspect", str(p, "router")} },
	},
	{
		name: "sax_net_restart",
		description: "Restart a vnet project's apps: stop each app's whole process tree, then start it " +
			"fresh from the env file (edits apply). Works from any folder.",
		props: map[string]interface{}{
			"project": stringProp("Project or router name (default: the current folder's project)"),
			"app":     stringProp("Only this app (default: every app of the project)"),
		},
		args: func(p map[string]interface{}) []string {
			args := []string{"restart"}
			if name := str(p, "project"); name != "" {
				args = append(args, name)
			}
			if app := str(p, "app"); app != "" {
				args = append(args, app)
			}
			return args
		},
	},
	{
		name: "sax_net_logs",
		description: "Recent output of a vnet project's apps, compose style (\"api | ...\"). " +
			"Works from any folder; for live output use sax_tail on <router>.<app>.",
		props: map[string]interface{}{
			"project": stringProp("Project or router name (default: the current folder's project)"),
			"app":     stringProp("Only this app (default: every app)"),
			"lines":   map[string]interface{}{"type": "integer", "description": "Lines per app (default 100)"},
		},
		args: func(p map[string]interface{}) []string {
			args := []string{"logs"}
			if name := str(p, "project"); name != "" {
				args = append(args, name)
			}
			if app := str(p, "app"); app != "" {
				args = append(args, app)
			}
			if n, ok := p["lines"].(float64); ok && n > 0 {
				args = append(args, "-n", strconv.Itoa(int(n)))
			}
			return append(args, "--no-color")
		},
	},
	{
		name: "sax_net_projects",
		description: "List the vnet projects sax_net_up can start: those of ./.sax.lan and of the " +
			"global ~/.sax.net (projects: map), with their router and whether they are up.",
		props: map[string]interface{}{},
		args:  func(map[string]interface{}) []string { return []string{"projects"} },
	},
	{
		name: "sax_net_up",
		description: "Start a vnet project: create/update its router, start its apps (tabs of session " +
			"<router>) and the HTTP gateway. Idempotent. Without project: ./.sax.lan, else the " +
			"~/.sax.net project covering the current folder.",
		props: map[string]interface{}{
			"file":    stringProp("Path to the env file (default ./.sax.lan, then ~/.sax.net)"),
			"project": stringProp("Project name from ./.sax.lan or ~/.sax.net (see sax_net_projects)"),
		},
		args: func(p map[string]interface{}) []string {
			args := []string{"up", "-d"}
			if f := str(p, "file"); f != "" {
				args = append(args, "-f", f)
			}
			if name := str(p, "project"); name != "" {
				args = append(args, name)
			}
			return args
		},
	},
	{
		name:        "sax_net_down",
		description: "Stop all sessions of a vnet router and release their addresses (rules are kept).",
		props:       map[string]interface{}{"router": stringProp("Router name, or a project name from ./.sax.lan or ~/.sax.net")},
		required:    []string{"router"},
		args:        func(p map[string]interface{}) []string { return []string{"down", str(p, "router")} },
	},
	{
		name: "sax_net_map",
		description: "Append a routing rule to a router (rules match in order, first match wins). " +
			"HTTP: pattern 'bob.lan/api/*' target 'api:3200'. TCP: pattern 'db.lan:5432' target 'db:5432'.",
		props: map[string]interface{}{
			"router":  stringProp("Router name"),
			"pattern": stringProp("Rule pattern, e.g. bob.lan/api/* or db.lan:5432"),
			"target":  stringProp("Target session:port, e.g. api:3200"),
		},
		required: []string{"router", "pattern", "target"},
		args: func(p map[string]interface{}) []string {
			return []string{"map", str(p, "router"), str(p, "pattern"), str(p, "target")}
		},
	},
}

func registerNet(s *Server) {
	for _, t := range netTools {
		t := t
		s.RegisterTool(
			ToolDef{
				Name:        t.name,
				Description: t.description,
				InputSchema: InputSchema{Type: "object", Properties: t.props, Required: t.required},
			},
			func(params map[string]interface{}) ToolResult {
				for _, key := range t.required {
					if str(params, key) == "" {
						return errorResult(key + " is required")
					}
				}
				return runSaxNet(t.args(params))
			},
		)
	}
}

// runSaxNet runs `sax net <args>` with this binary and returns its output.
func runSaxNet(args []string) ToolResult {
	exe, err := os.Executable()
	if err != nil {
		return errorResult(fmt.Sprintf("locate sax: %v", err))
	}
	out, err := exec.Command(exe, append([]string{"net"}, args...)...).CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		return errorResult(fmt.Sprintf("sax net %s failed: %v\n%s", strings.Join(args, " "), err, text))
	}
	return textResult(text)
}
