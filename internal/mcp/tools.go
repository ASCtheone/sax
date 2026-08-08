package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/asc/sax/internal/cuc"
	"github.com/asc/sax/internal/ipc"
	"github.com/asc/sax/internal/nx"
)

// RegisterAllTools registers all SAX MCP tools on the server.
func RegisterAllTools(s *Server, launchDaemon func(name string, command []string, workDir string) error, waitForSession func(name string) bool) {
	registerListSessions(s)
	registerCreateSession(s, launchDaemon, waitForSession)
	registerKillSession(s)
	registerKillAll(s)
	registerTail(s)
	registerSend(s)
	registerStatus(s)
	registerExec(s, launchDaemon, waitForSession)
	registerLaunch(s)
	registerNxList(s)
	registerNxServe(s, launchDaemon, waitForSession)
	registerNxStop(s)
	registerCucUp(s)
	registerCucClaude(s, launchDaemon, waitForSession)
	registerCucDown(s)
	registerCucStatus(s)
}

func registerListSessions(s *Server) {
	s.RegisterTool(
		ToolDef{
			Name:        "sax_list_sessions",
			Description: "List all active SAX terminal sessions with their status, attached clients, ports, and creation time.",
			InputSchema: InputSchema{Type: "object"},
		},
		func(params map[string]interface{}) ToolResult {
			sessions, err := ipc.ListSessions()
			if err != nil {
				return errorResult(fmt.Sprintf("failed to list sessions: %v", err))
			}

			type sessionEntry struct {
				Name      string      `json:"name"`
				Status    string      `json:"status"`
				Clients   int         `json:"clients"`
				Pid       int         `json:"pid,omitempty"`
				CreatedAt string      `json:"created_at"`
				Ports     interface{} `json:"ports,omitempty"`
			}

			var entries []sessionEntry
			for _, sess := range sessions {
				if !ipc.IsSessionAlive(sess.Name) {
					ipc.CleanupSocket(sess.Name)
					os.Remove(ipc.PidPath(sess.Name))
					continue
				}

				entry := sessionEntry{
					Name:      sess.Name,
					Status:    "detached",
					CreatedAt: sess.CreatedAt.Format("2006-01-02T15:04:05Z"),
				}

				info := ipc.QuerySession(sess.Name)
				if info != nil {
					if clients, ok := info["clients"].(float64); ok && clients > 0 {
						entry.Status = "attached"
						entry.Clients = int(clients)
					}
					if pid, ok := info["created_pid"].(float64); ok {
						entry.Pid = int(pid)
					}
					if ports, ok := info["ports"]; ok {
						entry.Ports = ports
					}
				}

				entries = append(entries, entry)
			}

			// Include launched (GUI) apps
			launchDir := launchedAppsDir()
			if dirEntries, err := os.ReadDir(launchDir); err == nil {
				for _, de := range dirEntries {
					if !strings.HasSuffix(de.Name(), ".pid") {
						continue
					}
					appName := strings.TrimSuffix(de.Name(), ".pid")
					pid, alive := isLaunchedAlive(appName)
					if !alive {
						cleanLaunched(appName)
						continue
					}
					cmdBytes, _ := os.ReadFile(launchedCmdPath(appName))
					entry := sessionEntry{
						Name:   appName,
						Status: "launched",
						Pid:    pid,
					}
					if len(cmdBytes) > 0 {
						entry.CreatedAt = strings.TrimSpace(string(cmdBytes))
					}
					entries = append(entries, entry)
				}
			}

			if len(entries) == 0 {
				return textResult("No active sessions.")
			}

			data, _ := json.MarshalIndent(entries, "", "  ")
			return textResult(string(data))
		},
	)
}

func registerCreateSession(s *Server, launchDaemon func(string, []string, string) error, waitForSession func(string) bool) {
	s.RegisterTool(
		ToolDef{
			Name:        "sax_create_session",
			Description: "Create a new SAX terminal session. The session runs in the background as a daemon.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Session name (alphanumeric, hyphens, underscores)",
					},
				},
				Required: []string{"name"},
			},
		},
		func(params map[string]interface{}) ToolResult {
			name, _ := params["name"].(string)
			if name == "" {
				return errorResult("name is required")
			}
			if err := ipc.ValidateSessionName(name); err != nil {
				return errorResult(fmt.Sprintf("invalid session name: %v", err))
			}

			if ipc.IsSessionAlive(name) {
				return textResult(fmt.Sprintf("Session %q already exists.", name))
			}

			if err := launchDaemon(name, nil, ""); err != nil {
				return errorResult(fmt.Sprintf("failed to start daemon: %v", err))
			}
			if !waitForSession(name) {
				return errorResult(fmt.Sprintf("timed out waiting for session %q", name))
			}

			return textResult(fmt.Sprintf("Session %q created.", name))
		},
	)
}

func registerKillSession(s *Server) {
	s.RegisterTool(
		ToolDef{
			Name:        "sax_kill_session",
			Description: "Kill (terminate) a running SAX session by name.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Session name to kill",
					},
				},
				Required: []string{"name"},
			},
		},
		func(params map[string]interface{}) ToolResult {
			name, _ := params["name"].(string)
			if name == "" {
				return errorResult("name is required")
			}

			// Check if it's a launched (GUI) app first
			if pid, alive := isLaunchedAlive(name); alive {
				if proc, err := os.FindProcess(pid); err == nil {
					_ = proc.Kill()
				}
				cleanLaunched(name)
				return textResult(fmt.Sprintf("Killed launched app %q (pid %d).", name, pid))
			}
			// Clean stale launched metadata if any
			cleanLaunched(name)

			if !ipc.IsSessionAlive(name) {
				ipc.CleanupSocket(name)
				os.Remove(ipc.PidPath(name))
				return errorResult(fmt.Sprintf("session %q not found or not running", name))
			}

			pidData, err := os.ReadFile(ipc.PidPath(name))
			if err != nil {
				return errorResult(fmt.Sprintf("cannot read PID for session %q: %v", name, err))
			}

			pidStr := strings.TrimSpace(string(pidData))
			var pid int
			fmt.Sscanf(pidStr, "%d", &pid)
			if pid > 0 {
				if proc, err := os.FindProcess(pid); err == nil {
					_ = proc.Signal(os.Interrupt)
				}
			}

			ipc.CleanupSocket(name)
			os.Remove(ipc.PidPath(name))
			return textResult(fmt.Sprintf("Killed session %q (pid %d).", name, pid))
		},
	)
}

func registerKillAll(s *Server) {
	s.RegisterTool(
		ToolDef{
			Name:        "sax_kill_all",
			Description: "Kill all running SAX sessions.",
			InputSchema: InputSchema{Type: "object"},
		},
		func(params map[string]interface{}) ToolResult {
			sessions, err := ipc.ListSessions()
			if err != nil {
				return errorResult(fmt.Sprintf("failed to list sessions: %v", err))
			}

			killed := 0
			for _, sess := range sessions {
				if !ipc.IsSessionAlive(sess.Name) {
					ipc.CleanupSocket(sess.Name)
					os.Remove(ipc.PidPath(sess.Name))
					continue
				}

				pidData, err := os.ReadFile(ipc.PidPath(sess.Name))
				if err != nil {
					continue
				}
				pidStr := strings.TrimSpace(string(pidData))
				var pid int
				fmt.Sscanf(pidStr, "%d", &pid)
				if pid > 0 {
					if proc, err := os.FindProcess(pid); err == nil {
						_ = proc.Signal(os.Interrupt)
					}
				}
				ipc.CleanupSocket(sess.Name)
				os.Remove(ipc.PidPath(sess.Name))
				killed++
			}

			if killed == 0 {
				return textResult("No active sessions to kill.")
			}
			return textResult(fmt.Sprintf("Killed %d session(s).", killed))
		},
	)
}

func registerTail(s *Server) {
	s.RegisterTool(
		ToolDef{
			Name:        "sax_tail",
			Description: "Get the last N lines of output from a session's active pane. Useful for checking command output, logs, and build status.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Session name",
					},
					"lines": map[string]interface{}{
						"type":        "number",
						"description": "Number of lines to retrieve (default: 10)",
					},
				},
				Required: []string{"name"},
			},
		},
		func(params map[string]interface{}) ToolResult {
			name, _ := params["name"].(string)
			if name == "" {
				return errorResult("name is required")
			}

			n := 10
			if lines, ok := params["lines"].(float64); ok && lines > 0 {
				n = int(lines)
			}

			if !ipc.IsSessionAlive(name) {
				return errorResult(fmt.Sprintf("session %q not found", name))
			}

			reply := ipc.QuerySessionTyped(name, "tail", fmt.Sprintf("%d", n))
			if reply == nil {
				return errorResult(fmt.Sprintf("failed to query session %q", name))
			}

			tail, _ := reply["tail"].(string)
			if tail == "" {
				return textResult("(empty)")
			}
			return textResult(tail)
		},
	)
}

func registerSend(s *Server) {
	s.RegisterTool(
		ToolDef{
			Name:        "sax_send",
			Description: "Send text or keystrokes to a session's active pane. Use \\n for Enter. Useful for running commands in a terminal session.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Session name",
					},
					"text": map[string]interface{}{
						"type":        "string",
						"description": "Text to send (use \\n for Enter)",
					},
				},
				Required: []string{"name", "text"},
			},
		},
		func(params map[string]interface{}) ToolResult {
			name, _ := params["name"].(string)
			text, _ := params["text"].(string)
			if name == "" || text == "" {
				return errorResult("name and text are required")
			}

			if !ipc.IsSessionAlive(name) {
				return errorResult(fmt.Sprintf("session %q not found", name))
			}

			// Interpret \n as actual newlines
			text = strings.ReplaceAll(text, `\n`, "\n")

			reply := ipc.QuerySessionTyped(name, "send", text)
			if reply == nil {
				return errorResult(fmt.Sprintf("failed to send to session %q", name))
			}

			if ok, _ := reply["ok"].(bool); ok {
				return textResult("sent")
			}
			return errorResult("send failed")
		},
	)
}

func registerStatus(s *Server) {
	s.RegisterTool(
		ToolDef{
			Name:        "sax_status",
			Description: "Get detailed status of a session as JSON, including tabs, panes, dimensions, clients, and ports.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Session name",
					},
				},
				Required: []string{"name"},
			},
		},
		func(params map[string]interface{}) ToolResult {
			name, _ := params["name"].(string)
			if name == "" {
				return errorResult("name is required")
			}

			// Check launched apps first
			if pid, alive := isLaunchedAlive(name); alive {
				cmdBytes, _ := os.ReadFile(launchedCmdPath(name))
				status := map[string]interface{}{
					"name":    name,
					"type":    "launched",
					"alive":   true,
					"pid":     pid,
					"command": strings.TrimSpace(string(cmdBytes)),
				}
				data, _ := json.MarshalIndent(status, "", "  ")
				return textResult(string(data))
			}

			if !ipc.IsSessionAlive(name) {
				return errorResult(fmt.Sprintf("session %q not found", name))
			}

			reply := ipc.QuerySessionTyped(name, "status", "")
			if reply == nil {
				return errorResult(fmt.Sprintf("failed to query session %q", name))
			}

			data, err := json.MarshalIndent(reply, "", "  ")
			if err != nil {
				return errorResult(fmt.Sprintf("failed to marshal status: %v", err))
			}
			return textResult(string(data))
		},
	)
}

func registerExec(s *Server, launchDaemon func(string, []string, string) error, waitForSession func(string) bool) {
	s.RegisterTool(
		ToolDef{
			Name:        "sax_exec",
			Description: "Create a new session running a specific command. The session persists in the background.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Session name",
					},
					"command": map[string]interface{}{
						"type":        "string",
						"description": "Command to run (e.g. 'npm run dev', 'python server.py')",
					},
				},
				Required: []string{"name", "command"},
			},
		},
		func(params map[string]interface{}) ToolResult {
			name, _ := params["name"].(string)
			command, _ := params["command"].(string)
			if name == "" || command == "" {
				return errorResult("name and command are required")
			}
			if err := ipc.ValidateSessionName(name); err != nil {
				return errorResult(fmt.Sprintf("invalid session name: %v", err))
			}

			if ipc.IsSessionAlive(name) {
				return errorResult(fmt.Sprintf("session %q already exists", name))
			}

			// Split command into parts
			parts := strings.Fields(command)
			if len(parts) == 0 {
				return errorResult("command is empty")
			}

			if err := launchDaemon(name, parts, ""); err != nil {
				return errorResult(fmt.Sprintf("failed to start daemon: %v", err))
			}
			if !waitForSession(name) {
				return errorResult(fmt.Sprintf("timed out waiting for session %q", name))
			}

			return textResult(fmt.Sprintf("Session %q created running %q.", name, command))
		},
	)
}

func registerNxList(s *Server) {
	s.RegisterTool(
		ToolDef{
			Name:        "sax_nx_list",
			Description: "List NX monorepo workspace projects that have serve/dev/start targets. Shows which are currently running as SAX sessions.",
			InputSchema: InputSchema{Type: "object"},
		},
		func(params map[string]interface{}) ToolResult {
			ws, err := nx.Discover("")
			if err != nil {
				return errorResult(fmt.Sprintf("NX discovery failed: %v", err))
			}

			projects := ws.ServeTargets()
			if len(projects) == 0 {
				return textResult(fmt.Sprintf("NX workspace at %s — no serve targets found (%d total projects).", ws.Root, len(ws.Projects)))
			}

			type projectEntry struct {
				Name    string   `json:"name"`
				Targets []string `json:"targets"`
				Running bool     `json:"running"`
			}

			var entries []projectEntry
			for _, p := range projects {
				var targets []string
				for _, t := range p.Targets {
					targets = append(targets, t.Name)
				}
				entries = append(entries, projectEntry{
					Name:    p.Name,
					Targets: targets,
					Running: ipc.IsSessionAlive(p.Name),
				})
			}

			data, _ := json.MarshalIndent(map[string]interface{}{
				"root":     ws.Root,
				"projects": entries,
			}, "", "  ")
			return textResult(string(data))
		},
	)
}

func registerNxServe(s *Server, launchDaemon func(string, []string, string) error, waitForSession func(string) bool) {
	s.RegisterTool(
		ToolDef{
			Name:        "sax_nx_serve",
			Description: "Start an NX workspace project's serve target as a background SAX session.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"app": map[string]interface{}{
						"type":        "string",
						"description": "NX project name to serve",
					},
				},
				Required: []string{"app"},
			},
		},
		func(params map[string]interface{}) ToolResult {
			app, _ := params["app"].(string)
			if app == "" {
				return errorResult("app is required")
			}

			ws, err := nx.Discover("")
			if err != nil {
				return errorResult(fmt.Sprintf("NX discovery failed: %v", err))
			}

			projects := ws.ServeTargets()

			// Exact match first
			var matched *nx.Project
			for _, p := range projects {
				if p.Name == app {
					matched = &p
					break
				}
			}
			// Partial match
			if matched == nil {
				for _, p := range projects {
					if strings.Contains(p.Name, app) {
						matched = &p
						break
					}
				}
			}
			if matched == nil {
				return errorResult(fmt.Sprintf("no NX project matching %q with a serve target", app))
			}

			sessionName := matched.Name
			if ipc.IsSessionAlive(sessionName) {
				return textResult(fmt.Sprintf("%s is already running.", sessionName))
			}

			// Find serve target
			targetName := "serve"
			for _, t := range matched.Targets {
				targetName = t.Name
				break
			}

			cmd := nx.NxCommand(matched.Name, targetName)
			if err := launchDaemon(sessionName, cmd, ""); err != nil {
				return errorResult(fmt.Sprintf("failed to start %s: %v", sessionName, err))
			}
			if !waitForSession(sessionName) {
				return errorResult(fmt.Sprintf("timed out starting %s", sessionName))
			}

			return textResult(fmt.Sprintf("Started %s (%s:%s).", sessionName, matched.Name, targetName))
		},
	)
}

func registerNxStop(s *Server) {
	s.RegisterTool(
		ToolDef{
			Name:        "sax_nx_stop",
			Description: "Stop a running NX project session, or all NX sessions if no app specified.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"app": map[string]interface{}{
						"type":        "string",
						"description": "NX project name to stop (omit to stop all)",
					},
				},
			},
		},
		func(params map[string]interface{}) ToolResult {
			app, _ := params["app"].(string)

			if app != "" {
				if !ipc.IsSessionAlive(app) {
					return errorResult(fmt.Sprintf("session %q is not running", app))
				}

				pidData, err := os.ReadFile(ipc.PidPath(app))
				if err != nil {
					return errorResult(fmt.Sprintf("cannot read PID for %q: %v", app, err))
				}
				pidStr := strings.TrimSpace(string(pidData))
				var pid int
				fmt.Sscanf(pidStr, "%d", &pid)
				if pid > 0 {
					if proc, err := os.FindProcess(pid); err == nil {
						_ = proc.Signal(os.Interrupt)
					}
				}
				ipc.CleanupSocket(app)
				os.Remove(ipc.PidPath(app))
				return textResult(fmt.Sprintf("Stopped %s.", app))
			}

			// Stop all NX sessions
			ws, err := nx.Discover("")
			if err != nil {
				return errorResult(fmt.Sprintf("NX discovery failed: %v", err))
			}

			projects := ws.ServeTargets()
			killed := 0
			for _, p := range projects {
				if !ipc.IsSessionAlive(p.Name) {
					continue
				}
				pidData, err := os.ReadFile(ipc.PidPath(p.Name))
				if err != nil {
					continue
				}
				pidStr := strings.TrimSpace(string(pidData))
				var pid int
				fmt.Sscanf(pidStr, "%d", &pid)
				if pid > 0 {
					if proc, err := os.FindProcess(pid); err == nil {
						_ = proc.Signal(os.Interrupt)
					}
				}
				ipc.CleanupSocket(p.Name)
				os.Remove(ipc.PidPath(p.Name))
				killed++
			}

			if killed == 0 {
				return textResult("No NX sessions running.")
			}
			return textResult(fmt.Sprintf("Stopped %d NX session(s).", killed))
		},
	)
}

// launchedAppsDir returns the directory for launched app metadata.
func launchedAppsDir() string {
	return filepath.Join(ipc.SessionsDir(), "launched")
}

// launchedPidPath returns the PID file path for a launched app.
func launchedPidPath(name string) string {
	return filepath.Join(launchedAppsDir(), name+".pid")
}

// launchedCmdPath returns the command file path for a launched app.
func launchedCmdPath(name string) string {
	return filepath.Join(launchedAppsDir(), name+".cmd")
}

// isLaunchedAlive checks if a launched app is still running by PID.
func isLaunchedAlive(name string) (int, bool) {
	data, err := os.ReadFile(launchedPidPath(name))
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, false
	}
	return pid, isProcessAlive(pid)
}

// cleanLaunched removes metadata for a launched app.
func cleanLaunched(name string) {
	os.Remove(launchedPidPath(name))
	os.Remove(launchedCmdPath(name))
}

func registerLaunch(s *Server) {
	s.RegisterTool(
		ToolDef{
			Name:        "sax_launch",
			Description: "Launch a GUI/desktop application without a PTY. If already running, reports existing status. Use this for apps that cannot run inside a terminal (e.g. WebView2, Electron, native GUI apps).",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "App name for tracking (e.g. 'cmddo', 'myapp')",
					},
					"command": map[string]interface{}{
						"type":        "string",
						"description": "Full path to the executable to launch",
					},
				},
				Required: []string{"name", "command"},
			},
		},
		func(params map[string]interface{}) ToolResult {
			name, _ := params["name"].(string)
			command, _ := params["command"].(string)
			if name == "" || command == "" {
				return errorResult("name and command are required")
			}

			// Check if already running
			if pid, alive := isLaunchedAlive(name); alive {
				return textResult(fmt.Sprintf("%s is already running (PID %d).", name, pid))
			}

			// Clean stale metadata
			cleanLaunched(name)

			// Ensure directory exists
			os.MkdirAll(launchedAppsDir(), 0700)

			// Split command
			parts := strings.Fields(command)
			if len(parts) == 0 {
				return errorResult("command is empty")
			}

			// Launch as detached process (no PTY, new process group)
			cmd := exec.Command(parts[0], parts[1:]...)
			cmd.Stdout = nil
			cmd.Stderr = nil
			cmd.Stdin = nil
			setLaunchProcAttr(cmd)
			if err := cmd.Start(); err != nil {
				return errorResult(fmt.Sprintf("failed to launch: %v", err))
			}

			pid := cmd.Process.Pid

			// Write PID and command for tracking
			os.WriteFile(launchedPidPath(name), []byte(strconv.Itoa(pid)), 0600)
			os.WriteFile(launchedCmdPath(name), []byte(command), 0600)

			// Don't wait — let the process run independently
			go cmd.Wait()

			return textResult(fmt.Sprintf("Launched %s (PID %d).", name, pid))
		},
	)
}

// --- cuc dev container ---

// runCapture runs a command in dir ("" = current dir) and returns combined output.
func runCapture(command []string, dir string) (string, error) {
	c := exec.Command(command[0], command[1:]...)
	c.Dir = dir
	out, err := c.CombinedOutput()
	return string(out), err
}

func registerCucUp(s *Server) {
	s.RegisterTool(
		ToolDef{
			Name:        "sax_cuc_up",
			Description: "Build and start the cuc (Claude Unleashed Container) dev container. Uses docker compose when a compose file is found nearby, otherwise pulls and runs the published image. May take minutes on first build.",
			InputSchema: InputSchema{Type: "object"},
		},
		func(params map[string]interface{}) ToolResult {
			if !cuc.DockerAvailable() {
				return errorResult("docker not found on PATH")
			}

			var log strings.Builder
			if root := cuc.ComposeRoot(""); root != "" {
				out, err := runCapture(cuc.ComposeUp(), root)
				log.WriteString(out)
				if err != nil {
					return errorResult(fmt.Sprintf("compose up failed: %v\n%s", err, tailLines(log.String(), 20)))
				}
			} else {
				out, err := runCapture(cuc.PullImage(cuc.DefaultImage), "")
				log.WriteString(out)
				if err != nil {
					return errorResult(fmt.Sprintf("pull failed: %v\n%s", err, tailLines(log.String(), 20)))
				}
				if cuc.Exists(cuc.DefaultContainer) {
					_, _ = runCapture(cuc.RemoveContainer(cuc.DefaultContainer), "")
				}
				wd, _ := os.Getwd()
				home, _ := os.UserHomeDir()
				out, err = runCapture(cuc.RunImage(cuc.DefaultContainer, cuc.DefaultImage, wd, home), "")
				log.WriteString(out)
				if err != nil {
					return errorResult(fmt.Sprintf("docker run failed: %v\n%s", err, tailLines(log.String(), 20)))
				}
			}

			// Container was (re)created — clear any stale Claude session so it
			// won't be reattached to a dead container.
			killSessionByName(cuc.SessionName)

			// Make the persistent volume writable, persist config location, seed.
			_, _ = runCapture(cuc.ChownHome(cuc.DefaultContainer), "")
			_, _ = runCapture(cuc.EnsureConfigSymlink(cuc.DefaultContainer), "")
			seedOut, _ := runCapture(cuc.Seed(cuc.DefaultContainer), "")

			// Transpose the host login into the container (best-effort).
			auth := "host login synced"
			if err := cuc.SetupAuth(cuc.DefaultContainer); err != nil {
				auth = "login sync skipped: " + err.Error()
			}

			return textResult(fmt.Sprintf("Container %q ready (%s). Launch Claude with sax_cuc_claude.\n%s", cuc.DefaultContainer, auth, tailLines(seedOut, 8)))
		},
	)
}

func registerCucClaude(s *Server, launchDaemon func(string, []string, string) error, waitForSession func(string) bool) {
	s.RegisterTool(
		ToolDef{
			Name:        "sax_cuc_claude",
			Description: "Launch a Claude Code instance INSIDE the cuc dev container as a background SAX session (named cuc-claude). Drive it afterward with sax_tail / sax_send. Requires the container to be running (sax_cuc_up first).",
			InputSchema: InputSchema{Type: "object"},
		},
		func(params map[string]interface{}) ToolResult {
			if !cuc.Running(cuc.DefaultContainer) {
				return errorResult(fmt.Sprintf("container %q is not running — call sax_cuc_up first", cuc.DefaultContainer))
			}

			name := cuc.SessionName
			if ipc.IsSessionAlive(name) {
				return textResult(fmt.Sprintf("%s is already running.", name))
			}

			if err := launchDaemon(name, cuc.Claude(cuc.DefaultContainer), ""); err != nil {
				return errorResult(fmt.Sprintf("failed to start %s: %v", name, err))
			}
			if !waitForSession(name) {
				return errorResult(fmt.Sprintf("timed out starting %s", name))
			}
			return textResult(fmt.Sprintf("Started Claude in %q as session %q. Use sax_tail/sax_send to drive it.", cuc.DefaultContainer, name))
		},
	)
}

func registerCucDown(s *Server) {
	s.RegisterTool(
		ToolDef{
			Name:        "sax_cuc_down",
			Description: "Stop the cuc dev container and its Claude session (cuc-claude).",
			InputSchema: InputSchema{Type: "object"},
		},
		func(params map[string]interface{}) ToolResult {
			if !cuc.DockerAvailable() {
				return errorResult("docker not found on PATH")
			}

			// Kill the Claude session if alive.
			killSessionByName(cuc.SessionName)

			if root := cuc.ComposeRoot(""); root != "" {
				if out, err := runCapture(cuc.ComposeDown(), root); err != nil {
					return errorResult(fmt.Sprintf("compose down failed: %v\n%s", err, tailLines(out, 20)))
				}
			} else if cuc.Exists(cuc.DefaultContainer) {
				if out, err := runCapture(cuc.RemoveContainer(cuc.DefaultContainer), ""); err != nil {
					return errorResult(fmt.Sprintf("remove failed: %v\n%s", err, tailLines(out, 20)))
				}
			}
			return textResult("Stopped cuc container and session.")
		},
	)
}

func registerCucStatus(s *Server) {
	s.RegisterTool(
		ToolDef{
			Name:        "sax_cuc_status",
			Description: "Report cuc dev-container and Claude-session state as JSON (container running? session alive? docker present?).",
			InputSchema: InputSchema{Type: "object"},
		},
		func(params map[string]interface{}) ToolResult {
			status := map[string]interface{}{
				"container":         cuc.DefaultContainer,
				"image":             cuc.DefaultImage,
				"docker":            cuc.DockerAvailable(),
				"container_running": cuc.Running(cuc.DefaultContainer),
				"session":           cuc.SessionName,
				"session_alive":     ipc.IsSessionAlive(cuc.SessionName),
			}
			if root := cuc.ComposeRoot(""); root != "" {
				status["compose_root"] = root
			}
			b, _ := json.MarshalIndent(status, "", "  ")
			return textResult(string(b))
		},
	)
}

// killSessionByName terminates a SAX session if it is alive, ignoring errors.
func killSessionByName(name string) {
	if ipc.IsSessionAlive(name) {
		if pidData, err := os.ReadFile(ipc.PidPath(name)); err == nil {
			pid := 0
			fmt.Sscanf(strings.TrimSpace(string(pidData)), "%d", &pid)
			if pid > 0 {
				if proc, err := os.FindProcess(pid); err == nil {
					_ = proc.Signal(os.Interrupt)
				}
			}
		}
	}
	ipc.CleanupSocket(name)
	os.Remove(ipc.PidPath(name))
}

// tailLines returns the last n non-empty-trimmed lines of s.
func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
