package ipc

import (
	"fmt"
	"strings"
)

// ResolveTarget maps a name to a session and, optionally, one of its tabs.
// "v1.api" is the session "v1.api" if it exists, otherwise the "api" tab of
// session "v1" (a router view started by sax net up). ok is false when
// neither exists.
func ResolveTarget(name string) (session, tab string, ok bool) {
	return resolveTarget(name, IsSessionAlive)
}

func resolveTarget(name string, alive func(string) bool) (string, string, bool) {
	if alive(name) {
		return name, "", true
	}
	if i := strings.LastIndexByte(name, '.'); i > 0 && i < len(name)-1 && alive(name[:i]) {
		return name[:i], name[i+1:], true
	}
	return name, "", false
}

// Tail returns the last n lines of a session (or session.tab).
func Tail(name string, n int) (string, error) {
	session, tab, ok := ResolveTarget(name)
	if !ok {
		return "", fmt.Errorf("session %q not found", name)
	}
	args := fmt.Sprintf("%d", n)
	if tab != "" {
		args += "\t" + tab
	}
	reply := QuerySessionTyped(session, "tail", args)
	if reply == nil {
		return "", fmt.Errorf("failed to query session %q", session)
	}
	out, _ := reply["tail"].(string)
	return out, nil
}

// Send types text into a session (or session.tab). It reports whether the
// pane accepted it.
func Send(name, text string) (bool, error) {
	session, tab, ok := ResolveTarget(name)
	if !ok {
		return false, fmt.Errorf("session %q not found", name)
	}
	query, args := "send", text
	if tab != "" {
		query, args = "sendtab", tab+"\t"+text
	}
	reply := QuerySessionTyped(session, query, args)
	if reply == nil {
		return false, fmt.Errorf("failed to send to session %q", session)
	}
	sent, _ := reply["ok"].(bool)
	return sent, nil
}
