package gateway

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/asc/sax/internal/vnet"
)

// Page banners: HTML documents served through the gateway get a small
// banner with the app's git branch, commit and worktree, so a browser tab
// says which checkout it shows. Only full page loads are touched; API
// calls, assets and WebSockets pass through unchanged.

const (
	gitTimeout     = time.Second
	gitCacheTTL    = 3 * time.Second
	maxBannerPage  = 8 << 20 // bigger pages are passed through untouched
	headerAccept   = "Accept"
	headerEncoding = "Content-Encoding"
)

type gitInfo struct {
	branch, commit, toplevel string
	dirty                    bool
	at                       time.Time
}

// gitCache remembers git info per folder for a few seconds, so page loads
// do not each run git.
type gitCache struct {
	mu      sync.Mutex
	entries map[string]gitInfo
}

func (c *gitCache) get(dir string) gitInfo {
	if dir == "" {
		return gitInfo{}
	}
	c.mu.Lock()
	if e, ok := c.entries[dir]; ok && time.Since(e.at) < gitCacheTTL {
		c.mu.Unlock()
		return e
	}
	c.mu.Unlock()

	info := readGit(dir)
	c.mu.Lock()
	if c.entries == nil {
		c.entries = map[string]gitInfo{}
	}
	c.entries[dir] = info
	c.mu.Unlock()
	return info
}

func readGit(dir string) gitInfo {
	info := gitInfo{at: time.Now()}
	branch, ok := git(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if !ok {
		return info // not a repository
	}
	if branch == "HEAD" {
		branch = "(detached)"
	}
	info.branch = branch
	info.commit, _ = git(dir, "rev-parse", "--short", "HEAD")
	info.toplevel, _ = git(dir, "rev-parse", "--show-toplevel")
	if out, ok := git(dir, "status", "--porcelain", "--untracked-files=no"); ok {
		info.dirty = out != ""
	}
	return info
}

func git(dir string, args ...string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

// wantsBanner reports whether a request is a page load the banner applies
// to: a GET that accepts HTML, through a router that has the banner on.
func wantsBanner(r vnet.Router, req *http.Request) bool {
	return !r.Banner.Off && req.Method == http.MethodGet && strings.Contains(req.Header.Get(headerAccept), "text/html")
}

// appFor names the router session a rule target points at.
func appFor(r vnet.Router, target vnet.Endpoint) string {
	if _, ok := r.Sessions[target.Host]; ok {
		return target.Host
	}
	for name, ip := range r.Sessions {
		if ip == target.Host {
			return name
		}
	}
	return target.Host
}

// addBanner rewrites an HTML page response to include the router's banner.
// Anything it cannot safely rewrite is passed through.
func (g *Gateway) addBanner(resp *http.Response, r vnet.Router, app, host string) error {
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		return nil
	}
	if enc := resp.Header.Get(headerEncoding); enc != "" && enc != "identity" {
		return nil // the app compressed it anyway
	}
	page, err := io.ReadAll(io.LimitReader(resp.Body, maxBannerPage+1))
	if err != nil {
		return err
	}
	if len(page) > maxBannerPage {
		resp.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(page), resp.Body), resp.Body}
		return nil
	}
	_ = resp.Body.Close()

	info := g.git.get(r.Dirs[app])
	vars := vnet.BannerVars{
		Branch: info.branch, Commit: info.commit, Dirty: info.dirty,
		WorktreePath: info.toplevel, Router: r.Name, App: app, Host: hostOnly(host),
		IP: r.Sessions[app], Dir: r.Dirs[app],
	}
	if info.toplevel != "" {
		vars.Worktree = filepath.Base(info.toplevel)
	}
	banner, err := r.Banner.Render(vars)
	if err != nil {
		g.cfg.Logf("gateway: router %s: %v", r.Name, err)
	}
	if banner != "" {
		page = vnet.InjectBanner(page, banner)
	}
	resp.Body = io.NopCloser(bytes.NewReader(page))
	resp.ContentLength = int64(len(page))
	resp.Header.Set("Content-Length", strconv.Itoa(len(page)))
	return nil
}

func hostOnly(h string) string {
	if i := strings.LastIndexByte(h, ':'); i > 0 && !strings.Contains(h[i:], "]") {
		return h[:i]
	}
	return h
}
