package main

import (
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/asc/sax/internal/ipc"
	"github.com/asc/sax/internal/scrollback"
	"github.com/asc/sax/internal/vnet"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/term"
)

// defaultLogLines is the backlog per app without -n.
const defaultLogLines = 100

// streamLogs is ipc.Logs; swapped in tests.
var streamLogs = ipc.Logs

type logOpts struct {
	Follow bool
	Lines  int
	Color  bool
}

// logOptsFromArgs reads -f/--follow, -n N|all and --no-color.
func logOptsFromArgs(args []string) (logOpts, error) {
	o := logOpts{
		Follow: containsFlag(args, "-f") || containsFlag(args, "--follow"),
		Lines:  defaultLogLines,
		Color: !containsFlag(args, "--no-color") && os.Getenv("NO_COLOR") == "" &&
			term.IsTerminal(int(os.Stdout.Fd())),
	}
	if v := flagValue(args, "-n"); v != "" {
		if v == "all" {
			o.Lines = scrollback.ConfiguredCapacity()
		} else if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			o.Lines = n
		} else {
			return o, fmt.Errorf("-n wants a number or all, got %q", v)
		}
	}
	return o, nil
}

// logsTarget resolves `sax net logs [project|router] [app...]` to the
// router (its session holds one tab per app) and the apps to show:
//   - no arguments: the project of this folder (./.sax.lan or ~/.sax.net)
//   - <project|router> [app...] or <project>.<app>, from any folder
//   - <app...> of this folder's project
func logsTarget(s vnet.Store, pos []string) (string, []string, error) {
	if len(pos) == 0 {
		p, err := resolveProject(projectRef{})
		if err != nil {
			return "", nil, err
		}
		return p.Router, nil, nil
	}
	if r, err := routerFor(s, pos[0]); err == nil {
		if len(pos) == 1 {
			return r, nil, nil // every app
		}
		return r, pos[1:], nil
	}
	if head, app, ok := strings.Cut(pos[0], "."); ok {
		if r, err := routerFor(s, head); err == nil {
			return r, append([]string{app}, pos[1:]...), nil
		}
	}
	if p, err := resolveProject(projectRef{}); err == nil {
		if _, isApp := p.Sessions[pos[0]]; isApp {
			return p.Router, pos, nil
		}
	}
	return "", nil, fmt.Errorf("no router or project %q", pos[0])
}

// netLogs prints the apps' output compose style ("api | ..."); with Follow
// it keeps printing new lines until interrupted or the router stops.
func netLogs(w io.Writer, s vnet.Store, run sessionRunner, pos []string, o logOpts) error {
	router, apps, err := logsTarget(s, pos)
	if err != nil {
		return err
	}
	if !run.Alive(router) {
		return fmt.Errorf("router %s is not up (sax net up %s)", router, router)
	}

	width := 0
	for _, a := range apps {
		width = max(width, len(a))
	}
	onStart := func(tabs []string) {
		for _, t := range tabs {
			width = max(width, len(t))
		}
	}
	onLines := func(m ipc.LogLines) error {
		if m.Skipped > 0 {
			if _, err := fmt.Fprintln(w, logLine(m.Tab, width, fmt.Sprintf("… %d lines skipped (scrollback full)", m.Skipped), o.Color)); err != nil {
				return err
			}
		}
		for _, line := range m.Lines {
			if _, err := fmt.Fprintln(w, logLine(m.Tab, width, line, o.Color)); err != nil {
				return err
			}
		}
		return nil
	}

	req := ipc.LogsRequest{Tabs: apps, Lines: o.Lines, Follow: o.Follow}
	reason, err := streamLogs(router, req, onStart, onLines)
	if errors.Is(err, ipc.ErrLogsUnsupported) {
		return fmt.Errorf("router %s runs an older sax that cannot stream logs; restart it: sax net down %s, then sax net up the project again",
			router, router)
	}
	if err != nil {
		return err
	}
	if o.Follow && reason != "" && reason != "done" {
		fmt.Fprintf(w, "[%s: %s]\n", router, reason)
	}
	return nil
}

// logColors are the prefix colors, picked per app name so an app keeps its
// color across runs.
var logColors = []string{"36", "33", "32", "35", "34", "91", "96", "93"}

// logLine formats one line: "<app padded> | <line>". Without color, the
// line's own styling is stripped too.
func logLine(tab string, width int, line string, color bool) string {
	prefix := tab + strings.Repeat(" ", max(0, width-len(tab))) + " | "
	if !color {
		return prefix + ansi.Strip(line)
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(tab))
	c := logColors[h.Sum32()%uint32(len(logColors))]
	return "\x1b[" + c + "m" + prefix + "\x1b[0m" + line + "\x1b[0m"
}
