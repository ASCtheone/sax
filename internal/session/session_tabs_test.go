//go:build !windows

package session

import (
	"reflect"
	"testing"
)

func TestAddCommandTab(t *testing.T) {
	s, err := NewSessionWithCommand(80, 24, "sh", []string{"-c", "sleep 2"}, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.Tabs[0].Name = "api"

	tab, err := s.AddCommandTab("web", "sh", []string{"-c", "sleep 2"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if tab.Name != "web" || len(tab.Panes) != 1 {
		t.Errorf("tab = %+v", tab)
	}
	if got := s.TabNames(); !reflect.DeepEqual(got, []string{"1:api", "2:web"}) {
		t.Errorf("TabNames = %v", got)
	}
	if s.ActiveTab != 0 {
		t.Errorf("AddCommandTab must not steal focus: ActiveTab = %d", s.ActiveTab)
	}
}
