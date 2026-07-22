package cuc

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestClaudeCommand(t *testing.T) {
	got := Claude("cuc-dev")
	want := []string{"docker", "exec", "-it", "-u", "dev", "cuc-dev",
		"bash", "-lc", "cd /workspace && claude"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Claude() = %v, want %v", got, want)
	}
}

func TestComposeCommands(t *testing.T) {
	if got := ComposeUp(); !reflect.DeepEqual(got, []string{"docker", "compose", "up", "-d", "--build"}) {
		t.Errorf("ComposeUp() = %v", got)
	}
	if got := ComposeDown(); !reflect.DeepEqual(got, []string{"docker", "compose", "down"}) {
		t.Errorf("ComposeDown() = %v", got)
	}
}

func TestPullAndRemove(t *testing.T) {
	if got := PullImage("ghcr.io/x/y:latest"); !reflect.DeepEqual(got, []string{"docker", "pull", "ghcr.io/x/y:latest"}) {
		t.Errorf("PullImage() = %v", got)
	}
	if got := RemoveContainer("cuc-dev"); !reflect.DeepEqual(got, []string{"docker", "rm", "-f", "cuc-dev"}) {
		t.Errorf("RemoveContainer() = %v", got)
	}
}

func TestRunImage_MountsAndSecretByName(t *testing.T) {
	// API key must be passed BY NAME (never its value) so it can't leak into argv.
	t.Setenv("ANTHROPIC_API_KEY", "sk-secret-should-not-appear")
	got := RunImage("cuc-dev", "img:latest", "/host/proj", "/home/asc")
	joined := strings.Join(got, " ")

	if strings.Contains(joined, "sk-secret-should-not-appear") {
		t.Fatalf("secret value leaked into argv: %v", got)
	}
	for _, want := range []string{
		"--name cuc-dev",
		"/host/proj:/workspace",
		filepath.Join("/home/asc", ".claude") + ":/host-claude:ro",
		"-e ANTHROPIC_API_KEY",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("RunImage() missing %q in %q", want, joined)
		}
	}
	// image + keepalive come last
	if got[len(got)-3] != "img:latest" || got[len(got)-2] != "sleep" || got[len(got)-1] != "infinity" {
		t.Errorf("RunImage() tail = %v, want [img:latest sleep infinity]", got[len(got)-3:])
	}
}

func TestRunImage_NoHomeNoKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	got := RunImage("cuc-dev", "img:latest", "/proj", "")
	joined := strings.Join(got, " ")
	if strings.Contains(joined, "host-claude") {
		t.Errorf("expected no creds mount when home is empty: %v", got)
	}
	if strings.Contains(joined, "-e ANTHROPIC_API_KEY") {
		t.Errorf("expected no -e when key unset: %v", got)
	}
}

func TestComposeRoot_WalksUp(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "docker-compose.yml"), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	got := ComposeRoot(nested)
	// macOS temp dirs are symlinks (/var -> /private/var); compare resolved paths.
	gotResolved, _ := filepath.EvalSymlinks(got)
	rootResolved, _ := filepath.EvalSymlinks(root)
	if gotResolved != rootResolved {
		t.Fatalf("ComposeRoot(%q) = %q, want %q", nested, got, root)
	}
}

func TestComposeRoot_NoneFound(t *testing.T) {
	// A deep temp dir with no compose file anywhere up to it should... walk to /.
	// We can't guarantee the filesystem root has no compose file, so only assert
	// that a lone empty temp dir doesn't spuriously match itself.
	dir := t.TempDir()
	if got := ComposeRoot(dir); got == dir {
		t.Fatalf("ComposeRoot(%q) unexpectedly matched itself with no compose file", dir)
	}
}

func TestSeed(t *testing.T) {
	got := Seed("cuc-dev")
	if got[0] != "docker" || got[1] != "exec" {
		t.Fatalf("Seed() should be a docker exec: %v", got)
	}
	if !strings.Contains(strings.Join(got, " "), "/workspace/.devcontainer/post-create.sh") {
		t.Errorf("Seed() should reference post-create.sh: %v", got)
	}
}
