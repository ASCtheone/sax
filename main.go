package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/asc/sax/internal/client"
	"github.com/asc/sax/internal/completion"
	"github.com/asc/sax/internal/config"
	"github.com/asc/sax/internal/cuc"
	"github.com/asc/sax/internal/ipc"
	"github.com/asc/sax/internal/mcp"
	"github.com/asc/sax/internal/nx"
	"github.com/asc/sax/internal/pty"
	"github.com/asc/sax/internal/saxrc"
	"github.com/asc/sax/internal/scrollback"
	"github.com/asc/sax/internal/server"
	"github.com/asc/sax/internal/theme"
	"github.com/asc/sax/internal/updater"

	tea "github.com/charmbracelet/bubbletea"
)

// Set via ldflags at build time.
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	args := os.Args[1:]

	// Version flag (earliest check)
	if len(args) > 0 && (args[0] == "--version" || args[0] == "-v") {
		fmt.Printf("sax %s (commit %s, built %s)\n", version, commit, date)
		return
	}

	// Self-update command
	if len(args) > 0 && args[0] == "update" {
		force := len(args) > 1 && args[1] == "--force"
		doSelfUpdate(force)
		return
	}

	// MCP server mode
	if len(args) > 0 && args[0] == "mcp" {
		doMCP()
		return
	}

	// Theme listing command
	if len(args) > 0 && (args[0] == "themes" || args[0] == "theme") {
		doListThemes(args[1:])
		return
	}

	// Completion machinery. These run on every Tab press, so they are handled
	// before the background update check to stay fast and produce no stray
	// stderr output.
	if len(args) >= 2 && args[0] == "nx" && args[1] == "__complete" {
		doNxComplete(args[2:])
		return
	}
	if len(args) > 0 && args[0] == "__complete" {
		doComplete(args[1:])
		return
	}
	if len(args) > 0 && args[0] == "completion" {
		doCompletion(args[1:])
		return
	}

	// Setup commands (e.g. shell completion profile wiring).
	if len(args) > 0 && args[0] == "setup" {
		doSetup(args[1:])
		return
	}

	// Internal server mode: sax --server -S <name> [--dir <path>] [--cmd <command...>]
	if containsFlag(args, "--server") {
		name := flagValue(args, "-S")
		if name == "" {
			name = ipc.DefaultSessionName()
		}
		serverWorkDir := flagValue(args, "--dir")
		cmdParts := flagValueRest(args, "--cmd")
		doServerMode(name, cmdParts, serverWorkDir)
		return
	}

	// Kill all sessions
	if containsFlag(args, "--kill-all") {
		doKillAll()
		return
	}

	// Background update check (non-blocking, for user-facing commands only)
	go checkForUpdateBackground()

	// NX workspace subcommand: sax nx <command> [args...]
	if len(args) > 0 && args[0] == "nx" {
		nxArgs := args[1:]
		if len(nxArgs) == 0 {
			doNxList()
		} else {
			switch nxArgs[0] {
			case "serve":
				app := ""
				if len(nxArgs) > 1 {
					app = nxArgs[1]
				}
				doNxServe(app)
			case "list", "ls":
				doNxList()
			case "stop":
				app := ""
				if len(nxArgs) > 1 {
					app = nxArgs[1]
				}
				doNxStop(app)
			default:
				// Anything that isn't a sax-managed verb passes through to
				// the real nx, so `sax nx build app`, `sax nx test`,
				// `sax nx generate ...` behave like native nx.
				doNxPassthrough(nxArgs)
			}
		}
		return
	}

	// cuc dev-container subcommand: sax cuc <command>
	if len(args) > 0 && args[0] == "cuc" {
		cucArgs := args[1:]
		sub := ""
		if len(cucArgs) > 0 {
			sub = cucArgs[0]
		}
		switch sub {
		case "", "status":
			doCucStatus()
		case "up":
			doCucUp()
		case "claude", "attach":
			doCucClaude()
		case "down", "stop":
			doCucDown()
		default:
			fmt.Fprintf(os.Stderr, "sax cuc: unknown command %q (use up | claude | down | status)\n", sub)
			os.Exit(1)
		}
		return
	}

	// Agent commands (long flags handled before parseArgs)
	if containsFlag(args, "--tail") {
		name := flagValue(args, "--tail")
		if name == "" {
			fmt.Fprintln(os.Stderr, "sax: --tail requires a session name")
			os.Exit(1)
		}
		n := 10
		// Check if a number follows the name
		for i, a := range args {
			if a == "--tail" && i+2 < len(args) {
				if num, err := strconv.Atoi(args[i+2]); err == nil {
					n = num
				}
			}
		}
		doTail(name, n)
		return
	}
	if containsFlag(args, "--send") {
		name := flagValue(args, "--send")
		if name == "" {
			fmt.Fprintln(os.Stderr, "sax: --send requires a session name and text")
			os.Exit(1)
		}
		// Everything after --send <name> is the text to send
		text := ""
		for i, a := range args {
			if a == "--send" && i+2 < len(args) {
				text = strings.Join(args[i+2:], " ")
				break
			}
		}
		if text == "" {
			fmt.Fprintln(os.Stderr, "sax: --send requires text to send")
			os.Exit(1)
		}
		doSend(name, text)
		return
	}
	if containsFlag(args, "--status") {
		name := flagValue(args, "--status")
		if name == "" {
			fmt.Fprintln(os.Stderr, "sax: --status requires a session name")
			os.Exit(1)
		}
		doStatus(name)
		return
	}

	// Parse user-facing flags
	parsed := parseArgs(args)

	switch {
	case parsed.list:
		doListSessions()

	case parsed.kill:
		if parsed.name == "" {
			fmt.Fprintln(os.Stderr, "sax: --kill requires a session name")
			printUsage()
			os.Exit(1)
		}
		doKillSession(parsed.name)

	case parsed.exec:
		if parsed.name == "" || len(parsed.command) == 0 {
			fmt.Fprintln(os.Stderr, "sax: -x requires a session name and a command")
			fmt.Fprintln(os.Stderr, "  usage: sax -x <name> <command...>")
			os.Exit(1)
		}
		doExecSession(parsed.name, parsed.command, parsed.attach, parsed.workDir)

	case parsed.create:
		if parsed.name == "" {
			fmt.Fprintln(os.Stderr, "sax: -c requires a session name")
			printUsage()
			os.Exit(1)
		}
		doCreateSession(parsed.name, parsed.attach)

	case parsed.attach:
		if parsed.name == "" {
			fmt.Fprintln(os.Stderr, "sax: -a requires a session name")
			printUsage()
			os.Exit(1)
		}
		doAttach(parsed.name)

	default:
		// Bare `sax` — interactive session picker
		doInteractive()
	}
}

// --- Arg parsing ---

type parsedArgs struct {
	list    bool
	attach  bool
	create  bool
	exec    bool
	kill    bool
	name    string
	command []string
	workDir string
}

func parseArgs(args []string) parsedArgs {
	var p parsedArgs

	// Pre-extract --dir before main parsing (so -x doesn't swallow it as command)
	var filtered []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--dir" && i+1 < len(args) {
			p.workDir = args[i+1]
			i++ // skip value
		} else {
			filtered = append(filtered, args[i])
		}
	}
	args = filtered

	i := 0
	for i < len(args) {
		arg := args[i]

		// Long flags
		if arg == "--list" || arg == "-l" {
			p.list = true
			i++
			continue
		}
		if arg == "--kill" {
			p.kill = true
			i++
			// Next arg is the name
			if i < len(args) && !strings.HasPrefix(args[i], "-") {
				p.name = args[i]
				i++
			}
			continue
		}

		// Short flags (can be combined: -ca, -ac, -xa, -ax)
		if strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && len(arg) > 1 {
			flags := arg[1:]
			for _, ch := range flags {
				switch ch {
				case 'l':
					p.list = true
				case 'a':
					p.attach = true
				case 'c':
					p.create = true
				case 'x':
					p.exec = true
				default:
					fmt.Fprintf(os.Stderr, "sax: unknown flag -%c\n", ch)
					printUsage()
					os.Exit(1)
				}
			}
			i++

			// After flags, next positional arg is the session name
			if i < len(args) && !strings.HasPrefix(args[i], "-") {
				p.name = args[i]
				i++
			}

			// If -x, remaining args are the command
			if p.exec {
				p.command = args[i:]
				i = len(args)
			}
			continue
		}

		// Positional arg (session name if none set yet)
		if p.name == "" {
			p.name = arg
		}
		i++
	}

	// Validate name
	if p.name != "" {
		if err := ipc.ValidateSessionName(p.name); err != nil {
			fmt.Fprintf(os.Stderr, "sax: %v\n", err)
			os.Exit(1)
		}
	}

	return p
}

func printUsage() {
	fmt.Fprintln(os.Stderr, `
usage: sax                          interactive session picker
       sax -a <name>                attach to session
       sax -c <name>                create session (detached)
       sax -ca <name>               create + attach
       sax -x <name> <command...>   run command in new session (detached)
       sax -xa <name> <command...>  run command + attach
       sax -l, --list               list active sessions
       sax --kill <name>            kill a session
       sax --kill-all               kill all sessions
       sax themes                   list available color themes
       sax themes set <name>        apply a theme preset
       sax mcp                      start MCP server (stdio, for AI agents)
       sax update                   update sax to the latest release
       sax --version, -v            show version

agent: sax --tail <name> [n]        last N lines from active pane (default 10)
       sax --send <name> <text>     send text to active pane
       sax --status <name>          session status as JSON

    nx: sax nx                       list NX projects with serve targets
       sax nx serve [app]           serve an app (interactive if no app)
       sax nx stop [app]            stop an app (all NX sessions if no app)
       sax nx <app>                 shorthand for sax nx serve <app>`)
}

// --- Actions ---

func doInteractive() {
	sessions, err := ipc.ListSessions()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sax: %v\n", err)
		os.Exit(1)
	}

	// Filter to alive sessions
	var alive []ipc.SessionInfo
	for _, s := range sessions {
		if ipc.IsSessionAlive(s.Name) {
			alive = append(alive, s)
		} else {
			ipc.CleanupSocket(s.Name)
			os.Remove(ipc.PidPath(s.Name))
		}
	}

	if len(alive) == 0 {
		// No sessions — create default and attach
		fmt.Println("No active sessions. Creating 'default'...")
		doCreateSession("default", true)
		return
	}

	// Show session list
	fmt.Println("  Active sessions:")
	fmt.Println()
	for i, s := range alive {
		info := ipc.QuerySession(s.Name)
		status := "detached"
		portStr := ""
		if info != nil {
			if clients, ok := info["clients"].(float64); ok && clients > 0 {
				status = fmt.Sprintf("%d attached", int(clients))
			}
			if ports, ok := info["ports"].([]interface{}); ok && len(ports) > 0 {
				var pp []string
				for _, pi := range ports {
					if pm, ok := pi.(map[string]interface{}); ok {
						port := int(pm["port"].(float64))
						proc, _ := pm["process"].(string)
						if proc != "" {
							pp = append(pp, fmt.Sprintf("%s:%d", proc, port))
						} else {
							pp = append(pp, fmt.Sprintf(":%d", port))
						}
					}
				}
				if len(pp) > 0 {
					portStr = "  ports: " + strings.Join(pp, ", ")
				}
			}
		}
		fmt.Printf("  [%d] %s  (%s)%s\n", i+1, s.Name, status, portStr)
	}

	fmt.Println()
	fmt.Printf("  [n] Create new session\n")
	fmt.Println()
	fmt.Print("  Select: ")

	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return
	}
	input := strings.TrimSpace(scanner.Text())

	if input == "" {
		return
	}

	if input == "n" || input == "N" {
		fmt.Print("  Session name: ")
		if !scanner.Scan() {
			return
		}
		name := strings.TrimSpace(scanner.Text())
		if name == "" {
			name = fmt.Sprintf("session-%d", time.Now().Unix())
		}
		if err := ipc.ValidateSessionName(name); err != nil {
			fmt.Fprintf(os.Stderr, "sax: %v\n", err)
			os.Exit(1)
		}
		doCreateSession(name, true)
		return
	}

	// Try as number
	if idx, err := strconv.Atoi(input); err == nil && idx >= 1 && idx <= len(alive) {
		connectClient(alive[idx-1].Name)
		return
	}

	// Try as session name
	for _, s := range alive {
		if s.Name == input {
			connectClient(s.Name)
			return
		}
	}

	fmt.Fprintf(os.Stderr, "sax: unknown selection %q\n", input)
	os.Exit(1)
}

func doListSessions() {
	sessions, err := ipc.ListSessions()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sax: %v\n", err)
		os.Exit(1)
	}

	if len(sessions) == 0 {
		fmt.Println("No active sessions.")
		return
	}

	hasAny := false
	for _, s := range sessions {
		if !ipc.IsSessionAlive(s.Name) {
			ipc.CleanupSocket(s.Name)
			os.Remove(ipc.PidPath(s.Name))
			continue
		}
		hasAny = true

		info := ipc.QuerySession(s.Name)
		status := "detached"
		portStr := ""
		pid := ""

		if info != nil {
			if clients, ok := info["clients"].(float64); ok && clients > 0 {
				status = fmt.Sprintf("%d attached", int(clients))
			}
			if p, ok := info["created_pid"].(float64); ok {
				pid = fmt.Sprintf("%d", int(p))
			}
			if ports, ok := info["ports"].([]interface{}); ok && len(ports) > 0 {
				var pp []string
				for _, pi := range ports {
					if pm, ok := pi.(map[string]interface{}); ok {
						port := int(pm["port"].(float64))
						proc, _ := pm["process"].(string)
						if proc != "" {
							pp = append(pp, fmt.Sprintf("%s:%d", proc, port))
						} else {
							pp = append(pp, fmt.Sprintf(":%d", port))
						}
					}
				}
				if len(pp) > 0 {
					portStr = "  " + strings.Join(pp, ", ")
				}
			}
		}

		fmt.Printf("  %-20s  %-14s  pid %-8s  %s%s\n",
			s.Name,
			status,
			pid,
			s.CreatedAt.Format("2006-01-02 15:04"),
			portStr,
		)
	}

	if !hasAny {
		fmt.Println("No active sessions.")
	}
}

func doKillAll() {
	sessions, err := ipc.ListSessions()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sax: %v\n", err)
		os.Exit(1)
	}

	killed := 0
	for _, s := range sessions {
		if !ipc.IsSessionAlive(s.Name) {
			ipc.CleanupSocket(s.Name)
			os.Remove(ipc.PidPath(s.Name))
			continue
		}

		pidData, err := os.ReadFile(ipc.PidPath(s.Name))
		if err != nil {
			continue
		}
		pidStr := strings.TrimSpace(string(pidData))
		var pid int
		fmt.Sscanf(pidStr, "%d", &pid)
		if pid > 0 {
			proc, err := os.FindProcess(pid)
			if err == nil {
				_ = proc.Signal(os.Interrupt)
			}
		}
		ipc.CleanupSocket(s.Name)
		os.Remove(ipc.PidPath(s.Name))
		fmt.Printf("  killed %s (pid %d)\n", s.Name, pid)
		killed++
	}

	if killed == 0 {
		fmt.Println("No active sessions to kill.")
	} else {
		fmt.Printf("Killed %d session(s).\n", killed)
	}
}

func doKillSession(name string) {
	if !ipc.IsSessionAlive(name) {
		fmt.Fprintf(os.Stderr, "sax: session %q not found or not running\n", name)
		ipc.CleanupSocket(name)
		os.Remove(ipc.PidPath(name))
		os.Exit(1)
	}

	pidData, err := os.ReadFile(ipc.PidPath(name))
	if err != nil {
		fmt.Fprintf(os.Stderr, "sax: cannot read PID file for session %q: %v\n", name, err)
		os.Exit(1)
	}

	pidStr := strings.TrimSpace(string(pidData))
	var pid int
	fmt.Sscanf(pidStr, "%d", &pid)
	if pid > 0 {
		proc, err := os.FindProcess(pid)
		if err == nil {
			_ = proc.Signal(os.Interrupt)
			fmt.Printf("Killed session %q (pid %d)\n", name, pid)
		}
	}

	ipc.CleanupSocket(name)
	os.Remove(ipc.PidPath(name))
}

func doCreateSession(name string, attach bool) {
	if ipc.IsSessionAlive(name) {
		if attach {
			connectClient(name)
		} else {
			fmt.Printf("Session %q already exists.\n", name)
		}
		return
	}

	if err := launchDaemon(name, nil, ""); err != nil {
		fmt.Fprintf(os.Stderr, "sax: failed to start daemon: %v\n", err)
		os.Exit(1)
	}

	if !waitForSession(name) {
		fmt.Fprintf(os.Stderr, "sax: timed out waiting for session %q to start\n", name)
		os.Exit(1)
	}

	if attach {
		connectClient(name)
	} else {
		fmt.Printf("Session %q created (detached).\n", name)
	}
}

func doAttach(name string) {
	if !ipc.IsSessionAlive(name) {
		fmt.Fprintf(os.Stderr, "sax: session %q not found. Use 'sax -l' to list sessions.\n", name)
		os.Exit(1)
	}
	connectClient(name)
}

func doExecSession(name string, command []string, attach bool, workDir string) {
	if ipc.IsSessionAlive(name) {
		fmt.Fprintf(os.Stderr, "sax: session %q already exists\n", name)
		os.Exit(1)
	}

	if err := launchDaemon(name, command, workDir); err != nil {
		fmt.Fprintf(os.Stderr, "sax: failed to start daemon: %v\n", err)
		os.Exit(1)
	}

	if !waitForSession(name) {
		fmt.Fprintf(os.Stderr, "sax: timed out waiting for session %q to start\n", name)
		os.Exit(1)
	}

	if attach {
		connectClient(name)
	} else {
		fmt.Printf("Session %q created running %q (detached).\n", name, strings.Join(command, " "))
	}
}

// --- cuc dev container ---

// runForeground runs a command with inherited stdio (so build/compose output
// streams to the user) in the given directory ("" = current dir).
func runForeground(command []string, dir string) error {
	if len(command) == 0 {
		return fmt.Errorf("empty command")
	}
	c := exec.Command(command[0], command[1:]...)
	c.Dir = dir
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

// doCucUp builds + starts the cuc dev container (via compose when a compose file
// is found, otherwise by pulling and running the published image), then seeds
// credentials best-effort.
func doCucUp() {
	if !cuc.DockerAvailable() {
		fmt.Fprintln(os.Stderr, "sax cuc: docker not found on PATH")
		os.Exit(1)
	}

	if root := cuc.ComposeRoot(""); root != "" {
		fmt.Printf("sax cuc: building + starting via docker compose (%s)\n", root)
		if err := runForeground(cuc.ComposeUp(), root); err != nil {
			fmt.Fprintf(os.Stderr, "sax cuc: compose up failed: %v\n", err)
			os.Exit(1)
		}
	} else {
		image := cuc.DefaultImage
		fmt.Printf("sax cuc: no compose file found; pulling %s\n", image)
		if err := runForeground(cuc.PullImage(image), ""); err != nil {
			fmt.Fprintf(os.Stderr, "sax cuc: pull failed: %v\n", err)
			os.Exit(1)
		}
		if cuc.Exists(cuc.DefaultContainer) {
			_ = runForeground(cuc.RemoveContainer(cuc.DefaultContainer), "")
		}
		wd, _ := os.Getwd()
		home, _ := os.UserHomeDir()
		if err := runForeground(cuc.RunImage(cuc.DefaultContainer, image, wd, home), ""); err != nil {
			fmt.Fprintf(os.Stderr, "sax cuc: docker run failed: %v\n", err)
			os.Exit(1)
		}
	}

	// Seed credentials + report toolchain (best-effort; failure is non-fatal).
	_ = runForeground(cuc.Seed(cuc.DefaultContainer), "")
	fmt.Printf("sax cuc: container %q ready. Launch Claude with: sax cuc claude\n", cuc.DefaultContainer)
}

// doCucClaude launches (or reattaches) a Claude instance running inside the
// container as a SAX session, so it gets detach/reattach + the scripting/MCP API.
func doCucClaude() {
	if !cuc.Running(cuc.DefaultContainer) {
		fmt.Fprintf(os.Stderr, "sax cuc: container %q is not running. Start it with: sax cuc up\n", cuc.DefaultContainer)
		os.Exit(1)
	}

	name := cuc.SessionName
	if ipc.IsSessionAlive(name) {
		fmt.Printf("sax cuc: attaching to existing session %q\n", name)
		connectClient(name)
		return
	}

	if err := launchDaemon(name, cuc.Claude(cuc.DefaultContainer), ""); err != nil {
		fmt.Fprintf(os.Stderr, "sax cuc: failed to start session: %v\n", err)
		os.Exit(1)
	}
	if !waitForSession(name) {
		fmt.Fprintf(os.Stderr, "sax cuc: timed out starting session %q\n", name)
		os.Exit(1)
	}
	connectClient(name)
}

// doCucDown stops the Claude session (if any) and tears the container down.
func doCucDown() {
	if !cuc.DockerAvailable() {
		fmt.Fprintln(os.Stderr, "sax cuc: docker not found on PATH")
		os.Exit(1)
	}

	if ipc.IsSessionAlive(cuc.SessionName) {
		doKillSession(cuc.SessionName)
	}

	if root := cuc.ComposeRoot(""); root != "" {
		_ = runForeground(cuc.ComposeDown(), root)
	} else if cuc.Exists(cuc.DefaultContainer) {
		_ = runForeground(cuc.RemoveContainer(cuc.DefaultContainer), "")
	}
	fmt.Println("sax cuc: stopped.")
}

// doCucStatus prints container + session state as JSON.
func doCucStatus() {
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
	fmt.Println(string(b))
}

// --- NX workspace ---

func nxDiscover() *nx.Workspace {
	ws, err := nx.Discover("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "sax: %v\n", err)
		os.Exit(1)
	}
	return ws
}

func nxLaunchProject(project nx.Project, targetName string) {
	// Find the matching target
	var target *nx.Target
	for _, t := range project.Targets {
		if t.Name == targetName {
			target = &t
			break
		}
	}
	if target == nil {
		// Default to first serve-like target
		for _, t := range project.Targets {
			target = &t
			break
		}
	}
	if target == nil {
		fmt.Fprintf(os.Stderr, "  no serve target found for %s\n", project.Name)
		return
	}

	sessionName := project.Name
	cmd := nx.NxCommand(project.Name, target.Name)

	if ipc.IsSessionAlive(sessionName) {
		fmt.Printf("  %s already running\n", sessionName)
		return
	}

	if err := launchDaemon(sessionName, cmd, ""); err != nil {
		fmt.Fprintf(os.Stderr, "  failed to start %s: %v\n", sessionName, err)
		return
	}
	if !waitForSession(sessionName) {
		fmt.Fprintf(os.Stderr, "  timed out starting %s\n", sessionName)
		return
	}
	fmt.Printf("  started %s (%s:%s)\n", sessionName, project.Name, target.Name)
}

// doNxList shows all NX projects with serve targets.
func doNxList() {
	ws := nxDiscover()
	projects := ws.ServeTargets()

	fmt.Printf("  NX Workspace: %s\n\n", ws.Root)

	if len(projects) == 0 {
		fmt.Println("  No serve targets found.")
		fmt.Printf("  Total projects: %d\n", len(ws.Projects))
		return
	}

	for _, p := range projects {
		var targets []string
		for _, t := range p.Targets {
			targets = append(targets, t.Name)
		}
		status := ""
		if ipc.IsSessionAlive(p.Name) {
			status = " (running)"
		}
		fmt.Printf("  %-20s  [%s]%s\n", p.Name, strings.Join(targets, ", "), status)
	}
}

// doNxServe launches a serve target for a specific app, or prompts if no app given.
func doNxServe(app string) {
	ws := nxDiscover()
	projects := ws.ServeTargets()

	if len(projects) == 0 {
		fmt.Println("No serve targets found in NX workspace.")
		return
	}

	// If app specified, find and launch it directly
	if app != "" {
		for _, p := range projects {
			if p.Name == app {
				nxLaunchProject(p, "serve")
				return
			}
		}
		// Try partial match
		for _, p := range projects {
			if strings.Contains(p.Name, app) {
				nxLaunchProject(p, "serve")
				return
			}
		}
		fmt.Fprintf(os.Stderr, "sax: no NX project matching %q with a serve target\n", app)
		os.Exit(1)
	}

	// No app specified — interactive picker
	fmt.Printf("  NX Workspace: %s\n\n", ws.Root)

	for i, p := range projects {
		status := ""
		if ipc.IsSessionAlive(p.Name) {
			status = " (running)"
		}
		fmt.Printf("  [%d] %s%s\n", i+1, p.Name, status)
	}

	fmt.Println()
	fmt.Printf("  [a] Launch all    [q] Quit\n\n")
	fmt.Print("  Select: ")

	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return
	}
	input := strings.TrimSpace(scanner.Text())

	if input == "" || input == "q" || input == "Q" {
		return
	}

	if input == "a" || input == "A" {
		for _, p := range projects {
			nxLaunchProject(p, "serve")
		}
		return
	}

	idx, err := strconv.Atoi(input)
	if err != nil || idx < 1 || idx > len(projects) {
		fmt.Fprintf(os.Stderr, "sax: invalid selection %q\n", input)
		os.Exit(1)
	}
	nxLaunchProject(projects[idx-1], "serve")
}

// doNxStop stops a running NX project session, or all if no app given.
func doNxStop(app string) {
	ws := nxDiscover()
	projects := ws.ServeTargets()

	if app != "" {
		// Stop specific app
		if ipc.IsSessionAlive(app) {
			doKillSession(app)
		} else {
			fmt.Fprintf(os.Stderr, "sax: session %q is not running\n", app)
			os.Exit(1)
		}
		return
	}

	// Stop all NX project sessions
	killed := 0
	for _, p := range projects {
		if ipc.IsSessionAlive(p.Name) {
			doKillSession(p.Name)
			killed++
		}
	}
	if killed == 0 {
		fmt.Println("No NX sessions running.")
	}
}

// doNxPassthrough runs the real nx with the given arguments in the foreground,
// inheriting stdio so it behaves exactly like invoking nx directly.
func doNxPassthrough(args []string) {
	bin, base := nx.Resolve()
	cmd := exec.Command(bin, append(base, args...)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "sax: nx: %v\n", err)
		os.Exit(1)
	}
}

// --- Completion ---

// doCompletion prints the shell completion script for the given shell.
func doCompletion(args []string) {
	shell := ""
	if len(args) > 0 {
		shell = args[0]
	}
	script, err := completion.Script(shell)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sax: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(script)
}

// doComplete prints sax completion candidates (one per line) for the hidden
// __complete protocol the generated shell scripts call.
func doComplete(args []string) {
	for _, c := range completion.SaxCandidates(args) {
		fmt.Println(c)
	}
}

// doNxComplete prints nx completion candidates (one per line).
func doNxComplete(args []string) {
	for _, c := range completion.NxCandidates(args) {
		fmt.Println(c)
	}
}

// --- Setup ---

func doSetup(args []string) {
	if len(args) == 0 {
		printSetupUsage()
		return
	}
	switch args[0] {
	case "completion":
		doSetupCompletion(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "sax: unknown setup command %q\n", args[0])
		printSetupUsage()
		os.Exit(1)
	}
}

func printSetupUsage() {
	fmt.Fprintln(os.Stderr, `usage: sax setup completion [--shell <zsh|bash|pwsh>] [--install]
       (without --install, prints what to add to your shell profile)`)
}

// doSetupCompletion installs (or shows how to install) sax shell completion in
// the user's shell profile.
func doSetupCompletion(args []string) {
	shell := ""
	install := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--shell":
			if i+1 < len(args) {
				shell = args[i+1]
				i++
			}
		case "--install":
			install = true
		}
	}
	if shell == "" {
		shell = completion.DetectShell()
	}

	if !install {
		path, _ := completion.ProfilePath(shell)
		fmt.Printf("Detected shell: %s\n", shell)
		if path != "" {
			fmt.Printf("Add sax completion to your profile (%s) by running:\n\n", path)
		}
		fmt.Printf("  sax setup completion --shell %s --install\n\n", shell)
		fmt.Println("Or load it for the current session only:")
		switch shell {
		case "pwsh", "powershell":
			fmt.Println("  sax completion pwsh | Out-String | Invoke-Expression")
		default:
			fmt.Printf("  source <(sax completion %s)\n", shell)
		}
		return
	}

	path, changed, err := completion.InstallToProfile(shell)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sax: %v\n", err)
		os.Exit(1)
	}
	if changed {
		fmt.Printf("Installed sax completion for %s in %s\n", shell, path)
	} else {
		fmt.Printf("sax completion for %s already up to date in %s\n", shell, path)
	}
	fmt.Println("Restart your shell or re-source the profile to activate it.")
}

// --- Agent commands ---

func doTail(name string, n int) {
	if !ipc.IsSessionAlive(name) {
		fmt.Fprintf(os.Stderr, "sax: session %q not found\n", name)
		os.Exit(1)
	}
	reply := ipc.QuerySessionTyped(name, "tail", fmt.Sprintf("%d", n))
	if reply == nil {
		fmt.Fprintf(os.Stderr, "sax: failed to query session %q\n", name)
		os.Exit(1)
	}
	if tail, ok := reply["tail"].(string); ok {
		fmt.Print(tail)
		if tail != "" && !strings.HasSuffix(tail, "\n") {
			fmt.Println()
		}
	}
}

func doSend(name, text string) {
	if !ipc.IsSessionAlive(name) {
		fmt.Fprintf(os.Stderr, "sax: session %q not found\n", name)
		os.Exit(1)
	}
	// Interpret escape sequences: \n → LF, \r → CR (Enter key in raw PTY)
	text = strings.ReplaceAll(text, `\r`, "\r")
	text = strings.ReplaceAll(text, `\n`, "\n")
	reply := ipc.QuerySessionTyped(name, "send", text)
	if reply == nil {
		fmt.Fprintf(os.Stderr, "sax: failed to send to session %q\n", name)
		os.Exit(1)
	}
	if ok, _ := reply["ok"].(bool); ok {
		fmt.Println("sent")
	} else {
		fmt.Fprintln(os.Stderr, "sax: send failed")
		os.Exit(1)
	}
}

func doStatus(name string) {
	if !ipc.IsSessionAlive(name) {
		fmt.Fprintf(os.Stderr, "sax: session %q not found\n", name)
		os.Exit(1)
	}
	reply := ipc.QuerySessionTyped(name, "status", "")
	if reply == nil {
		fmt.Fprintf(os.Stderr, "sax: failed to query session %q\n", name)
		os.Exit(1)
	}
	out, err := json.MarshalIndent(reply, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "sax: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(out))
}

func doMCP() {
	srv := mcp.NewServer("sax", version)
	mcp.RegisterAllTools(srv, launchDaemon, waitForSession)
	srv.Run()
}

// resolveTheme layers .saxrc theme settings on top of the saved config theme:
// "set theme <preset>" swaps the base palette, and "set theme.<field> <color>"
// overrides individual colors. Returns a new Theme; the input is not mutated.
func resolveTheme(base config.Theme, rc *saxrc.Config) config.Theme {
	t := base
	if name := rc.ThemeName(); name != "" {
		if preset, ok := config.ThemePresets[name]; ok {
			t = preset
		}
	}
	for field, val := range rc.ThemeOverrides() {
		if val == "" {
			continue
		}
		switch field {
		case "bg":
			t.Bg = val
		case "fg":
			t.Fg = val
		case "fg_muted", "fg-muted":
			t.FgMuted = val
		case "accent":
			t.Accent = val
		case "accent_secondary", "accent-secondary":
			t.AccentSecondary = val
		case "surface":
			t.Surface = val
		case "surface_dark", "surface-dark":
			t.SurfaceDark = val
		case "success":
			t.Success = val
		case "border_inactive", "border-inactive":
			t.BorderInactive = val
		}
	}
	return t
}

func doServerMode(name string, command []string, workDir string) {
	// Force true color rendering — server runs as headless daemon without a TTY,
	// so termenv/lipgloss would otherwise detect no color support.
	os.Setenv("COLORTERM", "truecolor")

	logFile := ipc.SessionsDir() + "/" + name + ".log"
	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err == nil {
		log.SetOutput(f)
		defer f.Close()
	}

	// Load ~/.saxrc (zsh-style config). A missing file yields an empty config;
	// parse problems are logged as warnings and never block startup.
	rc := saxrc.Load()
	for _, w := range rc.Warnings {
		log.Printf("saxrc: %s", w)
	}

	// Load config and apply theme colors, with .saxrc overrides layered on top.
	if cfg, err := config.Load(); err == nil {
		theme.Init(resolveTheme(cfg.Theme, rc))
	}

	// Apply daemon-wide options from .saxrc.
	if n, ok := rc.HistoryLimit(); ok {
		scrollback.SetDefaultCapacity(n)
	}
	pty.Configure(rc.Shell(), rc.EnvStrings())

	// "set default-dir" provides a fallback working directory when one was not
	// supplied explicitly via --dir.
	if workDir == "" {
		workDir = saxrc.ExpandHome(rc.DefaultDir())
	}

	srv := server.NewServer(name)
	srv.Saxrc = rc
	srv.InitWorkDir = workDir
	if len(command) > 0 {
		srv.InitCmd = command[0]
		if len(command) > 1 {
			srv.InitCmdArgs = command[1:]
		}
	}
	if err := srv.Run(); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

// --- Themes ---

func doListThemes(args []string) {
	// sax themes set <name>
	if len(args) >= 2 && args[0] == "set" {
		name := args[1]
		if _, ok := config.ThemePresets[name]; !ok {
			fmt.Fprintf(os.Stderr, "sax: unknown theme %q\n", name)
			fmt.Fprintf(os.Stderr, "Available: %s\n", strings.Join(config.PresetNames(), ", "))
			os.Exit(1)
		}
		cfg, err := config.Load()
		if err != nil {
			fmt.Fprintf(os.Stderr, "sax: %v\n", err)
			os.Exit(1)
		}
		cfg.ThemeName = name
		cfg.Theme = config.ThemePresets[name]
		if err := cfg.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "sax: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Theme set to %q. Restart sessions to apply.\n", name)
		return
	}

	// sax themes — list available
	cfg, _ := config.Load()
	current := cfg.ThemeName
	if current == "" {
		current = "neon-blue"
	}

	fmt.Println("  Available themes:")
	fmt.Println()
	for _, name := range config.PresetNames() {
		t := config.ThemePresets[name]
		marker := "  "
		if name == current {
			marker = "* "
		}
		fmt.Printf("  %s%-20s  accent: %s  bg: %s\n", marker, name, t.Accent, t.Bg)
	}
	fmt.Println()
	fmt.Printf("  Current: %s\n", current)
	fmt.Println()
	fmt.Println("  Apply:  sax themes set <name>")
	fmt.Printf("  Config: %s\n", config.ConfigPath())
}

// --- Update ---

func doSelfUpdate(force bool) {
	if version == "dev" && !force {
		fmt.Fprintln(os.Stderr, "sax: cannot update a dev build — install from a release binary (use --force to override)")
		os.Exit(1)
	}

	fmt.Printf("sax: checking for updates (current: %s)...\n", version)

	latest, downloadURL, err := updater.CheckForUpdate(version)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sax: %v\n", err)
		os.Exit(1)
	}

	if !force && !updater.IsNewer(version, latest) {
		fmt.Printf("sax: already up to date (%s)\n", version)
		return
	}

	fmt.Printf("sax: downloading %s...\n", latest)
	if err := updater.DownloadAndReplace(downloadURL); err != nil {
		fmt.Fprintf(os.Stderr, "sax: update failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("sax: updated %s -> %s\n", version, latest)
}

func checkForUpdateBackground() {
	if version == "dev" {
		return
	}

	cfg, err := config.Load()
	if err != nil || !cfg.AutoUpdate {
		return
	}

	// If checked within the last 24 hours, use cached result
	if time.Since(cfg.LastCheckTime) < 24*time.Hour {
		if cfg.LatestVersion != "" && updater.IsNewer(version, cfg.LatestVersion) {
			fmt.Fprintf(os.Stderr, "sax: update available %s -> %s (run 'sax update')\n", version, cfg.LatestVersion)
		}
		return
	}

	latest, _, err := updater.CheckForUpdate(version)
	if err != nil {
		return
	}

	cfg.LastCheckTime = time.Now()
	cfg.LatestVersion = latest
	_ = cfg.Save()

	if updater.IsNewer(version, latest) {
		fmt.Fprintf(os.Stderr, "sax: update available %s -> %s (run 'sax update')\n", version, latest)
	}
}

// --- Helpers ---

func launchDaemon(name string, command []string, workDir string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable: %w", err)
	}

	cmdArgs := []string{"--server", "-S", name}
	if workDir != "" {
		cmdArgs = append(cmdArgs, "--dir", workDir)
	}
	if len(command) > 0 {
		cmdArgs = append(cmdArgs, "--cmd")
		cmdArgs = append(cmdArgs, command...)
	}

	cmd := exec.Command(exe, cmdArgs...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	// Force true color in the daemon process so lipgloss renders ANSI colors
	cmd.Env = append(os.Environ(), "COLORTERM=truecolor")

	setProcAttr(cmd)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}

	go cmd.Wait()
	return nil
}

func waitForSession(name string) bool {
	for i := 0; i < 50; i++ {
		if ipc.IsSessionAlive(name) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func connectClient(name string) {
	conn, err := ipc.Dial(name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sax: cannot connect to session %q: %v\n", name, err)
		os.Exit(1)
	}

	model := client.New(conn)

	opts := []tea.ProgramOption{tea.WithAltScreen()}
	// Mouse support is on unless ~/.saxrc explicitly sets "mouse off".
	if mouse, set := saxrc.Load().BoolOption("mouse"); !set || mouse {
		opts = append(opts, tea.WithMouseCellMotion())
	}

	p := tea.NewProgram(model, opts...)
	model.SetProgram(p)

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "sax: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("[detached from session " + name + "]")
}

func setProcAttr(cmd *exec.Cmd) {
	switch runtime.GOOS {
	case "windows":
		setWindowsProcAttr(cmd)
	default:
		setUnixProcAttr(cmd)
	}
}

// --- Flag helpers for internal --server mode ---

func containsFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

func flagValue(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// flagValueRest returns all args after the given flag.
func flagValueRest(args []string, flag string) []string {
	for i, a := range args {
		if a == flag {
			return args[i+1:]
		}
	}
	return nil
}
