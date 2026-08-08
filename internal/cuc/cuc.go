// Package cuc integrates the Claude Unleashed Container (cuc) dev container with
// SAX: bring the container up, run a Claude instance inside it as a SAX session,
// and tear it down. It is deliberately image-specific — it targets the cuc image
// and the `cuc-dev` container, mirroring the shape of the nx integration.
package cuc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	// DefaultContainer is the container name the cuc devcontainer/compose creates.
	DefaultContainer = "cuc-dev"
	// DefaultImage is the published multi-arch image (GHCR).
	DefaultImage = "ghcr.io/asctheone/cuc:latest"
	// SessionName is the SAX session that hosts the in-container Claude instance.
	SessionName = "cuc-claude"
	// WorkspaceDir is where the project is mounted inside the container.
	WorkspaceDir = "/workspace"
	// ContainerUser is the non-root user Claude runs as.
	ContainerUser = "dev"
)

var composeFiles = []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"}

// DockerAvailable reports whether a docker CLI is on PATH.
func DockerAvailable() bool {
	_, err := exec.LookPath("docker")
	return err == nil
}

// ComposeRoot walks up from dir (or the current dir when empty) looking for a
// compose file. Returns "" when none is found — the caller then falls back to
// pulling and running the published image.
func ComposeRoot(dir string) string {
	if dir == "" {
		var err error
		if dir, err = os.Getwd(); err != nil {
			return ""
		}
	}
	current := dir
	for {
		for _, name := range composeFiles {
			if _, err := os.Stat(filepath.Join(current, name)); err == nil {
				return current
			}
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
		current = parent
	}
}

// ComposeUp builds the compose invocation that starts the stack. It reuses the
// existing image (compose still builds automatically on first run when absent);
// a rebuild is an explicit `docker compose build` / `cuc-up.sh --build`.
func ComposeUp() []string { return []string{"docker", "compose", "up", "-d"} }

// ComposeDown builds the compose teardown invocation.
func ComposeDown() []string { return []string{"docker", "compose", "down"} }

// PullImage builds the docker pull invocation for the published image.
func PullImage(image string) []string { return []string{"docker", "pull", image} }

// RunImage builds the no-compose fallback: run the published image detached with
// the standard cuc mounts. The API key is passed by NAME (not value) so the
// secret never lands in the process argument list.
func RunImage(container, image, projectDir, home string) []string {
	args := []string{
		"docker", "run", "-d",
		"--name", container,
		"-w", WorkspaceDir,
		"-v", projectDir + ":" + WorkspaceDir,
	}
	if home != "" {
		args = append(args, "-v", filepath.Join(home, ".claude")+":/host-claude:ro")
	}
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		args = append(args, "-e", "ANTHROPIC_API_KEY")
	}
	args = append(args, image, "sleep", "infinity")
	return args
}

// RemoveContainer builds a force-remove invocation for a stale container.
func RemoveContainer(container string) []string {
	return []string{"docker", "rm", "-f", container}
}

// Seed runs the container's credential-seeding / version-report script if it is
// present (compose mounts it at /workspace/.devcontainer). Best-effort.
func Seed(container string) []string {
	script := WorkspaceDir + "/.devcontainer/post-create.sh"
	return []string{"docker", "exec", "-u", ContainerUser, container,
		"bash", "-lc", "test -f " + script + " && bash " + script + " || true"}
}

// Claude is the command a SAX session runs to host a Claude instance inside the
// container. A login shell (-l) ensures Go/Rust are on PATH; -it gives Claude a
// TTY (SAX supplies the PTY around it).
func Claude(container string) []string {
	return []string{"docker", "exec", "-it", "-u", ContainerUser, container,
		"bash", "-lc", "cd " + WorkspaceDir + " && claude"}
}

// Running reports whether the named container exists and is currently running.
func Running(container string) bool {
	out, err := exec.Command("docker", "inspect", "-f", "{{.State.Running}}", container).Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "true"
}

// Exists reports whether a container with the given name exists in any state.
func Exists(container string) bool {
	return exec.Command("docker", "inspect", container).Run() == nil
}

// KeychainService is the macOS Keychain service under which Claude Code stores
// its OAuth token (there is no ~/.claude/.credentials.json on macOS).
const KeychainService = "Claude Code-credentials"

// ChownHome makes the (root-owned) named volume at ~/.claude writable by dev.
func ChownHome(container string) []string {
	home := "/home/" + ContainerUser + "/.claude"
	return []string{"docker", "exec", "-u", "root", container,
		"chown", "-R", ContainerUser + ":" + ContainerUser, home}
}

// EnsureConfigSymlink points ~/.claude.json at a file INSIDE the volume so the
// login/onboarding state persists across container recreation (otherwise
// ~/.claude.json is a sibling of the volume and is lost on every recreate).
func EnsureConfigSymlink(container string) []string {
	script := `mkdir -p "$HOME/.claude"; ` +
		`if [ ! -L "$HOME/.claude.json" ]; then ` +
		`[ -f "$HOME/.claude.json" ] && mv "$HOME/.claude.json" "$HOME/.claude/config.json"; ` +
		`ln -sfn "$HOME/.claude/config.json" "$HOME/.claude.json"; fi`
	return []string{"docker", "exec", "-u", ContainerUser, container, "bash", "-lc", script}
}

// SetupAuth transposes the host Claude login into the container: macOS Keychain
// credentials plus the account + onboarding fields the interactive TUI requires
// (print mode needs only credentials, the TUI needs these too). Best-effort —
// returns nil when there is nothing to sync (e.g. non-macOS, no Keychain entry).
func SetupAuth(container string) error {
	// 1. credentials: macOS Keychain -> container ~/.claude/.credentials.json.
	//    Piped via stdin so the token never lands in an argv/env.
	if _, err := exec.LookPath("security"); err == nil {
		out, err := exec.Command("security", "find-generic-password", "-s", KeychainService, "-w").Output()
		if err == nil && len(strings.TrimSpace(string(out))) > 0 {
			write := exec.Command("docker", "exec", "-i", "-u", ContainerUser, container,
				"bash", "-c", `umask 077; mkdir -p "$HOME/.claude" && cat > "$HOME/.claude/.credentials.json"`)
			write.Stdin = bytes.NewReader(out)
			if err := write.Run(); err != nil {
				return fmt.Errorf("write credentials: %w", err)
			}
		}
	}

	// 2. account + onboarding flag -> container ~/.claude.json. oauthAccount/userID
	//    carry no secret, so they pass via env; the script is piped on stdin.
	acct := hostAccountJSON()
	py := "import json,os\n" +
		"acct=json.loads(os.environ.get('CUC_ACCT','{}'))\n" +
		"p=os.path.expanduser('~/.claude.json')\n" +
		"d=json.load(open(p)) if os.path.exists(p) else {}\n" +
		"d.update(acct)\n" +
		"d['hasCompletedOnboarding']=True\n" +
		"json.dump(d,open(p,'w'),indent=2)\n"
	cmd := exec.Command("docker", "exec", "-i", "-u", ContainerUser, "-e", "CUC_ACCT="+acct, container, "python3", "-")
	cmd.Stdin = strings.NewReader(py)
	return cmd.Run()
}

// hostAccountJSON returns {"oauthAccount":..,"userID":..} extracted from the
// host's ~/.claude.json, or "{}" when unavailable. Values stay as raw JSON so
// nested account objects are preserved verbatim.
func hostAccountJSON() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "{}"
	}
	data, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		return "{}"
	}
	var full map[string]json.RawMessage
	if err := json.Unmarshal(data, &full); err != nil {
		return "{}"
	}
	out := map[string]json.RawMessage{}
	for _, k := range []string{"oauthAccount", "userID"} {
		if v, ok := full[k]; ok {
			out[k] = v
		}
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "{}"
	}
	return string(b)
}
