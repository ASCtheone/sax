package vnet

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ConfigDoc edits an env file as a YAML tree, so comments, key order and
// formatting the user wrote survive `sax net project ...` edits. Callers
// validate the result (LoadConfig + Plan) before writing it.
type ConfigDoc struct {
	root *yaml.Node // the top-level mapping
	doc  *yaml.Node
}

// ProjectFields are a project's own keys; nil leaves a key as it is and ""
// removes it.
type ProjectFields struct {
	Dir, Router, Subnet, Domain *string
}

// AppFields are an app's keys; nil leaves a key as it is and "" removes it.
// Cmd, NX and NPM are how the app starts: give at most one, and it replaces
// the others. Env entries are merged; UnsetEnv removes variables.
type AppFields struct {
	Cmd, NX, NPM *string
	Cwd, IP      *string
	Env          map[string]string
	UnsetEnv     []string
}

// ParseConfigDoc parses an env file for editing; empty data starts a
// projects file.
func ParseConfigDoc(data []byte) (*ConfigDoc, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse env file: %w", err)
	}
	if doc.Kind == 0 { // empty file
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("env file: want a mapping at the top")
	}
	return &ConfigDoc{root: doc.Content[0], doc: &doc}, nil
}

// Bytes encodes the edited document.
func (d *ConfigDoc) Bytes() ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(d.doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// projects returns the projects mapping, creating it in an empty file.
func (d *ConfigDoc) projects(create bool) (*yaml.Node, error) {
	if p := mapGet(d.root, "projects"); p != nil {
		if p.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("env file: projects is not a mapping")
		}
		return p, nil
	}
	if mapGet(d.root, "router") != nil {
		return nil, nil // a single-project file
	}
	if !create {
		return nil, nil
	}
	p := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	mapSet(d.root, "projects", p)
	return p, nil
}

// project returns a project's mapping: an entry of projects, or the whole
// file for a single-project file whose router is name.
func (d *ConfigDoc) project(name string) (*yaml.Node, error) {
	ps, err := d.projects(false)
	if err != nil {
		return nil, err
	}
	if ps != nil {
		if p := mapGet(ps, name); p != nil {
			return p, nil
		}
	} else if r := mapGet(d.root, "router"); r != nil && r.Value == name {
		return d.root, nil
	}
	return nil, fmt.Errorf("no project %q in this file", name)
}

// ProjectNames lists the file's projects.
func (d *ConfigDoc) ProjectNames() []string {
	ps, _ := d.projects(false)
	if ps == nil {
		if r := mapGet(d.root, "router"); r != nil {
			return []string{r.Value}
		}
		return nil
	}
	var names []string
	for i := 0; i+1 < len(ps.Content); i += 2 {
		names = append(names, ps.Content[i].Value)
	}
	sort.Strings(names)
	return names
}

// AddProject adds a project; it must not exist yet.
func (d *ConfigDoc) AddProject(name string, f ProjectFields) error {
	if err := ValidateName("project", name); err != nil {
		return err
	}
	ps, err := d.projects(true)
	if err != nil {
		return err
	}
	if ps == nil {
		return fmt.Errorf("this is a single-project file (router: ...); add projects to ~/.sax.net or a projects file")
	}
	if mapGet(ps, name) != nil {
		return fmt.Errorf("project %q already exists", name)
	}
	p := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	mapSet(ps, name, p)
	setProjectFields(p, f)
	return nil
}

// SetProject changes a project's own keys.
func (d *ConfigDoc) SetProject(name string, f ProjectFields) error {
	p, err := d.project(name)
	if err != nil {
		return err
	}
	setProjectFields(p, f)
	return nil
}

// RemoveProject removes a project of a projects file.
func (d *ConfigDoc) RemoveProject(name string) error {
	ps, err := d.projects(false)
	if err != nil {
		return err
	}
	if ps == nil || !mapDel(ps, name) {
		return fmt.Errorf("no project %q in this file", name)
	}
	return nil
}

func setProjectFields(p *yaml.Node, f ProjectFields) {
	for _, kv := range []struct {
		key string
		val *string
	}{{"router", f.Router}, {"subnet", f.Subnet}, {"domain", f.Domain}, {"dir", f.Dir}} {
		setScalar(p, kv.key, kv.val)
	}
}

// SetApp adds (create) or changes (!create) an app of a project.
func (d *ConfigDoc) SetApp(project, app string, f AppFields, create bool) error {
	if err := ValidateName("app", app); err != nil {
		return err
	}
	p, err := d.project(project)
	if err != nil {
		return err
	}
	sessions := mapGet(p, "sessions")
	if sessions == nil {
		sessions = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		mapSet(p, "sessions", sessions)
	}
	a := mapGet(sessions, app)
	switch {
	case a == nil && !create:
		return fmt.Errorf("no app %q in project %s", app, project)
	case a != nil && create:
		return fmt.Errorf("app %q already exists in project %s (use: sax net project app set)", app, project)
	}
	runs := map[string]*string{"cmd": f.Cmd, "nx": f.NX, "npm": f.NPM}
	var given string
	for _, kind := range RunKinds {
		if v := runs[kind]; v != nil && *v != "" {
			if given != "" {
				return fmt.Errorf("app %s: use only one of --cmd, --nx or --npm", app)
			}
			given = kind
		}
	}
	if a == nil {
		if given == "" {
			return fmt.Errorf("app %s: say how it starts: --cmd \"...\", --nx <project[:target]> or --npm <script>", app)
		}
		a = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Style: siblingStyle(sessions)}
		mapSet(sessions, app, a)
	}
	if given != "" {
		for _, kind := range RunKinds {
			if kind == given {
				setScalar(a, kind, runs[kind])
			} else {
				mapDel(a, kind)
			}
		}
	}
	setScalar(a, "cwd", f.Cwd)
	setScalar(a, "ip", f.IP)
	if len(f.Env) > 0 || len(f.UnsetEnv) > 0 {
		env := mapGet(a, "env")
		if env == nil {
			env = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Style: yaml.FlowStyle}
			mapSet(a, "env", env)
		}
		keys := make([]string, 0, len(f.Env))
		for k := range f.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := f.Env[k]
			setScalar(env, k, &v)
		}
		for _, k := range f.UnsetEnv {
			mapDel(env, k)
		}
		if len(env.Content) == 0 {
			mapDel(a, "env")
		}
	}
	for _, kind := range RunKinds {
		if v := mapGet(a, kind); v != nil && v.Value != "" {
			return nil
		}
	}
	return fmt.Errorf("app %s: cmd, nx or npm cannot be removed", app)
}

// RemoveApp removes an app and the routes and ports that point at it,
// returning those.
func (d *ConfigDoc) RemoveApp(project, app string) ([]string, error) {
	p, err := d.project(project)
	if err != nil {
		return nil, err
	}
	sessions := mapGet(p, "sessions")
	if sessions == nil || !mapDel(sessions, app) {
		return nil, fmt.Errorf("no app %q in project %s", app, project)
	}
	var dropped []string
	for _, key := range []string{"routes", "publish"} {
		dropped = append(dropped, filterSeq(p, key, func(entry string) bool {
			_, target, ok := strings.Cut(entry, ruleArrow)
			host, _, _ := strings.Cut(strings.TrimSpace(target), ":")
			return ok && host == app
		})...)
	}
	return dropped, nil
}

// AddRoute inserts "pattern -> target" at position at (1-based; 0 appends).
// A route with the same pattern is an error: patterns are unique.
func (d *ConfigDoc) AddRoute(project, pattern, target string, at int) (string, error) {
	rule, err := NewRule(pattern, target)
	if err != nil {
		return "", err
	}
	p, err := d.project(project)
	if err != nil {
		return "", err
	}
	routes := seq(p, "routes")
	for _, n := range routes.Content {
		if r, err := ParseRule(n.Value); err == nil && r.Pattern() == rule.Pattern() {
			return "", fmt.Errorf("project %s already routes %s (%s)", project, rule.Pattern(), n.Value)
		}
	}
	entry := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: rule.String()}
	if at <= 0 || at > len(routes.Content) {
		routes.Content = append(routes.Content, entry)
	} else {
		routes.Content = append(routes.Content[:at-1], append([]*yaml.Node{entry}, routes.Content[at-1:]...)...)
	}
	return rule.String(), nil
}

// RemoveRoute removes the route with pattern.
func (d *ConfigDoc) RemoveRoute(project, pattern string) error {
	want, err := NewRule(pattern, "x:1")
	if err != nil {
		return err
	}
	p, err := d.project(project)
	if err != nil {
		return err
	}
	if len(filterSeq(p, "routes", func(entry string) bool {
		r, err := ParseRule(entry)
		return err == nil && r.Pattern() == want.Pattern()
	})) == 0 {
		return fmt.Errorf("project %s has no route %s", project, want.Pattern())
	}
	return nil
}

// AddPublish adds "hostPort -> target"; host ports are unique.
func (d *ConfigDoc) AddPublish(project, hostPort, target string) (string, error) {
	pub, err := ParsePublish(hostPort + " " + ruleArrow + " " + target)
	if err != nil {
		return "", err
	}
	p, err := d.project(project)
	if err != nil {
		return "", err
	}
	list := seq(p, "publish")
	for _, n := range list.Content {
		if cur, err := ParsePublish(n.Value); err == nil && cur.HostPort == pub.HostPort {
			return "", fmt.Errorf("project %s already publishes port %d (%s)", project, pub.HostPort, n.Value)
		}
	}
	list.Content = append(list.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: pub.String()})
	return pub.String(), nil
}

// RemovePublish removes the publish of hostPort.
func (d *ConfigDoc) RemovePublish(project, hostPort string) error {
	port, err := parsePort(hostPort)
	if err != nil {
		return err
	}
	p, err := d.project(project)
	if err != nil {
		return err
	}
	if len(filterSeq(p, "publish", func(entry string) bool {
		cur, err := ParsePublish(entry)
		return err == nil && cur.HostPort == port
	})) == 0 {
		return fmt.Errorf("project %s does not publish port %d", project, port)
	}
	return nil
}

// --- yaml.Node helpers ---

func mapGet(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// mapSet replaces key's value (keeping the key and its comments) or
// appends the pair.
func mapSet(m *yaml.Node, key string, val *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = val
			return
		}
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, val)
}

func mapDel(m *yaml.Node, key string) bool {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return true
		}
	}
	return false
}

// setScalar sets key to *val, removes it for "", and leaves it for nil.
func setScalar(m *yaml.Node, key string, val *string) {
	switch {
	case val == nil:
	case *val == "":
		mapDel(m, key)
	default:
		if cur := mapGet(m, key); cur != nil && cur.Kind == yaml.ScalarNode {
			cur.Value, cur.Tag, cur.Style = *val, "!!str", 0 // keeps its comments
			return
		}
		mapSet(m, key, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: *val})
	}
}

// seq returns the sequence under key, creating it.
func seq(m *yaml.Node, key string) *yaml.Node {
	if s := mapGet(m, key); s != nil && s.Kind == yaml.SequenceNode {
		return s
	}
	s := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	mapSet(m, key, s)
	return s
}

// filterSeq removes the entries of the sequence under key that drop
// matches, returning them; an emptied sequence is removed.
func filterSeq(m *yaml.Node, key string, drop func(string) bool) []string {
	s := mapGet(m, key)
	if s == nil || s.Kind != yaml.SequenceNode {
		return nil
	}
	var kept []*yaml.Node
	var dropped []string
	for _, n := range s.Content {
		if drop(n.Value) {
			dropped = append(dropped, n.Value)
		} else {
			kept = append(kept, n)
		}
	}
	s.Content = kept
	if len(kept) == 0 {
		mapDel(m, key)
	}
	return dropped
}

// siblingStyle is the style of the mapping's first value (so a new app is
// written like the others: { cmd: ... } or block).
func siblingStyle(m *yaml.Node) yaml.Style {
	if len(m.Content) >= 2 {
		return m.Content[1].Style
	}
	return 0
}
