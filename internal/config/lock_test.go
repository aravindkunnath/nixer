package config

import (
	"os"
	"path/filepath"
	"testing"
)

const lockSample = `{
  "nodes": {
    "nixpkgs": {"locked": {"type": "github", "owner": "NixOS", "repo": "nixpkgs", "rev": "abc123"}},
    "nixpkgs_2": {"locked": {"type": "github", "owner": "NixOS", "repo": "nixpkgs", "rev": "def456"}},
    "other": {"locked": {"type": "git", "url": "https://example.com/x.git", "rev": "zzz"}},
    "darwin": {"inputs": {"nixpkgs": ["nixpkgs"]}},
    "root": {"inputs": {"darwin": "darwin", "nixpkgs": "nixpkgs", "unstable": "nixpkgs_2", "weird": "other", "follow": ["darwin", "nixpkgs"]}}
  },
  "root": "root",
  "version": 7
}`

func TestPinnedRef(t *testing.T) {
	for input, want := range map[string]string{
		"nixpkgs":  "github:NixOS/nixpkgs/abc123",
		"unstable": "github:NixOS/nixpkgs/def456",
		"follow":   "github:NixOS/nixpkgs/abc123",
	} {
		if got, ok := pinnedRef([]byte(lockSample), input); !ok || got != want {
			t.Errorf("%s: got %q, %v; want %q", input, got, ok, want)
		}
	}
	for _, input := range []string{"weird", "missing"} {
		if got, ok := pinnedRef([]byte(lockSample), input); ok {
			t.Errorf("%s: want no pin, got %q", input, got)
		}
	}
	if _, ok := pinnedRef([]byte("not json"), "nixpkgs"); ok {
		t.Error("bad json accepted")
	}
}

func TestPinnedNixpkgsFallsBack(t *testing.T) {
	dir := t.TempDir()
	p := &Project{Dir: dir}
	if got := p.PinnedNixpkgs(); got != Registry {
		t.Fatalf("no lock file: %q", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "flake.lock"), []byte(lockSample), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := p.PinnedNixpkgs(); got != "github:NixOS/nixpkgs/abc123" {
		t.Fatalf("got %q", got)
	}
	p.Nixpkgs = "unstable"
	if got := p.PinnedNixpkgs(); got != "github:NixOS/nixpkgs/def456" {
		t.Fatalf("got %q", got)
	}
	p.Nixpkgs = "weird"
	if got := p.PinnedNixpkgs(); got != Registry {
		t.Fatalf("non-github pin: %q", got)
	}
}
