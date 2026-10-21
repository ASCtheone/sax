package vnet

import (
	"strings"
	"testing"
)

func mustRouter(t *testing.T) Router {
	t.Helper()
	r, err := NewRouter("v1", "10.12.12.0/24", "lan")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestNewRouter_Validation(t *testing.T) {
	cases := []struct{ name, subnet, domain string }{
		{"", "10.12.12.0/24", "lan"},
		{"bad name", "10.12.12.0/24", "lan"},
		{"v1", "10.12.12.4/24", "lan"},
		{"v1", "10.12.12.0/24", "bad_domain"},
	}
	for _, c := range cases {
		if _, err := NewRouter(c.name, c.subnet, c.domain); err == nil {
			t.Errorf("NewRouter(%q, %q, %q) expected error", c.name, c.subnet, c.domain)
		}
	}
	if _, err := NewRouter("v1", "10.12.12.0/24", ""); err != nil {
		t.Errorf("empty domain should be allowed: %v", err)
	}
}

func TestWithSession_AutoAndExplicit(t *testing.T) {
	r0 := mustRouter(t)

	r1, ip, err := r0.WithSession("api", "")
	if err != nil || ip != "10.12.12.2" {
		t.Fatalf("auto ip = %q, %v; want 10.12.12.2", ip, err)
	}
	r2, ip, err := r1.WithSession("db", "10.12.12.9")
	if err != nil || ip != "10.12.12.9" {
		t.Fatalf("explicit ip = %q, %v", ip, err)
	}
	r3, ip, err := r2.WithSession("web", "")
	if err != nil || ip != "10.12.12.3" {
		t.Fatalf("next auto ip = %q, %v; want 10.12.12.3", ip, err)
	}

	// Immutability: earlier values are untouched.
	if len(r0.Sessions) != 0 || len(r1.Sessions) != 1 || len(r2.Sessions) != 2 {
		t.Errorf("WithSession mutated its receiver: %d %d %d", len(r0.Sessions), len(r1.Sessions), len(r2.Sessions))
	}

	// Re-adding a session keeps its address (idempotent restart).
	if _, ip, _ := r3.WithSession("api", ""); ip != "10.12.12.2" {
		t.Errorf("re-add api got %s, want stable 10.12.12.2", ip)
	}
}

func TestWithSession_Errors(t *testing.T) {
	r, _, _ := mustRouter(t).WithSession("api", "10.12.12.4")
	cases := map[string][2]string{
		"taken ip":      {"web", "10.12.12.4"},
		"outside":       {"web", "10.12.13.4"},
		"gateway":       {"web", "10.12.12.1"},
		"broadcast":     {"web", "10.12.12.255"},
		"bad name":      {"we b", ""},
		"garbage ip":    {"web", "nope"},
		"move existing": {"api", "10.12.12.5"},
	}
	for label, c := range cases {
		if _, _, err := r.WithSession(c[0], c[1]); err == nil {
			t.Errorf("%s: expected error", label)
		}
	}
}

func TestWithSession_Exhaustion(t *testing.T) {
	r, err := NewRouter("tiny", "10.0.0.0/30", "") // hosts .1 (gateway) .2
	if err != nil {
		t.Fatal(err)
	}
	r, _, err = r.WithSession("a", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.WithSession("b", ""); err == nil {
		t.Error("expected exhaustion error")
	}
}

func TestWithRule_AllocatesVIPFromTop(t *testing.T) {
	r, err := mustRouter(t).WithRule("bob.lan/api/* -> api:3200")
	if err != nil {
		t.Fatal(err)
	}
	r, err = r.WithRule("bob.lan/* -> web:4200")
	if err != nil {
		t.Fatal(err)
	}
	r, err = r.WithRule("db.lan:5432 -> db:5432")
	if err != nil {
		t.Fatal(err)
	}
	if r.VIPs["bob.lan"] != "10.12.12.254" || r.VIPs["db.lan"] != "10.12.12.253" {
		t.Errorf("VIPs = %v", r.VIPs)
	}
	if len(r.Rules) != 3 || r.Rules[0] != "bob.lan/api/* -> api:3200" {
		t.Errorf("rules = %v", r.Rules)
	}
}

func TestWithRule_RejectsConflicts(t *testing.T) {
	r, _ := mustRouter(t).WithRule("bob.lan/* -> web:4200")
	if _, err := r.WithRule("bob.lan -> api:3200"); err == nil {
		t.Error("expected conflict with HTTP routes on :80")
	}
	if _, err := r.WithRule("garbage"); err == nil {
		t.Error("expected parse error")
	}
}

func TestWithoutRule_ReleasesVIP(t *testing.T) {
	r, _ := mustRouter(t).WithRule("bob.lan/api/* -> api:3200")
	r, _ = r.WithRule("bob.lan/* -> web:4200")

	r2, err := r.WithoutRule("bob.lan/api/*")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r2.VIPs["bob.lan"]; !ok {
		t.Error("VIP released while another rule still uses the host")
	}
	r3, err := r2.WithoutRule("bob.lan/*")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r3.VIPs["bob.lan"]; ok {
		t.Error("VIP should be released with the last rule")
	}
	if _, err := r3.WithoutRule("nope.lan/*"); err == nil {
		t.Error("expected not-found error")
	}
	if len(r.Rules) != 2 {
		t.Error("WithoutRule mutated its receiver")
	}
}

func TestWithoutSession(t *testing.T) {
	r, _, _ := mustRouter(t).WithSession("api", "")
	r2 := r.WithoutSession("api")
	if _, ok := r2.Sessions["api"]; ok {
		t.Error("session not removed")
	}
	if _, ok := r.Sessions["api"]; !ok {
		t.Error("WithoutSession mutated its receiver")
	}
}

func TestTables(t *testing.T) {
	r := mustRouter(t)
	r, _, _ = r.WithSession("api", "10.12.12.4")
	r, _, _ = r.WithSession("web", "10.12.12.5")
	r, _ = r.WithRule("bob.lan/api/* -> api:3200")
	r, _ = r.WithRule("bob.lan/* -> web:4200")
	r, _ = r.WithRule("db.lan:5432 -> db:5432")          // db not running: warning
	r, _ = r.WithRule("raw.lan:9000 -> 10.12.12.4:9000") // literal IP target

	tables, warnings := r.Tables()

	names := tables.NamesFile()
	for _, want := range []string{
		"api 10.12.12.4\n", "web 10.12.12.5\n",
		"bob.lan 10.12.12.254\n", "db.lan 10.12.12.253\n", "raw.lan 10.12.12.252\n",
	} {
		if !strings.Contains(names, want) {
			t.Errorf("names file missing %q:\n%s", want, names)
		}
	}
	if strings.Contains(names, "api.lan") {
		t.Errorf("the domain is for routes only, apps get no <app>.<domain> name:\n%s", names)
	}

	svc := tables.ServicesFile()
	if !strings.Contains(svc, "10.12.12.252 9000 10.12.12.4 9000\n") {
		t.Errorf("services file missing raw.lan entry:\n%s", svc)
	}
	if strings.Contains(svc, "10.12.12.253") {
		t.Errorf("unresolved db target should not be rendered:\n%s", svc)
	}

	if len(tables.HTTP) != 1 || tables.HTTP[0].Host != "bob.lan" || tables.HTTP[0].VIP != "10.12.12.254" {
		t.Errorf("HTTP hosts = %+v", tables.HTTP)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "db") {
		t.Errorf("warnings = %v", warnings)
	}
}

func TestTables_ShadowWarning(t *testing.T) {
	r := mustRouter(t)
	r, _ = r.WithRule("bob.lan/* -> web:4200")
	r, _ = r.WithRule("bob.lan/api/* -> api:3200")
	_, warnings := r.Tables()
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "never matches") && strings.Contains(w, "/api/*") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected shadow warning, got %v", warnings)
	}
}

// A route hostname without a dot gets the router's domain (default lan);
// a full hostname is kept as written.
func TestWithRule_Domain(t *testing.T) {
	r, err := NewRouter("v1", "10.12.12.0/24", "")
	if err != nil {
		t.Fatal(err)
	}
	if r.Domain != DefaultDomain {
		t.Errorf("default domain = %q, want %q", r.Domain, DefaultDomain)
	}
	r, _, _ = r.WithSession("api", "")
	for raw, want := range map[string]string{
		"bob/api/* -> api:3200":     "bob.lan/api/* -> api:3200",
		"db:5432 -> api:5432":       "db.lan:5432 -> api:5432",
		"shop.test/* -> api:3200":   "shop.test/* -> api:3200",
		"admin.lan/* -> api:3200":   "admin.lan/* -> api:3200",
	} {
		next, err := r.WithRule(raw)
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		if got := next.Rules[len(next.Rules)-1]; got != want {
			t.Errorf("%s -> %s, want %s", raw, got, want)
		}
	}
	two, _ := NewRouter("v2", "10.12.12.0/24", "lan2")
	two, _ = two.WithRule("bob/* -> 10.12.12.4:80")
	if two.Rules[0] != "bob.lan2/* -> 10.12.12.4:80" {
		t.Errorf("custom domain: %s", two.Rules[0])
	}
}
