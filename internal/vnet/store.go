package vnet

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

const (
	routerFile   = "router.json"
	namesFile    = "names"
	servicesFile = "svc"
	portsDir     = "ports"
	lockFile     = ".lock"
	dirPerm      = 0o755
	filePerm     = 0o644
)

var (
	// ErrNotFound is returned when a router does not exist.
	ErrNotFound = errors.New("router not found")
	// ErrExists is returned when creating a router that already exists.
	ErrExists = errors.New("router already exists")
)

// Store persists routers as one directory each:
//
//	<dir>/<router>/router.json   the Router (source of truth)
//	<dir>/<router>/names         rendered for the shim
//	<dir>/<router>/svc           rendered for the shim
//	<dir>/<router>/ports/        live listener registry, written by the shim
//
// Writers serialize on a per-router file lock, so concurrent sessions
// starting in the same router never lose each other's updates.
type Store struct {
	dir string
}

// NewStore returns a store rooted at dir.
func NewStore(dir string) Store { return Store{dir: dir} }

// RouterDir is the directory holding a router's state; it is also the
// SAX_VNET_DIR handed to the shim.
func (s Store) RouterDir(name string) string { return filepath.Join(s.dir, name) }

// Create persists a new router and renders its tables.
func (s Store) Create(r Router) error {
	if err := r.validateHeader(); err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, dirPerm); err != nil {
		return fmt.Errorf("create vnet dir: %w", err)
	}
	if err := os.Mkdir(s.RouterDir(r.Name), dirPerm); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%s: %w", r.Name, ErrExists)
		}
		return fmt.Errorf("create router %s: %w", r.Name, err)
	}
	if err := s.withLock(r.Name, func() error { return s.write(r) }); err != nil {
		_ = os.RemoveAll(s.RouterDir(r.Name)) // don't leave a half-created router behind
		return err
	}
	return nil
}

// Get loads a router.
func (s Store) Get(name string) (Router, error) {
	if err := ValidateName("router", name); err != nil {
		return Router{}, err
	}
	data, err := os.ReadFile(filepath.Join(s.RouterDir(name), routerFile))
	if errors.Is(err, os.ErrNotExist) {
		return Router{}, fmt.Errorf("%s: %w", name, ErrNotFound)
	}
	if err != nil {
		return Router{}, fmt.Errorf("read router %s: %w", name, err)
	}
	var r Router
	if err := json.Unmarshal(data, &r); err != nil {
		return Router{}, fmt.Errorf("decode router %s: %w", name, err)
	}
	if err := r.validateHeader(); err != nil {
		return Router{}, fmt.Errorf("router %s is corrupt: %w", name, err)
	}
	return r.clone(), nil // normalizes nil maps/slices from older files
}

// List returns all routers sorted by name. Directories without a router file
// are ignored.
func (s Store) List() ([]Router, error) {
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list routers: %w", err)
	}
	var out []Router
	for _, e := range entries {
		if !e.IsDir() || ValidateName("router", e.Name()) != nil {
			continue
		}
		r, err := s.Get(e.Name())
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Update applies fn to the router under its lock and persists the result.
// If fn fails nothing is written.
func (s Store) Update(name string, fn func(Router) (Router, error)) (Router, error) {
	var updated Router
	err := s.withLock(name, func() error {
		cur, err := s.Get(name)
		if err != nil {
			return err
		}
		next, err := fn(cur)
		if err != nil {
			return err
		}
		if next.Name != name {
			return fmt.Errorf("update router %s: renaming is not supported", name)
		}
		updated = next
		return s.write(next)
	})
	return updated, err
}

// Remove deletes a router and all its state.
func (s Store) Remove(name string) error {
	if _, err := s.Get(name); err != nil {
		return err
	}
	if err := os.RemoveAll(s.RouterDir(name)); err != nil {
		return fmt.Errorf("remove router %s: %w", name, err)
	}
	return nil
}

func (s Store) withLock(name string, fn func() error) error {
	if err := ValidateName("router", name); err != nil {
		return err
	}
	dir := s.RouterDir(name)
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%s: %w", name, ErrNotFound)
	}
	f, err := os.OpenFile(filepath.Join(dir, lockFile), os.O_CREATE|os.O_RDWR, filePerm)
	if err != nil {
		return fmt.Errorf("open lock for router %s: %w", name, err)
	}
	defer f.Close()
	if err := lockFileExclusive(f); err != nil {
		return fmt.Errorf("lock router %s: %w", name, err)
	}
	defer unlockFile(f)
	return fn()
}

// write persists r and its rendered tables. Callers hold the router lock.
func (s Store) write(r Router) error {
	dir := s.RouterDir(r.Name)
	if err := os.MkdirAll(filepath.Join(dir, portsDir), dirPerm); err != nil {
		return fmt.Errorf("create ports dir: %w", err)
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("encode router %s: %w", r.Name, err)
	}
	tables, _ := r.Tables()
	files := []struct {
		name string
		data []byte
	}{
		{routerFile, append(data, '\n')},
		{namesFile, []byte(tables.NamesFile())},
		{servicesFile, []byte(tables.ServicesFile())},
	}
	for _, f := range files {
		if err := writeAtomic(filepath.Join(dir, f.name), f.data); err != nil {
			return err
		}
	}
	return nil
}

// writeAtomic replaces path in one rename so the shim never reads a partial
// table.
func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Chmod(filePerm); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}
