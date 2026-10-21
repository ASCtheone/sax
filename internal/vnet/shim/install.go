// Package shim builds and installs the vnet interpose library that sessions
// load through DYLD_INSERT_LIBRARIES (macOS) or LD_PRELOAD (Linux). The C
// sources are embedded in sax and compiled on first use with the system C
// compiler, then cached under a content-addressed directory so upgrades
// rebuild exactly once.
package shim

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

//go:embed src/common.c src/common.h src/shim.c src/exec.c src/shim.h src/sup.c src/reinject.cjs src/zshenv
var sources embed.FS

const (
	supName       = "sax-vnet-sup"
	reinjectName  = "reinject.cjs"
	zdotdirName   = "zdotdir"
	versionLength = 12
	dirPerm       = 0o755
	filePerm      = 0o644
)

// platform describes how the shim is built and loaded on one OS.
type platform struct {
	libName    string
	sources    []string   // compiled into the library, in this order
	baseFlags  []string   // compiler flags for a shared library
	archSets   [][]string // -arch sets to try in order (nil: native only)
	ldFlags    []string
	supSources []string // seccomp supervisor executable (Linux); nil: none
	sipHelpers bool     // Node reinject + zsh wrapper (macOS SIP workarounds)
	ccHint     string   // how to get a C compiler
}

var platforms = map[string]platform{
	"darwin": {
		libName:   "libsaxvnet.dylib",
		sources:   []string{"common.c", "shim.c", "exec.c"},
		baseFlags: []string{"-dynamiclib", "-O2"},
		// Native arm64, Apple's arm64e binaries and Rosetta; then host only.
		archSets:   [][]string{{"arm64", "arm64e", "x86_64"}, {hostArch()}},
		sipHelpers: true,
		ccHint:     "run: xcode-select --install",
	},
	"linux": {
		libName:    "libsaxvnet.so",
		sources:    []string{"common.c", "shim.c"},
		baseFlags:  []string{"-shared", "-fPIC", "-O2"},
		archSets:   [][]string{nil},
		ldFlags:    []string{"-ldl"},
		supSources: []string{"common.c", "sup.c"},
		ccHint:     "install gcc, e.g. apt install build-essential",
	},
}

// ErrUnsupported is returned on platforms without a shim backend yet.
var ErrUnsupported = errors.New("vnet sessions are supported on macOS and Linux only")

func currentPlatform() (platform, bool) {
	p, ok := platforms[goos]
	return p, ok
}

// goos is runtime.GOOS; tests override it.
var goos = runtime.GOOS

// Paths locates an installed shim.
type Paths struct {
	Dir      string // install directory (content-addressed)
	Lib      string // the interpose dylib
	Reinject string // Node --require fallback
	ZDotDir  string // ZDOTDIR holding the wrapper .zshenv
	// Supervisor runs pane commands under seccomp (Linux), so binaries that
	// skip libc get virtual addresses too.
	Supervisor string
	// DevBin holds links to the real developer tools behind Apple's
	// SIP-protected /usr/bin stubs (macOS; "" when none). See devbin.go.
	DevBin string
}

func pathsIn(dir string, p platform) Paths {
	paths := Paths{Dir: dir, Lib: filepath.Join(dir, p.libName)}
	if p.sipHelpers {
		paths.Reinject = filepath.Join(dir, reinjectName)
		paths.ZDotDir = filepath.Join(dir, zdotdirName)
	}
	if p.supSources != nil {
		paths.Supervisor = filepath.Join(dir, supName)
	}
	return paths
}

// Version is a short hash of the embedded sources; it names the install dir.
func Version() string {
	h := sha256.New()
	_ = fs.WalkDir(sources, "src", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := sources.ReadFile(path)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", path, len(data))
		h.Write(data)
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))[:versionLength]
}

// Install makes sure the shim for this sax version exists under root and
// returns its paths. Safe to call concurrently from several processes.
func Install(root string) (Paths, error) {
	paths, err := installLib(root)
	if err != nil || paths.ZDotDir == "" {
		return paths, err
	}
	// Without it only python3 & co lose the network; everything else works.
	if dir, err := installDevBin(root); err == nil {
		paths.DevBin = dir
	}
	return paths, nil
}

func installLib(root string) (Paths, error) {
	plat, ok := currentPlatform()
	if !ok {
		return Paths{}, ErrUnsupported
	}
	dir := filepath.Join(root, Version())
	if _, err := os.Stat(filepath.Join(dir, plat.libName)); err == nil {
		return pathsIn(dir, plat), nil
	}

	if err := os.MkdirAll(root, dirPerm); err != nil {
		return Paths{}, fmt.Errorf("create shim dir: %w", err)
	}
	tmp, err := os.MkdirTemp(root, ".build-")
	if err != nil {
		return Paths{}, fmt.Errorf("create shim build dir: %w", err)
	}
	defer os.RemoveAll(tmp) // no-op once renamed into place

	if err := build(tmp, plat); err != nil {
		return Paths{}, err
	}
	if err := os.Rename(tmp, dir); err != nil {
		// Another process won the race; its install is just as good.
		if _, statErr := os.Stat(filepath.Join(dir, plat.libName)); statErr == nil {
			return pathsIn(dir, plat), nil
		}
		return Paths{}, fmt.Errorf("install shim: %w", err)
	}
	return pathsIn(dir, plat), nil
}

// build writes the sources into dir and compiles the library there.
func build(dir string, plat platform) error {
	srcDir := filepath.Join(dir, "src")
	if err := os.MkdirAll(srcDir, dirPerm); err != nil {
		return fmt.Errorf("create shim src dir: %w", err)
	}
	entries, err := sources.ReadDir("src")
	if err != nil {
		return fmt.Errorf("read embedded shim: %w", err)
	}
	for _, e := range entries {
		data, err := sources.ReadFile("src/" + e.Name())
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", e.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(srcDir, e.Name()), data, filePerm); err != nil {
			return fmt.Errorf("write %s: %w", e.Name(), err)
		}
	}

	if plat.sipHelpers {
		if err := copyFile(filepath.Join(srcDir, reinjectName), filepath.Join(dir, reinjectName)); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Join(dir, zdotdirName), dirPerm); err != nil {
			return fmt.Errorf("create zdotdir: %w", err)
		}
		if err := copyFile(filepath.Join(srcDir, "zshenv"), filepath.Join(dir, zdotdirName, ".zshenv")); err != nil {
			return err
		}
	}
	if err := compile(srcDir, filepath.Join(dir, plat.libName), plat, plat.sources, plat.baseFlags); err != nil {
		return err
	}
	if plat.supSources != nil {
		return compile(srcDir, filepath.Join(dir, supName), plat, plat.supSources, []string{"-O2"})
	}
	return nil
}

// compile builds the library, trying each architecture set in turn (on
// macOS: fat first, then the host architecture alone).
func compile(srcDir, out string, plat platform, sources, flags []string) error {
	cc := os.Getenv("CC")
	if cc == "" {
		cc = "cc"
	}
	if _, err := exec.LookPath(cc); err != nil {
		return fmt.Errorf("vnet needs a C compiler to build its shim (%s): %w", plat.ccHint, err)
	}

	srcs := make([]string, len(sources))
	for i, s := range sources {
		srcs[i] = filepath.Join(srcDir, s)
	}
	var lastErr error
	for _, archs := range plat.archSets {
		args := append(append([]string{}, flags...), "-o", out)
		for _, a := range archs {
			args = append(args, "-arch", a)
		}
		args = append(append(args, srcs...), plat.ldFlags...)
		cmd := exec.Command(cc, args...)
		output, err := cmd.CombinedOutput()
		if err == nil {
			return nil
		}
		lastErr = fmt.Errorf("compile shim (%s): %w\n%s", strings.Join(archs, ","), err, output)
	}
	return lastErr
}

func hostArch() string {
	if runtime.GOARCH == "amd64" {
		return "x86_64"
	}
	return runtime.GOARCH
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read %s: %w", src, err)
	}
	if err := os.WriteFile(dst, data, filePerm); err != nil {
		return fmt.Errorf("write %s: %w", dst, err)
	}
	return nil
}

// SupervisionAvailable reports whether the seccomp supervisor can work on
// this machine (it can be blocked by Yama ptrace_scope >= 2 or a container
// seccomp profile). Always false where there is no supervisor.
func SupervisionAvailable(p Paths) bool {
	if p.Supervisor == "" {
		return false
	}
	return exec.Command(p.Supervisor, "--check").Run() == nil
}
