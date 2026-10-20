package gateway

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asc/sax/internal/vnet"
)

// gitWorktree makes a repo with a commit and a linked worktree on branch
// feat/banner, returning the worktree's folder.
func gitWorktree(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	wt := filepath.Join(root, "repo-banner")
	run := func(dir string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t",
			"GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_GLOBAL=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	_ = os.MkdirAll(repo, 0o755)
	run(repo, "init", "-q", "-b", "main")
	_ = os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a"), 0o644)
	run(repo, "add", ".")
	run(repo, "commit", "-q", "-m", "init")
	run(repo, "worktree", "add", "-q", "-b", "feat/banner", wt)
	_ = os.WriteFile(filepath.Join(wt, "a.txt"), []byte("changed"), 0o644) // dirty
	return wt
}

// bannerSetup runs a gateway for router v1 whose web app serves the given
// handler, with banner b and the app's folder dir.
func bannerSetup(t *testing.T, b vnet.Banner, dir string, h http.HandlerFunc) string {
	t.Helper()
	s := vnet.NewStore(t.TempDir())
	r, _ := vnet.NewRouter("v1", "10.12.12.0/24", "lan")
	if err := s.Create(r); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update("v1", func(r vnet.Router) (vnet.Router, error) {
		r, _, _ = r.WithSession("web", "10.12.12.5")
		r = r.WithDir("web", dir).WithBanner(b)
		return r.WithRule("bob.lan/* -> web:4200")
	}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	real := srv.Listener.Addr().(*net.TCPAddr).Port
	if err := vnet.RegisterPort(s.RouterDir("v1"), "10.12.12.5", 4200, real, os.Getpid()); err != nil {
		t.Fatal(err)
	}
	g, _ := startGateway(t, s)
	return g.HostAddr()
}

func page(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Accept-Encoding") != "" {
		w.Header().Set("X-Got-Accept-Encoding", r.Header.Get("Accept-Encoding"))
	}
	switch r.URL.Path {
	case "/api":
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	case "/gz":
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Content-Encoding", "gzip")
		zw := gzip.NewWriter(w)
		fmt.Fprint(zw, "<html><body>zipped</body></html>")
		zw.Close()
	default:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, "<html><body><h1>app</h1></body></html>")
	}
}

func fetch(t *testing.T, addr, path, accept string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", "http://"+addr+path, nil)
	req.Host = "bob.lan.v1.localhost"
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	req.Header.Set("Accept-Encoding", "gzip") // like a browser
	tr := &http.Transport{DisableCompression: true}
	resp, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

const htmlAccept = "text/html,application/xhtml+xml,*/*;q=0.8"

func TestBanner_DefaultShowsGitInfo(t *testing.T) {
	wt := gitWorktree(t)
	addr := bannerSetup(t, vnet.Banner{}, wt, page)

	resp, body := fetch(t, addr, "/", htmlAccept)
	for _, want := range []string{`id="sax-vnet-banner"`, "⎇ feat/banner* · ", " · repo-banner · v1 › web"} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q:\n%s", want, body)
		}
	}
	if !strings.HasSuffix(body, "</body></html>") || !strings.Contains(body, "<h1>app</h1>") {
		t.Errorf("banner not placed before </body>: %s", body)
	}
	if resp.ContentLength != int64(len(body)) {
		t.Errorf("Content-Length %d, body %d", resp.ContentLength, len(body))
	}
	if resp.Header.Get("X-Got-Accept-Encoding") != "" {
		t.Error("page requests must ask the app for uncompressed HTML")
	}
}

func TestBanner_LeavesOtherResponsesAlone(t *testing.T) {
	addr := bannerSetup(t, vnet.Banner{}, t.TempDir(), page)

	if _, body := fetch(t, addr, "/api", "application/json"); body != `{"ok":true}` {
		t.Errorf("json = %q", body)
	}
	if resp, _ := fetch(t, addr, "/api", "application/json"); resp.Header.Get("X-Got-Accept-Encoding") != "gzip" {
		t.Error("non-page requests keep their Accept-Encoding")
	}
	resp, body := fetch(t, addr, "/gz", htmlAccept)
	zr, err := gzip.NewReader(bytes.NewReader([]byte(body)))
	if err != nil || resp.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("compressed page altered: %v", err)
	}
	plain, _ := io.ReadAll(zr)
	if string(plain) != "<html><body>zipped</body></html>" {
		t.Errorf("gz body = %q", plain)
	}
	// Outside git: the banner still names the router and app.
	if _, body := fetch(t, addr, "/", htmlAccept); !strings.Contains(body, "v1 › web") || strings.Contains(body, "⎇") {
		t.Errorf("no-git banner: %s", body)
	}
}

func TestBanner_OffTextHTMLFile(t *testing.T) {
	addr := bannerSetup(t, vnet.Banner{Off: true}, t.TempDir(), page)
	if _, body := fetch(t, addr, "/", htmlAccept); strings.Contains(body, "sax-vnet-banner") {
		t.Error("banner: false still injected")
	}

	wt := gitWorktree(t)
	addr = bannerSetup(t, vnet.Banner{HTML: `<i class="mine">{branch}@{app}</i>`}, wt, page)
	if _, body := fetch(t, addr, "/", htmlAccept); !strings.Contains(body, `<i class="mine">feat/banner@web</i></body>`) {
		t.Errorf("html banner: %s", body)
	}

	file := filepath.Join(t.TempDir(), "banner.html")
	_ = os.WriteFile(file, []byte("<div id=custom>{worktree}</div>"), 0o644)
	addr = bannerSetup(t, vnet.Banner{File: file}, wt, page)
	if _, body := fetch(t, addr, "/", htmlAccept); !strings.Contains(body, "<div id=custom>repo-banner</div>") {
		t.Errorf("file banner: %s", body)
	}
	_ = os.WriteFile(file, []byte("<div id=custom>edited</div>"), 0o644)
	if _, body := fetch(t, addr, "/", htmlAccept); !strings.Contains(body, "<div id=custom>edited</div>") {
		t.Errorf("file edits should show on the next page load: %s", body)
	}
}
