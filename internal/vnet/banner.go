package vnet

import (
	"bytes"
	"fmt"
	"html"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Banner configures the banner the gateway adds to HTML pages it serves, so
// you can tell which branch, worktree and router a tab is showing. In
// .sax.lan:
//
//	banner: false                       off
//	banner: "{branch} on {app}"         text, shown in the default pill
//	banner: {html: "<b>{branch}</b>"}   an HTML snippet
//	banner: {file: ./banner.html}       a full HTML file (re-read on change)
//
// Unset means the default pill. Placeholders: {branch} {commit} {dirty}
// {worktree} {worktree_path} {router} {app} {host} {ip} {dir}; their values
// are HTML-escaped.
type Banner struct {
	Off  bool   `json:"off,omitempty"`
	Text string `json:"text,omitempty"`
	HTML string `json:"html,omitempty"`
	File string `json:"file,omitempty"`
}

// BannerVars are the values a banner can show.
type BannerVars struct {
	Branch       string
	Commit       string
	Dirty        bool
	Worktree     string // name of the checkout's folder
	WorktreePath string
	Router       string
	App          string
	Host         string
	IP           string
	Dir          string // the app's folder
}

// UnmarshalYAML accepts false/true, a string, or a map with html or file.
func (b *Banner) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		if n.Tag == "!!bool" {
			var on bool
			if err := n.Decode(&on); err != nil {
				return err
			}
			*b = Banner{Off: !on}
			return nil
		}
		*b = Banner{Text: n.Value}
		return nil
	case yaml.MappingNode:
		var m struct {
			HTML string `yaml:"html"`
			File string `yaml:"file"`
		}
		dec := map[string]string{}
		if err := n.Decode(&dec); err != nil {
			return fmt.Errorf("banner: %w", err)
		}
		for k := range dec {
			if k != "html" && k != "file" {
				return fmt.Errorf("banner: unknown key %q (want html or file)", k)
			}
		}
		m.HTML, m.File = dec["html"], dec["file"]
		if (m.HTML == "") == (m.File == "") {
			return fmt.Errorf("banner: set exactly one of html or file")
		}
		*b = Banner{HTML: m.HTML, File: m.File}
		return nil
	}
	return fmt.Errorf("banner: want false, a text, or {html: ...} / {file: ...}")
}

const pillStyle = "position:fixed;bottom:8px;right:8px;z-index:2147483647;" +
	"font:12px/1.4 ui-monospace,SFMono-Regular,Menlo,monospace;background:rgba(17,17,17,.88);" +
	"color:#f5f5f5;padding:4px 10px;border-radius:999px;box-shadow:0 2px 8px rgba(0,0,0,.35);" +
	"pointer-events:auto;user-select:text"

// Render returns the banner HTML for a page ("" when off).
func (b Banner) Render(v BannerVars) (string, error) {
	switch {
	case b.Off:
		return "", nil
	case b.File != "":
		data, err := os.ReadFile(b.File)
		if err != nil {
			return "", fmt.Errorf("banner file: %w", err)
		}
		return fill(string(data), v), nil
	case b.HTML != "":
		return fill(b.HTML, v), nil
	case b.Text != "":
		return pill(fill(html.EscapeString(b.Text), v)), nil
	}
	return pill(defaultText(v)), nil
}

func pill(inner string) string {
	return `<div id="sax-vnet-banner" style="` + pillStyle + `">` + inner +
		`<span title="hide" style="cursor:pointer;margin-left:8px;opacity:.7" ` +
		`onclick="this.parentNode.remove()">×</span></div>`
}

// defaultText is "⎇ branch* · commit · worktree · router › app", leaving out
// what is unknown (e.g. outside a git repository).
func defaultText(v BannerVars) string {
	var parts []string
	if v.Branch != "" {
		branch := "⎇ " + html.EscapeString(v.Branch)
		if v.Dirty {
			branch += "*"
		}
		parts = append(parts, branch)
	}
	for _, s := range []string{v.Commit, v.Worktree} {
		if s != "" {
			parts = append(parts, html.EscapeString(s))
		}
	}
	parts = append(parts, html.EscapeString(v.Router)+" › "+html.EscapeString(v.App))
	return strings.Join(parts, " · ")
}

// fill replaces placeholders with HTML-escaped values.
func fill(tmpl string, v BannerVars) string {
	dirty := ""
	if v.Dirty {
		dirty = "*"
	}
	esc := html.EscapeString
	return strings.NewReplacer(
		"{branch}", esc(v.Branch), "{commit}", esc(v.Commit), "{dirty}", dirty,
		"{worktree}", esc(v.Worktree), "{worktree_path}", esc(v.WorktreePath),
		"{router}", esc(v.Router), "{app}", esc(v.App), "{host}", esc(v.Host),
		"{ip}", esc(v.IP), "{dir}", esc(v.Dir),
	).Replace(tmpl)
}

// InjectBanner inserts the banner before the page's last </body>, or at the
// end when there is none.
func InjectBanner(page []byte, banner string) []byte {
	i := bytes.LastIndex(bytes.ToLower(page), []byte("</body>"))
	if i < 0 {
		return append(append([]byte{}, page...), banner...)
	}
	out := make([]byte, 0, len(page)+len(banner))
	out = append(out, page[:i]...)
	out = append(out, banner...)
	return append(out, page[i:]...)
}
