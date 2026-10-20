package ipc

import "testing"

func TestResolveTarget(t *testing.T) {
	alive := map[string]bool{"v1": true, "v2.api": true, "plain": true}
	isAlive := func(n string) bool { return alive[n] }
	cases := []struct {
		name, session, tab string
		ok                 bool
	}{
		{"plain", "plain", "", true},
		{"v1.api", "v1", "api", true},  // tab of the v1 view
		{"v2.api", "v2.api", "", true}, // a real session of that name wins
		{"v1", "v1", "", true},
		{"ghost.api", "ghost.api", "", false},
		{"v1.", "v1.", "", false},
		{".api", ".api", "", false},
	}
	for _, c := range cases {
		s, tab, ok := resolveTarget(c.name, isAlive)
		if s != c.session || tab != c.tab || ok != c.ok {
			t.Errorf("resolveTarget(%q) = %q %q %v; want %q %q %v", c.name, s, tab, ok, c.session, c.tab, c.ok)
		}
	}
}
