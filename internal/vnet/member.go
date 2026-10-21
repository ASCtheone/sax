package vnet

import (
	"fmt"
	"runtime"
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
	EnvSupervisor  = "SAX_VNET_SUPERVISOR"
	EnvDevBin      = "SAX_VNET_DEVBIN"

	envDyld          = "DYLD_INSERT_LIBRARIES"
	envLDPreload     = "LD_PRELOAD"
	envNodeOptions   = "NODE_OPTIONS"
	envZDotDir       = "ZDOTDIR"
	envPromptCommand = "PROMPT_COMMAND"
	envPath          = "PATH"
)

// goos is runtime.GOOS; tests override it.
var goos = runtime.GOOS

// preloadVar is the loader variable that injects the shim.
func preloadVar() string {
	if goos == "darwin" {
		return envDyld
	}
	return envLDPreload
}

// sipWorkarounds reports whether panes need the macOS SIP workarounds
// (re-exporting the shim inside Apple's shells).
func sipWorkarounds() bool { return goos == "darwin" }

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
	env := []string{
		EnvDir + "=" + m.Dir,
		EnvIP + "=" + m.IP,
		EnvSubnet + "=" + m.Subnet,
		EnvRouter + "=" + m.Router,
		EnvHost + "=" + m.Host,
		EnvShim + "=" + p.Lib,
	}
	if p.Reinject != "" {
		env = append(env, EnvReinject+"="+p.Reinject)
	}
	if p.ZDotDir != "" {
		env = append(env, EnvZWrapper+"="+p.ZDotDir)
	}
	if p.Supervisor != "" {
		env = append(env, EnvSupervisor+"="+p.Supervisor)
	}
	if p.DevBin != "" {
		env = append(env, EnvDevBin+"="+p.DevBin)
	}
	return env
}

// PaneWrapper is the command prefix for every pane of a networked daemon on
// Linux: the seccomp supervisor, which gives binaries that skip libc (Go,
// static) their virtual addresses. Nil elsewhere.
func PaneWrapper(getenv func(string) string) []string {
	sup := getenv(EnvSupervisor)
	if sup == "" || goos != "linux" {
		return nil
	}
	return []string{sup}
}

// PaneEnv returns the extra environment for a pane spawned by a networked
// daemon, or nil when the daemon is not in a router. getenv reads the
// daemon's environment.
//
//   - DYLD_INSERT_LIBRARIES (macOS) / LD_PRELOAD (Linux) loads the shim; on
//     macOS only non-SIP programs started directly by the pane keep it.
//   - NODE_OPTIONS --require is Node's fallback when the shim was lost on
//     the way (e.g. through #!/usr/bin/env node).
//   - ZDOTDIR (zsh) and PROMPT_COMMAND (bash) re-export the shim inside the
//     SIP-protected shells, which drop DYLD_* at startup.
//   - PATH starts with the devbin folder (macOS), so python3 & co run the
//     real tool instead of Apple's SIP-protected /usr/bin stub; the shells
//     keep it first at every prompt.
func PaneEnv(getenv func(string) string) []string {
	lib := getenv(EnvShim)
	if lib == "" {
		return nil
	}
	if PaneWrapper(getenv) != nil {
		return nil // the supervisor sets LD_PRELOAD itself if it has to fall back
	}
	preload := lib
	if cur := getenv(preloadVar()); cur != "" && !strings.Contains(cur, lib) {
		preload = lib + ":" + cur
	}
	env := []string{preloadVar() + "=" + preload}

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

	devbin := getenv(EnvDevBin)
	if sipWorkarounds() && devbin != "" {
		env = append(env, envPath+"="+devbinFirst(devbin, getenv(envPath)))
	}
	if sipWorkarounds() && getenv(envPromptCommand) == "" {
		env = append(env, envPromptCommand+"="+promptLine(lib, devbin))
	}
	return env
}

func exportLine(lib string) string {
	return fmt.Sprintf("export %s='%s'", envDyld, shellQuote(lib))
}

// promptLine is the bash PROMPT_COMMAND: re-export the shim and keep devbin
// first on PATH (rc files may have put /usr/bin before it).
func promptLine(lib, devbin string) string {
	line := exportLine(lib)
	if devbin != "" {
		q := shellQuote(devbin)
		line += fmt.Sprintf("; PATH='%s':\"${PATH//'%s:'/}\"", q, q)
	}
	return line
}

func shellQuote(s string) string { return strings.ReplaceAll(s, "'", `'\''`) }

// devbinFirst returns path with devbin as its first entry, once.
func devbinFirst(devbin, path string) string {
	parts := []string{devbin}
	for _, p := range strings.Split(path, ":") {
		if p != "" && p != devbin {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, ":")
}

// withoutDevbin removes devbin from path.
func withoutDevbin(devbin, path string) string {
	var parts []string
	for _, p := range strings.Split(path, ":") {
		if p != devbin {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, ":")
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
	lib, reinject, wrapper, devbin := vars[EnvShim], vars[EnvReinject], vars[EnvZWrapper], vars[EnvDevBin]

	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		k, v, _ := strings.Cut(kv, "=")
		switch {
		case strings.HasPrefix(k, "SAX_VNET_"):
			continue
		case k == envDyld && lib != "" && v == lib:
			continue
		case k == envLDPreload && lib != "" && strings.Contains(v, lib):
			if rest := strings.Trim(strings.ReplaceAll(v, lib, ""), ": "); rest != "" {
				out = append(out, envLDPreload+"="+rest)
			}
			continue
		case k == envPromptCommand && lib != "" && (v == exportLine(lib) || v == promptLine(lib, devbin)):
			continue
		case k == envPath && devbin != "":
			out = append(out, envPath+"="+withoutDevbin(devbin, v))
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
