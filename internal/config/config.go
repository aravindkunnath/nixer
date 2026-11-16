// Package config knows where the user's Nix config lives and which list in
// which file each kind of package belongs to.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Target describes one editable list.
type Target struct {
	Kind  string
	Label string
	File  string
	Attr  string
	Quote bool // items are quoted strings (Homebrew) rather than bare attrs
}

// FileName is the optional project config file in the config directory.
const FileName = "nixer.json"

// DefaultUnstable is the attribute set unstable packages are taken from.
const DefaultUnstable = "pkgs-unstable"

// DefaultTargets lists every editable list, in display order. It is used as
// is when there is no nixer.json, and as the base nixer.json overrides.
var DefaultTargets = []Target{
	{"home", "home packages", "home.nix", "home.packages", false},
	{"system", "system packages", "configuration.nix", "environment.systemPackages", false},
	{"font", "fonts", "configuration.nix", "fonts.packages", false},
	{"brew", "homebrew brews", "configuration.nix", "homebrew.brews", true},
	{"cask", "homebrew casks", "configuration.nix", "homebrew.casks", true},
	{"tap", "homebrew taps", "configuration.nix", "homebrew.taps", true},
}

// Dir resolves the config directory: the flag, then $NIXER_DIR, then
// the current directory if it holds a flake.nix.
func Dir(flagValue string) (string, error) {
	d := flagValue
	if d == "" {
		d = os.Getenv("NIXER_DIR")
	}
	if d == "" {
		d = "."
	}
	if strings.HasPrefix(d, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		d = filepath.Join(home, d[2:])
	}
	abs, err := filepath.Abs(d)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(abs, "flake.nix")); err != nil {
		return "", fmt.Errorf("no flake.nix in %s (use --dir or set NIXER_DIR)", abs)
	}
	return abs, nil
}

// Project is the effective configuration for one config directory.
type Project struct {
	Dir      string
	Targets  []Target // display order
	Unstable string   // attribute set for unstable packages, e.g. pkgs-unstable
	Nixpkgs  string   // flake input used for search and existence checks; empty means "nixpkgs"
	Host     string   // darwinConfigurations name; empty means detect
}

// ByKind returns the target with the given kind.
func (p *Project) ByKind(kind string) (Target, bool) {
	for _, t := range p.Targets {
		if t.Kind == kind {
			return t, true
		}
	}
	return Target{}, false
}

// Seams for tests.
var (
	listHosts     = flakeHosts
	localHostName = Hostname
)

// ResolveHost picks the darwinConfigurations name: an explicit value (the
// -host flag), then nixer.json, then the flake itself (its only entry, or
// the one matching LocalHostName), then LocalHostName.
func (p *Project) ResolveHost(explicit string) (string, error) {
	h, _, err := p.ResolveHostSource(explicit)
	return h, err
}

// ResolveHostSource is ResolveHost that also says where the answer came from.
func (p *Project) ResolveHostSource(explicit string) (host, source string, err error) {
	switch {
	case explicit != "":
		return explicit, "-host flag", nil
	case p.Host != "":
		return p.Host, FileName, nil
	}
	hosts, err := listHosts(p.Dir)
	if err != nil || len(hosts) == 0 {
		// Cannot read the flake (no nix, eval error): guess from the machine.
		h, err := localHostName()
		return h, "LocalHostName (flake's darwinConfigurations unreadable)", err
	}
	if len(hosts) == 1 {
		return hosts[0], "the flake's only darwinConfigurations entry", nil
	}
	if local, err := localHostName(); err == nil {
		for _, h := range hosts {
			if h == local {
				return h, "the flake entry matching LocalHostName", nil
			}
		}
	}
	return "", "", fmt.Errorf("flake defines several darwinConfigurations (%s); pick one with -host or %q in %s",
		strings.Join(hosts, ", "), "host", FileName)
}

// flakeHosts returns the names under darwinConfigurations in the flake in dir.
func flakeHosts(dir string) ([]string, error) {
	cmd := exec.Command("nix", "eval", "--json", dir+"#darwinConfigurations",
		"--apply", "builtins.attrNames")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var hosts []string
	if err := json.Unmarshal(out, &hosts); err != nil {
		return nil, err
	}
	return hosts, nil
}

// file is the on-disk shape of nixer.json. Every field is optional.
type file struct {
	Host     string                `json:"host"`
	Unstable string                `json:"unstable"`
	Nixpkgs  string                `json:"nixpkgs"`
	Targets  map[string]targetSpec `json:"targets"`
}

type targetSpec struct {
	File string `json:"file"`
	Attr string `json:"attr"`
}

// Load reads dir/nixer.json, if present, over the built-in defaults.
// A target given in nixer.json replaces the default's file and/or attribute
// for that kind; kinds it does not mention keep their defaults.
func Load(dir string) (*Project, error) {
	p := &Project{
		Dir:      dir,
		Targets:  append([]Target(nil), DefaultTargets...),
		Unstable: DefaultUnstable,
	}
	b, err := os.ReadFile(filepath.Join(dir, FileName))
	if errors.Is(err, os.ErrNotExist) {
		return p, nil
	}
	if err != nil {
		return nil, err
	}
	var f file
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("%s: %w", FileName, err)
	}
	if f.Unstable != "" {
		if !isIdent(f.Unstable) {
			return nil, fmt.Errorf("%s: unstable %q is not a Nix attribute name", FileName, f.Unstable)
		}
		p.Unstable = f.Unstable
	}
	if f.Nixpkgs != "" {
		if !isIdent(f.Nixpkgs) {
			return nil, fmt.Errorf("%s: nixpkgs %q is not a flake input name", FileName, f.Nixpkgs)
		}
		p.Nixpkgs = f.Nixpkgs
	}
	if f.Host != "" {
		if strings.ContainsAny(f.Host, " \t\n#\"'") {
			return nil, fmt.Errorf("%s: host %q is not a valid darwinConfigurations name", FileName, f.Host)
		}
		p.Host = f.Host
	}
	for kind, spec := range f.Targets {
		i := indexOf(p.Targets, kind)
		if i < 0 {
			return nil, fmt.Errorf("%s: unknown target %q (known: %s)", FileName, kind, kinds(p.Targets))
		}
		t := &p.Targets[i]
		if spec.File != "" {
			if !filepath.IsLocal(spec.File) {
				return nil, fmt.Errorf("%s: target %q file %q must be inside the config directory", FileName, kind, spec.File)
			}
			t.File = spec.File
		}
		if spec.Attr != "" {
			if !isIdent(spec.Attr) {
				return nil, fmt.Errorf("%s: target %q attr %q is not a Nix attribute path", FileName, kind, spec.Attr)
			}
			t.Attr = spec.Attr
		}
	}
	return p, nil
}

func indexOf(ts []Target, kind string) int {
	for i, t := range ts {
		if t.Kind == kind {
			return i
		}
	}
	return -1
}

func kinds(ts []Target) string {
	var ks []string
	for _, t := range ts {
		ks = append(ks, t.Kind)
	}
	return strings.Join(ks, ", ")
}

// isIdent reports whether s is a dotted path of Nix identifiers.
func isIdent(s string) bool {
	if s == "" || strings.HasPrefix(s, ".") || strings.HasSuffix(s, ".") || strings.Contains(s, "..") {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-', r == '.', r == '\'':
		default:
			return false
		}
	}
	return true
}

// Hostname returns the macOS LocalHostName, which nix-darwin flakes are
// conventionally keyed by.
func Hostname() (string, error) {
	out, err := exec.Command("scutil", "--get", "LocalHostName").Output()
	if err != nil {
		return "", fmt.Errorf("cannot determine hostname: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
