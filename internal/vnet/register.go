package vnet

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// registrationFile records the domains whose names the OS resolver sends to
// sax (sax net register). It sits next to the routers; the underscore keeps
// it out of the router namespace.
const registrationFile = "_register.json"

// Registration lists the domains registered with the host resolver. Values
// are immutable.
type Registration struct {
	Domains []string `json:"domains"`
}

// With adds domains (normalized, sorted, unique).
func (r Registration) With(domains ...string) (Registration, error) {
	set := map[string]bool{}
	for _, d := range r.Domains {
		set[d] = true
	}
	for _, d := range domains {
		d = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(d)), ".")
		if err := validateHostname(d); err != nil {
			return r, fmt.Errorf("domain: %w", err)
		}
		if d == "localhost" {
			return r, fmt.Errorf("domain: .localhost already reaches sax without registering")
		}
		set[d] = true
	}
	return Registration{Domains: sortedKeys(set)}, nil
}

// Without removes domains; unknown ones are ignored.
func (r Registration) Without(domains ...string) Registration {
	drop := map[string]bool{}
	for _, d := range domains {
		drop[strings.TrimSuffix(strings.ToLower(d), ".")] = true
	}
	set := map[string]bool{}
	for _, d := range r.Domains {
		if !drop[d] {
			set[d] = true
		}
	}
	return Registration{Domains: sortedKeys(set)}
}

// Covers returns the registered domain a host name (optionally with a port)
// belongs to. The domain itself is not a usable name.
func (r Registration) Covers(host string) (string, bool) {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	for _, d := range r.Domains {
		if strings.HasSuffix(host, "."+d) {
			return d, true
		}
	}
	return "", false
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Registration loads the registered domains (none if never registered).
func (s Store) Registration() (Registration, error) {
	data, err := os.ReadFile(filepath.Join(s.dir, registrationFile))
	if errors.Is(err, os.ErrNotExist) {
		return Registration{}, nil
	}
	if err != nil {
		return Registration{}, fmt.Errorf("read registration: %w", err)
	}
	var r Registration
	if err := json.Unmarshal(data, &r); err != nil {
		return Registration{}, fmt.Errorf("decode registration: %w", err)
	}
	return r, nil
}

// SaveRegistration persists the registered domains.
func (s Store) SaveRegistration(r Registration) error {
	if err := os.MkdirAll(s.dir, dirPerm); err != nil {
		return fmt.Errorf("create vnet dir: %w", err)
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("encode registration: %w", err)
	}
	return writeAtomic(filepath.Join(s.dir, registrationFile), append(data, '\n'))
}
