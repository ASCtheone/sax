//go:build !windows

package session

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestAddCommandTab(t *testing.T) {
	s, err := NewSessionWithCommand(80, 24, "sh", []string{"-c", "sleep 2"}, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.Tabs[0].Name = "api"

	tab, err := s.AddCommandTab("web", "sh", []string{"-c", "sleep 2"}, "", []string{"SAX_TEST_VAR=web"})
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

func TestTabEnvAndLookup(t *testing.T) {
	s, err := NewSessionWithTab(80, 24, "api", "sh", []string{"-c", "echo var=$SAX_TEST_VAR; sleep 2"}, "",
		[]string{"SAX_TEST_VAR=from-api-tab"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.TabByName("api") != s.Tabs[0] || s.TabByName("nope") != nil {
		t.Fatal("TabByName")
	}
	pane := s.Tabs[0].ActivePaneObj()
	buf := make([]byte, 1024)
	var out strings.Builder
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(out.String(), "var=from-api-tab") {
		n, err := pane.Pty.Read(buf)
		out.Write(buf[:n])
		if err != nil {
			break
		}
	}
	if !strings.Contains(out.String(), "var=from-api-tab") {
		t.Errorf("tab env not applied: %q", out.String())
	}
}
