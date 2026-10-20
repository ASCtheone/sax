package vnet

import (
	"strings"
	"testing"
)

func mustRules(t *testing.T, raw ...string) []Rule {
	t.Helper()
	rules := make([]Rule, 0, len(raw))
	for _, s := range raw {
		r, err := ParseRule(s)
		if err != nil {
			t.Fatalf("ParseRule(%q): %v", s, err)
		}
		rules = append(rules, r)
	}
	return rules
}

func TestParseRule_HTTP(t *testing.T) {
	r, err := ParseRule("Bob.lan/api/*  ->  api:3200")
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != KindHTTP || r.Host != "bob.lan" || r.Path != "/api/*" || r.Port != 80 {
		t.Errorf("got %+v", r)
	}
	if r.Target != (Endpoint{Host: "api", Port: 3200}) {
		t.Errorf("target = %+v", r.Target)
	}
	if r.String() != "bob.lan/api/* -> api:3200" {
		t.Errorf("String() = %q", r.String())
	}
}

func TestParseRule_TCP(t *testing.T) {
	r, err := ParseRule("db.lan:5432 -> 10.12.12.9:5432")
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != KindTCP || r.Host != "db.lan" || r.Port != 5432 || r.Path != "" {
		t.Errorf("got %+v", r)
	}
	bare, err := ParseRule("bob.lan -> api:3200")
	if err != nil {
		t.Fatal(err)
	}
	if bare.Kind != KindTCP || bare.Port != 80 {
		t.Errorf("bare host should be TCP :80, got %+v", bare)
	}
}

func TestParseRule_Errors(t *testing.T) {
	cases := map[string]string{
		"bob.lan/api/*":              "->",
		"bob.lan/api/* -> api":       "port",
		"bob.lan/api/* -> api:0":     "port",
		"bob.lan/api/* -> api:99999": "port",
		"bob.lan:8080/api -> api:1":  "port 80",
		"*.lan/x -> api:1":           "hostname",
		"bob..lan -> api:1":          "hostname",
		" -> api:1":                  "hostname",
		"bob.lan/api -> -bad-:1":     "target",
		"bob.lan:abc -> api:1":       "port",
	}
	for in, wantSub := range cases {
		_, err := ParseRule(in)
		if err == nil {
			t.Errorf("ParseRule(%q) expected error", in)
			continue
		}
		if !strings.Contains(err.Error(), wantSub) {
			t.Errorf("ParseRule(%q) error %q should mention %q", in, err, wantSub)
		}
	}
}

func TestGlobMatch(t *testing.T) {
	cases := []struct {
		pattern, s string
		want       bool
	}{
		{"/api/*", "/api/users", true},
		{"/api/*", "/api/users/42", true}, // * crosses '/'
		{"/api/*", "/api/", true},         // * matches empty
		{"/api/*", "/api", false},         // Cloudflare: no implicit bare match
		{"/api/*", "/apix/users", false},
		{"/*", "/", true},
		{"/*", "/anything/at/all", true},
		{"/static/*.js", "/static/a/b.js", true},
		{"/static/*.js", "/static/a/b.css", false},
		{"/exact", "/exact", true},
		{"/exact", "/exact/", false},
		{"/*/v2/*", "/svc/v2/x", true},
	}
	for _, c := range cases {
		if got := globMatch(c.pattern, c.s); got != c.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", c.pattern, c.s, got, c.want)
		}
	}
}

func TestMatchHTTP_FirstMatchWins(t *testing.T) {
	rules := mustRules(t,
		"bob.lan/api/* -> api:3200",
		"bob.lan/* -> web:4200",
		"other.lan/* -> other:9000",
	)
	cases := map[string]string{
		"bob.lan /api/users":      "api:3200",
		"bob.lan /api/users?id=1": "api:3200", // query ignored for matching
		"BOB.LAN /api/x":          "api:3200", // host is case-insensitive
		"bob.lan /":               "web:4200",
		"bob.lan /api":            "web:4200", // bare /api falls through
		"bob.lan:80 /dashboard":   "web:4200", // port in Host header ignored
		"other.lan /x":            "other:9000",
	}
	for in, want := range cases {
		host, path, _ := strings.Cut(in, " ")
		r, ok := MatchHTTP(rules, host, path)
		if !ok {
			t.Errorf("MatchHTTP(%s) no match, want %s", in, want)
			continue
		}
		if r.Target.String() != want {
			t.Errorf("MatchHTTP(%s) = %s, want %s", in, r.Target, want)
		}
	}
	if _, ok := MatchHTTP(rules, "nobody.lan", "/"); ok {
		t.Error("unknown host should not match")
	}

	// Order matters: the catch-all first swallows everything.
	reversed := mustRules(t, "bob.lan/* -> web:4200", "bob.lan/api/* -> api:3200")
	if r, _ := MatchHTTP(reversed, "bob.lan", "/api/users"); r.Target.Host != "web" {
		t.Errorf("first rule must win, got %s", r.Target)
	}
}

func TestShadowed(t *testing.T) {
	rules := mustRules(t,
		"bob.lan/* -> web:4200",
		"bob.lan/api/* -> api:3200",   // shadowed by /*
		"bob.lan/x -> api:3200",       // shadowed by /*
		"other.lan/api/* -> api:3200", // different host: fine
		"other.lan/api/v2/* -> v2:1",  // shadowed by /api/*
		"other.lan/apix -> api:1",     // not covered by /api/*
	)
	got := Shadowed(rules)
	want := map[int]int{1: 0, 2: 0, 4: 3}
	if len(got) != len(want) {
		t.Fatalf("Shadowed = %+v, want %d entries", got, len(want))
	}
	for _, s := range got {
		if want[s.Index] != s.By {
			t.Errorf("rule %d shadowed by %d, want %d", s.Index, s.By, want[s.Index])
		}
	}

	ok := mustRules(t, "bob.lan/api/* -> api:3200", "bob.lan/* -> web:4200")
	if s := Shadowed(ok); len(s) != 0 {
		t.Errorf("correct order reported shadowed: %+v", s)
	}
}

func TestCheckConflicts(t *testing.T) {
	if err := CheckConflicts(mustRules(t, "db.lan:5432 -> db:5432", "db.lan:5432 -> db2:5432")); err == nil {
		t.Error("duplicate TCP host:port should conflict")
	}
	if err := CheckConflicts(mustRules(t, "bob.lan/* -> web:4200", "bob.lan -> api:3200")); err == nil {
		t.Error("TCP :80 on an HTTP-routed host should conflict")
	}
	if err := CheckConflicts(mustRules(t, "bob.lan/* -> web:4200", "bob.lan:5432 -> db:5432")); err != nil {
		t.Errorf("HTTP + TCP on another port should be fine: %v", err)
	}
}
