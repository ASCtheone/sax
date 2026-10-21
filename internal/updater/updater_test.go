package updater

import "testing"

func TestIsNewer(t *testing.T) {
	tests := []struct {
		current, latest string
		want            bool
	}{
		{"v1.4.0", "v1.7.0", true},
		{"v1.7.0", "v1.4.0", false}, // stale cache after an update: no downgrade
		{"v1.7.0", "v1.7.0", false},
		{"v1.7.0", "v1.7.1", true},
		{"v1.7.1", "v1.7.0", false},
		{"v1.9.0", "v1.10.0", true}, // numeric, not lexical
		{"v1.10.0", "v1.9.0", false},
		{"v1.7.0", "v2.0.0", true},
		{"1.7.0", "v1.7.0", false},
		{"v1.7", "v1.7.0", false},
		{"v1.7.0", "", false},
		{"v1.7.0-rc1", "v1.7.0", true},
		{"v1.7.0", "v1.7.0-rc1", false},
		{"v0.1.0-20250101-120000-abc1234", "v0.1.0-20250202-120000-def5678", true},
		{"v0.1.0-20250202-120000-def5678", "v0.1.0-20250101-120000-abc1234", false},
		{"weird", "v1.0.0", true}, // unparseable current: any other tag is offered
	}
	for _, tt := range tests {
		if got := IsNewer(tt.current, tt.latest); got != tt.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
		}
	}
}
