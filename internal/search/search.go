// Package search looks up packages in nixpkgs and Homebrew.
package search

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

const limit = 60

// Result is one search hit. Kind is the config target it belongs in for
// Homebrew hits ("brew" or "cask") and empty for nixpkgs hits.
type Result struct {
	Name        string `json:"name"`
	Version     string `json:"version,omitempty"`
	Description string `json:"description,omitempty"`
	Kind        string `json:"kind,omitempty"`
}

// Nix searches the nixpkgs flake reference ref: normally the revision pinned
// in the user's flake.lock, or "nixpkgs" for the global registry.
func Nix(ctx context.Context, ref, query string) ([]Result, error) {
	if ref == "" {
		ref = "nixpkgs"
	}
	out, err := exec.CommandContext(ctx, "nix", "search", ref, query, "--json").Output()
	if err != nil {
		return nil, fmt.Errorf("nix search failed: %w", err)
	}
	var raw map[string]struct {
		Version     string `json:"version"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, err
	}
	var res []Result
	for key, v := range raw {
		// legacyPackages.<system>.<attr>
		parts := strings.SplitN(key, ".", 3)
		if len(parts) != 3 {
			continue
		}
		res = append(res, Result{Name: parts[2], Version: v.Version, Description: v.Description})
	}
	return rank(res, query), nil
}

// Brew searches Homebrew formulae and casks.
func Brew(ctx context.Context, query string) ([]Result, error) {
	var res []Result
	for _, kind := range []struct{ flag, kind string }{{"--formula", "brew"}, {"--cask", "cask"}} {
		out, err := exec.CommandContext(ctx, "brew", "search", kind.flag, query).Output()
		if err != nil {
			if _, ok := err.(*exec.ExitError); ok {
				continue // brew exits non-zero when nothing matches
			}
			return nil, fmt.Errorf("brew search failed: %w", err)
		}
		for _, l := range strings.Split(string(out), "\n") {
			if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "==>") {
				res = append(res, Result{Name: l, Kind: kind.kind})
			}
		}
	}
	return rank(res, query), nil
}

// rank puts exact matches first, then prefix matches, then the rest by name.
func rank(res []Result, query string) []Result {
	q := strings.ToLower(query)
	score := func(r Result) int {
		n := strings.ToLower(r.Name)
		switch {
		case n == q:
			return 0
		case strings.HasPrefix(n, q):
			return 1
		default:
			return 2
		}
	}
	sort.Slice(res, func(i, j int) bool {
		if si, sj := score(res[i]), score(res[j]); si != sj {
			return si < sj
		}
		if len(res[i].Name) != len(res[j].Name) {
			return len(res[i].Name) < len(res[j].Name)
		}
		return res[i].Name < res[j].Name
	})
	if len(res) > limit {
		res = res[:limit]
	}
	return res
}
