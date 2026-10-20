package vnet

import (
	"fmt"
	"strings"

	"github.com/asc/sax/internal/vnet/shim"
)

// Environment carried by a networked session. The sax daemon gets the
// SAX_VNET_* settings only (it never loads the shim itself); PaneEnv derives
// what each pane needs from them.
const (
	EnvDir         = "SAX_VNET_DIR"
	EnvIP          = "SAX_VNET_IP"
	EnvSubnet      = "SAX_VNET_SUBNET"
	EnvRouter      = "SAX_VNET_ROUTER"
	EnvHost        = "SAX_VNET_HOST"
	EnvShim        = "SAX_VNET_SHIM"
	EnvReinject    = "SAX_VNET_REINJECT"
	EnvZWrapper    = "SAX_VNET_ZWRAPPER"
	EnvOrigZDotDir = "SAX_VNET_ZDOTDIR"

	envDyld          = "DYLD_INSERT_LIBRARIES"
	envNodeOptions   = "NODE_OPTIONS"
	envZDotDir       = "ZDOTDIR"
	envPromptCommand = "PROMPT_COMMAND"
)

// Member is one session's place in a router.
type Member struct {
	Router string
	Host   string // name inside the router (DNS label)
	IP     string
	Subnet string
	Dir    string // router state dir (SAX_VNET_DIR)
}

// ParseNetFlag parses --net <router>[:<host>].
func ParseNetFlag(v string) (router, host string, err error) {
	router, host, hasHost := strings.Cut(v, ":")
	if err := ValidateName("router", router); err != nil {
		return "", "", fmt.Errorf("--net %q: %w", v, err)
	}
	if hasHost {
		if err := ValidateName("session", host); err != nil {
			return "", "", fmt.Errorf("--net %q: %w", v, err)
		}
	}
	return router, host, nil
}

// HostFromSessionName derives a router host name from a sax session name:
// the last dot-separated part, lowercased ("v1.api" -> "api").
func HostFromSessionName(name string) (string, error) {
	host := strings.ToLower(name[strings.LastIndex(name, ".")+1:])
	if err := ValidateName("session", host); err != nil {
		return "", fmt.Errorf("cannot derive a router name from session %q; use --net <router>:<name>", name)
	}
	return host, nil
}

// SessionName is the sax session that `sax net up` uses for a host.
func SessionName(router, host string) string { return router + "." + host }

// DaemonEnv is the environment for the sax daemon of a networked session.
func DaemonEnv(m Member, p shim.Paths) []string {
	return []string{
		EnvDir + "=" + m.Dir,
		EnvIP + "=" + m.IP,
		EnvSubnet + "=" + m.Subnet,
		EnvRouter + "=" + m.Router,
		EnvHost + "=" + m.Host,
		EnvShim + "=" + p.Lib,
		EnvReinject + "=" + p.Reinject,
		EnvZWrapper + "=" + p.ZDotDir,
	}
}

// PaneEnv returns the extra environment for a pane spawned by a networked
// daemon, or nil when the daemon is not in a router. getenv reads the
// daemon's environment.
//
//   - DYLD_INSERT_LIBRARIES loads the shim into non-SIP programs started
//     directly by the pane.
//   - NODE_OPTIONS --require is Node's fallback when the shim was lost on
//     the way (e.g. through #!/usr/bin/env node).
//   - ZDOTDIR (zsh) and PROMPT_COMMAND (bash) re-export the shim inside the
//     SIP-protected shells, which drop DYLD_* at startup.
func PaneEnv(getenv func(string) string) []string {
	lib := getenv(EnvShim)
	if lib == "" {
		return nil
	}
	env := []string{envDyld + "=" + lib}

	if reinject := getenv(EnvReinject); reinject != "" {
		opts := getenv(envNodeOptions)
		req := fmt.Sprintf("--require %q", reinject)
		if !strings.Contains(opts, req) {
			opts = strings.TrimSpace(opts + " " + req)
		}
		env = append(env, envNodeOptions+"="+opts)
	}

	if wrapper := getenv(EnvZWrapper); wrapper != "" {
		env = append(env, envZDotDir+"="+wrapper)
		if orig := getenv(envZDotDir); orig != "" && orig != wrapper {
			env = append(env, EnvOrigZDotDir+"="+orig)
		}
	}

	if getenv(envPromptCommand) == "" {
		env = append(env, envPromptCommand+"="+exportLine(lib))
	}
	return env
}

// WrapScript prefixes a shell script so a SIP-protected shell re-exports the
// shim before running it. Returns the script unchanged outside a router.
func WrapScript(script string, getenv func(string) string) string {
	lib := getenv(EnvShim)
	if lib == "" {
		return script
	}
	return exportLine(lib) + "\n" + script
}

func exportLine(lib string) string {
	return fmt.Sprintf("export %s='%s'", envDyld, strings.ReplaceAll(lib, "'", `'\''`))
}

// StripEnv removes everything PaneEnv and DaemonEnv added, restoring the
// user's own values, so a sax daemon started from inside a networked pane
// does not inherit that pane's network identity.
func StripEnv(environ []string) []string {
	vars := make(map[string]string, len(environ))
	for _, kv := range environ {
		k, v, _ := strings.Cut(kv, "=")
		vars[k] = v
	}
	lib, reinject, wrapper := vars[EnvShim], vars[EnvReinject], vars[EnvZWrapper]

	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		k, v, _ := strings.Cut(kv, "=")
		switch {
		case strings.HasPrefix(k, "SAX_VNET_"):
			continue
		case k == envDyld && lib != "" && v == lib:
			continue
		case k == envPromptCommand && lib != "" && v == exportLine(lib):
			continue
		case k == envZDotDir && wrapper != "" && v == wrapper:
			if orig := vars[EnvOrigZDotDir]; orig != "" {
				out = append(out, envZDotDir+"="+orig)
			}
			continue
		case k == envNodeOptions && reinject != "":
			opts := strings.TrimSpace(strings.ReplaceAll(v, fmt.Sprintf("--require %q", reinject), ""))
			if opts != "" {
				out = append(out, envNodeOptions+"="+opts)
			}
			continue
		}
		out = append(out, kv)
	}
	return out
}
