package main

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/asc/sax/internal/vnet"
)

// errReplaceUnsupported means the router's session predates replacing tabs.
var errReplaceUnsupported = errors.New("session cannot restart tabs")

// errNoTab means the app has no tab yet (it was added to the file after up).
var errNoTab = errors.New("no such tab")

// restartTarget resolves `sax net restart [project|router] [app...]` like
// sax net logs: no arguments is this folder's project; a project or router
// name (or <project>.<app>) works from any folder; bare app names are apps
// of this folder's project.
func restartTarget(pos []string) (projectRef, []string, error) {
	if len(pos) == 0 {
		return projectRef{}, nil, nil
	}
	if _, err := resolveProject(projectRef{Name: pos[0]}); err == nil {
		return projectRef{Name: pos[0]}, pos[1:], nil
	}
	if head, app, ok := strings.Cut(pos[0], "."); ok {
		if _, err := resolveProject(projectRef{Name: head}); err == nil {
			return projectRef{Name: head}, append([]string{app}, pos[1:]...), nil
		}
	}
	if p, err := resolveProject(projectRef{}); err == nil {
		if _, isApp := p.Sessions[pos[0]]; isApp {
			return projectRef{}, pos, nil
		}
	}
	return projectRef{}, nil, fmt.Errorf("no project %q", pos[0])
}

// netRestart replaces running apps with fresh ones: each app's tab is
// swapped for a new one, its old process tree is stopped, and its command
// (re-read from the file, so edits apply) starts once the old one is gone.
// Without apps, every app of the project restarts. With o.Follow it then
// follows the restarted apps' logs.
func netRestart(w io.Writer, s vnet.Store, run sessionRunner, pos []string, o logOpts) error {
	ref, apps, err := restartTarget(pos)
	if err != nil {
		return err
	}
	u, err := prepareProject(w, s, run, ref)
	if err != nil {
		return err
	}
	router := u.router.Name
	if !run.Alive(router) {
		return fmt.Errorf("%s is not up (sax net up %s)", u.Name, u.Name)
	}
	if len(apps) == 0 {
		apps = u.env.SessionNames()
	}
	for _, app := range apps {
		if _, ok := u.env.Sessions[app]; !ok {
			return fmt.Errorf("no app %q in %s (apps: %s)", app, u.label(), strings.Join(u.env.SessionNames(), ", "))
		}
	}

	for _, app := range apps {
		killed, err := run.ReplaceTab(router, u.tab(app))
		switch {
		case errors.Is(err, errReplaceUnsupported):
			return fmt.Errorf("router %s runs an older sax that cannot restart apps; restart it once: sax net down %s, then sax net up %s",
				router, router, u.Name)
		case errors.Is(err, errNoTab):
			if _, err := run.AddTab(router, u.tab(app)); err != nil {
				return err
			}
			fmt.Fprintf(w, "%s: started (it was not running)\n", app)
		case err != nil:
			return fmt.Errorf("restart %s: %w", app, err)
		default:
			fmt.Fprintf(w, "%s: restarted (%d old processes stopped)\n", app, killed)
		}
	}
	printWarnings(w, u.warnings)
	if !o.Follow {
		return nil
	}
	if len(apps) == len(u.env.Sessions) {
		apps = nil // every app, including ones added later
	}
	return netLogs(w, s, run, append([]string{router}, apps...), o)
}
