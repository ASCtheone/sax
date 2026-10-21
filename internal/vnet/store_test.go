package vnet

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestStore_CreateGetListRemove(t *testing.T) {
	s := NewStore(t.TempDir())

	if routers, err := s.List(); err != nil || len(routers) != 0 {
		t.Fatalf("empty List = %v, %v", routers, err)
	}

	r := mustRouter(t)
	if err := s.Create(r); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(r); !errors.Is(err, ErrExists) {
		t.Errorf("second Create err = %v, want ErrExists", err)
	}

	// Same subnet in another router is allowed: routers are isolated.
	r2, _ := NewRouter("v2", "10.12.12.0/24", "lan")
	if err := s.Create(r2); err != nil {
		t.Fatalf("same subnet in another router: %v", err)
	}

	got, err := s.Get("v1")
	if err != nil || got.Subnet != "10.12.12.0/24" || got.Domain != "lan" {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	if _, err := s.Get("ghost"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(ghost) err = %v, want ErrNotFound", err)
	}

	routers, _ := s.List()
	if len(routers) != 2 || routers[0].Name != "v1" || routers[1].Name != "v2" {
		t.Errorf("List = %+v", routers)
	}

	if err := s.Remove("v1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.RouterDir("v1")); !os.IsNotExist(err) {
		t.Error("router dir not removed")
	}
	if err := s.Remove("v1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("second Remove err = %v", err)
	}
}

func TestStore_WritesShimTables(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.Create(mustRouter(t)); err != nil {
		t.Fatal(err)
	}
	_, err := s.Update("v1", func(r Router) (Router, error) {
		r, _, err := r.WithSession("api", "10.12.12.4")
		if err != nil {
			return r, err
		}
		return r.WithRule("raw.lan:9000 -> api:9000")
	})
	if err != nil {
		t.Fatal(err)
	}

	dir := s.RouterDir("v1")
	names, _ := os.ReadFile(filepath.Join(dir, "names"))
	if !strings.Contains(string(names), "api 10.12.12.4") || strings.Contains(string(names), "api.lan") {
		t.Errorf("names = %q", names)
	}
	svc, _ := os.ReadFile(filepath.Join(dir, "svc"))
	if string(svc) != "10.12.12.254 9000 10.12.12.4 9000\n" {
		t.Errorf("svc = %q", svc)
	}
	if fi, err := os.Stat(filepath.Join(dir, "ports")); err != nil || !fi.IsDir() {
		t.Error("ports dir missing")
	}
}

func TestStore_UpdateErrorLeavesStateUntouched(t *testing.T) {
	s := NewStore(t.TempDir())
	_ = s.Create(mustRouter(t))
	boom := errors.New("boom")
	if _, err := s.Update("v1", func(r Router) (Router, error) { return r, boom }); !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}
	if _, err := s.Update("ghost", func(r Router) (Router, error) { return r, nil }); !errors.Is(err, ErrNotFound) {
		t.Errorf("ghost err = %v", err)
	}
	got, _ := s.Get("v1")
	if len(got.Sessions) != 0 {
		t.Error("failed update changed state")
	}
}

func TestStore_ConcurrentUpdatesDoNotLoseWrites(t *testing.T) {
	s := NewStore(t.TempDir())
	_ = s.Create(mustRouter(t))

	const n = 20
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := "s" + string(rune('a'+i))
			if _, err := s.Update("v1", func(r Router) (Router, error) {
				r, _, err := r.WithSession(name, "")
				return r, err
			}); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()

	got, _ := s.Get("v1")
	if len(got.Sessions) != n {
		t.Errorf("sessions = %d, want %d (lost updates)", len(got.Sessions), n)
	}
	seen := map[string]bool{}
	for _, ip := range got.Sessions {
		if seen[ip] {
			t.Errorf("duplicate ip %s", ip)
		}
		seen[ip] = true
	}
}

func TestStore_RejectsBadNames(t *testing.T) {
	s := NewStore(t.TempDir())
	for _, name := range []string{"../escape", "a/b", ""} {
		if _, err := s.Get(name); err == nil {
			t.Errorf("Get(%q) should fail", name)
		}
		if err := s.Remove(name); err == nil {
			t.Errorf("Remove(%q) should fail", name)
		}
	}
}

func TestStore_GetRejectsCorruptRouter(t *testing.T) {
	s := NewStore(t.TempDir())
	_ = s.Create(mustRouter(t))
	path := filepath.Join(s.RouterDir("v1"), routerFile)
	if err := os.WriteFile(path, []byte(`{"name":"v1","subnet":"garbage"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("v1"); err == nil || !strings.Contains(err.Error(), "corrupt") {
		t.Errorf("err = %v, want corrupt error", err)
	}
	if err := os.WriteFile(path, []byte(`{not json`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("v1"); err == nil {
		t.Error("expected decode error")
	}
}
