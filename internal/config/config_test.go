package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if body != "" {
		if err := os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestLoadDefaultsWhenAbsent(t *testing.T) {
	p, err := Load(writeConfig(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	if p.Unstable != DefaultUnstable || p.Host != "" {
		t.Errorf("unstable=%q host=%q", p.Unstable, p.Host)
	}
	if len(p.Targets) != len(DefaultTargets) {
		t.Fatalf("targets: %d", len(p.Targets))
	}
	if h, _ := p.ByKind("home"); h.File != "home.nix" || h.Attr != "home.packages" {
		t.Errorf("home: %+v", h)
	}
}

func TestLoadOverrides(t *testing.T) {
	p, err := Load(writeConfig(t, `{
		"host": "work-mac",
		"unstable": "unstable",
		"targets": {
			"home": {"file": "modules/home.nix"},
			"cask": {"attr": "homebrew.casks", "file": "darwin.nix"}
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if p.Host != "work-mac" || p.Unstable != "unstable" {
		t.Errorf("host=%q unstable=%q", p.Host, p.Unstable)
	}
	h, _ := p.ByKind("home")
	if h.File != "modules/home.nix" || h.Attr != "home.packages" || h.Quote {
		t.Errorf("home keeps default attr: %+v", h)
	}
	c, _ := p.ByKind("cask")
	if c.File != "darwin.nix" || c.Attr != "homebrew.casks" || !c.Quote {
		t.Errorf("cask: %+v", c)
	}
	if s, _ := p.ByKind("system"); s.File != "configuration.nix" {
		t.Errorf("untouched target changed: %+v", s)
	}
	// Display order is unchanged by overrides.
	for i, want := range DefaultTargets {
		if p.Targets[i].Kind != want.Kind {
			t.Errorf("order[%d]=%s", i, p.Targets[i].Kind)
		}
	}
}

func TestLoadDoesNotMutateDefaults(t *testing.T) {
	if _, err := Load(writeConfig(t, `{"targets":{"home":{"file":"x.nix"}}}`)); err != nil {
		t.Fatal(err)
	}
	if DefaultTargets[0].File != "home.nix" {
		t.Fatalf("defaults mutated: %+v", DefaultTargets[0])
	}
}

func TestLoadErrors(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"syntax":        {`{`, "nixer.json"},
		"unknown field": {`{"hostname":"x"}`, "unknown field"},
		"unknown kind":  {`{"targets":{"vim":{"file":"a.nix","attr":"a"}}}`, `unknown target "vim"`},
		"absolute file": {`{"targets":{"home":{"file":"/etc/x.nix"}}}`, "inside the config directory"},
		"escaping file": {`{"targets":{"home":{"file":"../x.nix"}}}`, "inside the config directory"},
		"bad unstable":  {`{"unstable":"a b"}`, "unstable"},
		"bad host":      {`{"host":"a b#c"}`, "host"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tc.body))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v, want %q", err, tc.want)
			}
		})
	}
}

func TestResolveHost(t *testing.T) {
	defer func(l func(string) ([]string, error), h func() (string, error)) { listHosts, localHostName = l, h }(listHosts, localHostName)
	local := func(n string, err error) func() (string, error) { return func() (string, error) { return n, err } }
	hosts := func(h []string, err error) func(string) ([]string, error) {
		return func(string) ([]string, error) { return h, err }
	}
	errBoom := os.ErrInvalid
	for name, tc := range map[string]struct {
		proj     Project
		explicit string
		flake    []string
		flakeErr error
		local    string
		localErr error
		want     string
		wantErr  string
	}{
		"flag wins":           {Project{Host: "cfg"}, "flag", []string{"a"}, nil, "loc", nil, "flag", ""},
		"nixer.json wins":     {Project{Host: "cfg"}, "", []string{"a"}, nil, "loc", nil, "cfg", ""},
		"single flake entry":  {Project{}, "", []string{"only"}, nil, "loc", nil, "only", ""},
		"single, no hostname": {Project{}, "", []string{"only"}, nil, "", errBoom, "only", ""},
		"several, one local":  {Project{}, "", []string{"a", "loc"}, nil, "loc", nil, "loc", ""},
		"several, no match":   {Project{}, "", []string{"a", "b"}, nil, "loc", nil, "", "several darwinConfigurations (a, b)"},
		"flake unreadable":    {Project{}, "", nil, errBoom, "loc", nil, "loc", ""},
		"flake empty":         {Project{}, "", []string{}, nil, "loc", nil, "loc", ""},
		"nothing works":       {Project{}, "", nil, errBoom, "", errBoom, "", "invalid argument"},
	} {
		t.Run(name, func(t *testing.T) {
			listHosts = hosts(tc.flake, tc.flakeErr)
			localHostName = local(tc.local, tc.localErr)
			got, err := tc.proj.ResolveHost(tc.explicit)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err=%v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}
