// Package shim builds and installs the vnet interpose library that sessions
// load through DYLD_INSERT_LIBRARIES. The C sources are embedded in sax and
// compiled on first use with the system C compiler (present wherever the
// Xcode Command Line Tools are, which git on macOS already requires), then
// cached under a content-addressed directory so upgrades rebuild exactly once.
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

//go:embed src/shim.c src/exec.c src/shim.h src/reinject.cjs src/zshenv
var sources embed.FS

const (
	libName       = "libsaxvnet.dylib"
	reinjectName  = "reinject.cjs"
	zdotdirName   = "zdotdir"
	versionLength = 12
	dirPerm       = 0o755
	filePerm      = 0o644
)

// cSources are compiled into the library, in this order.
var cSources = []string{"shim.c", "exec.c"}

// fatArchs covers native arm64 processes, Apple's arm64e binaries and Rosetta.
var fatArchs = []string{"arm64", "arm64e", "x86_64"}

// ErrUnsupported is returned on platforms without a shim backend yet.
var ErrUnsupported = errors.New("vnet sessions are only supported on macOS for now")

// Paths locates an installed shim.
type Paths struct {
	Dir      string // install directory (content-addressed)
	Lib      string // the interpose dylib
	Reinject string // Node --require fallback
	ZDotDir  string // ZDOTDIR holding the wrapper .zshenv
}

func pathsIn(dir string) Paths {
	return Paths{
		Dir:      dir,
		Lib:      filepath.Join(dir, libName),
		Reinject: filepath.Join(dir, reinjectName),
		ZDotDir:  filepath.Join(dir, zdotdirName),
	}
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
	if runtime.GOOS != "darwin" {
		return Paths{}, ErrUnsupported
	}
	dir := filepath.Join(root, Version())
	if _, err := os.Stat(filepath.Join(dir, libName)); err == nil {
		return pathsIn(dir), nil
	}

	if err := os.MkdirAll(root, dirPerm); err != nil {
		return Paths{}, fmt.Errorf("create shim dir: %w", err)
	}
	tmp, err := os.MkdirTemp(root, ".build-")
	if err != nil {
		return Paths{}, fmt.Errorf("create shim build dir: %w", err)
	}
	defer os.RemoveAll(tmp) // no-op once renamed into place

	if err := build(tmp); err != nil {
		return Paths{}, err
	}
	if err := os.Rename(tmp, dir); err != nil {
		// Another process won the race; its install is just as good.
		if _, statErr := os.Stat(filepath.Join(dir, libName)); statErr == nil {
			return pathsIn(dir), nil
		}
		return Paths{}, fmt.Errorf("install shim: %w", err)
	}
	return pathsIn(dir), nil
}

// build writes the sources into dir and compiles the library there.
func build(dir string) error {
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

	if err := copyFile(filepath.Join(srcDir, reinjectName), filepath.Join(dir, reinjectName)); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, zdotdirName), dirPerm); err != nil {
		return fmt.Errorf("create zdotdir: %w", err)
	}
	if err := copyFile(filepath.Join(srcDir, "zshenv"), filepath.Join(dir, zdotdirName, ".zshenv")); err != nil {
		return err
	}

	return compile(srcDir, filepath.Join(dir, libName))
}

// compile builds a fat library, falling back to the host architecture when
// the toolchain lacks one of the slices.
func compile(srcDir, out string) error {
	cc := os.Getenv("CC")
	if cc == "" {
		cc = "cc"
	}
	if _, err := exec.LookPath(cc); err != nil {
		return fmt.Errorf("vnet needs a C compiler to build its shim (run: xcode-select --install): %w", err)
	}

	srcs := make([]string, len(cSources))
	for i, s := range cSources {
		srcs[i] = filepath.Join(srcDir, s)
	}
	attempts := [][]string{fatArchs, {hostArch()}}
	var lastErr error
	for _, archs := range attempts {
		args := []string{"-dynamiclib", "-O2", "-o", out}
		for _, a := range archs {
			args = append(args, "-arch", a)
		}
		cmd := exec.Command(cc, append(args, srcs...)...)
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
