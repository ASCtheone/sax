package vnet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func parseBanner(t *testing.T, src string) Banner {
	t.Helper()
	var doc struct {
		Banner Banner `yaml:"banner"`
	}
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatalf("%q: %v", src, err)
	}
	return doc.Banner
}

func TestBanner_YAMLForms(t *testing.T) {
	if b := parseBanner(t, "other: 1\n"); b.Off || b.Text != "" || b.HTML != "" || b.File != "" {
		t.Errorf("unset = %+v, want the default", b)
	}
	if b := parseBanner(t, "banner: false\n"); !b.Off {
		t.Errorf("false = %+v", b)
	}
	if b := parseBanner(t, "banner: true\n"); b.Off {
		t.Errorf("true = %+v", b)
	}
	if b := parseBanner(t, "banner: \"{branch} on {app}\"\n"); b.Text != "{branch} on {app}" {
		t.Errorf("text = %+v", b)
	}
	if b := parseBanner(t, "banner:\n  html: \"<b>{branch}</b>\"\n"); b.HTML != "<b>{branch}</b>" {
		t.Errorf("html = %+v", b)
	}
	if b := parseBanner(t, "banner:\n  file: ./banner.html\n"); b.File != "./banner.html" {
		t.Errorf("file = %+v", b)
	}
	for _, bad := range []string{"banner:\n  html: a\n  file: b\n", "banner:\n  bogus: x\n", "banner: [1]\n"} {
		var doc struct {
			Banner Banner `yaml:"banner"`
		}
		if err := yaml.Unmarshal([]byte(bad), &doc); err == nil {
			t.Errorf("%q should fail", bad)
		}
	}
}

var vars = BannerVars{
	Branch: "feat/login", Commit: "a1b2c3d", Dirty: true, Worktree: "sax-login",
	WorktreePath: "/w/sax-login", Router: "v2", App: "web", Host: "bob.lan", IP: "10.12.12.3", Dir: "/w/sax-login/web",
}

func TestBanner_RenderDefault(t *testing.T) {
	out, err := Banner{}.Render(vars)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`id="sax-vnet-banner"`, "feat/login*", "a1b2c3d", "sax-login", "v2 › web", "position:fixed"} {
		if !strings.Contains(out, want) {
			t.Errorf("default banner missing %q:\n%s", want, out)
		}
	}
	// Missing git info (not a repo) leaves no empty separators.
	plain, _ := Banner{}.Render(BannerVars{Router: "v1", App: "api"})
	if strings.Contains(plain, "·  ·") || !strings.Contains(plain, "v1 › api") || strings.Contains(plain, "⎇") {
		t.Errorf("no-git banner = %s", plain)
	}
	if out, _ := (Banner{Off: true}).Render(vars); out != "" {
		t.Errorf("off = %q", out)
	}
}

func TestBanner_RenderTextEscapes(t *testing.T) {
	v := vars
	v.Branch = "<script>x</script>"
	out, err := Banner{Text: "branch {branch} & app {app}"}.Render(v)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "<script>x") || !strings.Contains(out, "&lt;script&gt;") || !strings.Contains(out, "branch ") ||
		!strings.Contains(out, "&amp; app web") || !strings.Contains(out, `id="sax-vnet-banner"`) {
		t.Errorf("text banner:\n%s", out)
	}
}

func TestBanner_RenderHTMLAndFile(t *testing.T) {
	v := vars
	v.Branch = "a<b"
	out, err := Banner{HTML: `<div class="x">{branch} {worktree}</div>`}.Render(v)
	if err != nil || out != `<div class="x">a&lt;b sax-login</div>` {
		t.Errorf("html = %q, %v", out, err)
	}

	path := filepath.Join(t.TempDir(), "banner.html")
	_ = os.WriteFile(path, []byte("<style>.b{}</style><div class=b>{app}@{ip}</div>"), 0o644)
	out, err = Banner{File: path}.Render(vars)
	if err != nil || out != "<style>.b{}</style><div class=b>web@10.12.12.3</div>" {
		t.Errorf("file = %q, %v", out, err)
	}
	if _, err := (Banner{File: filepath.Join(t.TempDir(), "missing.html")}).Render(vars); err == nil {
		t.Error("missing file should be an error")
	}
}

func TestInjectBanner(t *testing.T) {
	cases := map[string]string{
		"<html><body><p>x</p></body></html>":    "<html><body><p>x</p>[B]</body></html>",
		"<HTML><BODY>x</BODY></HTML>":           "<HTML><BODY>x[B]</BODY></HTML>",
		"<p>no body tag</p>":                    "<p>no body tag</p>[B]",
		"<body>a</body><!-- </body> --></html>": "<body>a</body><!-- [B]</body> --></html>",
	}
	for in, want := range cases {
		if got := string(InjectBanner([]byte(in), "[B]")); got != want {
			t.Errorf("InjectBanner(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEnvFile_BannerAndDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, EnvFileName)
	src := "router: v1\nsubnet: 10.0.0.0/24\nsessions:\n  web: {cwd: ./web, cmd: x}\nbanner:\n  file: ./b.html\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	e, err := LoadEnvFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if e.Banner.File != filepath.Join(dir, "b.html") {
		t.Errorf("banner file = %q (should be resolved next to the env file)", e.Banner.File)
	}
	r, _, err := e.Plan()
	if err != nil {
		t.Fatal(err)
	}
	if r.Banner.File == "" || r.Dirs["web"] != filepath.Join(dir, "web") {
		t.Errorf("plan banner %+v dirs %v", r.Banner, r.Dirs)
	}
	if r2 := r.WithoutSession("web"); r2.Dirs["web"] != "" || r.Dirs["web"] == "" {
		t.Error("WithoutSession should drop the dir without touching the original")
	}
}
