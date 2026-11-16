package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// DefaultNixpkgsInput is the flake input whose pin is used for searching and
// existence checks.
const DefaultNixpkgsInput = "nixpkgs"

// Registry is the flake reference used when the lock file cannot be read: the
// global registry's nixpkgs, which may differ from what the config builds.
const Registry = "nixpkgs"

// PinnedNixpkgs returns a flake reference to the nixpkgs revision locked in
// the project's flake.lock, so "exists" and search results match what will be
// built. It falls back to Registry when the lock file is missing or the input
// is not a plain GitHub pin.
func (p *Project) PinnedNixpkgs() string {
	b, err := os.ReadFile(filepath.Join(p.Dir, "flake.lock"))
	if err != nil {
		return Registry
	}
	input := p.Nixpkgs
	if input == "" {
		input = DefaultNixpkgsInput
	}
	if ref, ok := pinnedRef(b, input); ok {
		return ref
	}
	return Registry
}

type lockFile struct {
	Root  string `json:"root"`
	Nodes map[string]struct {
		Inputs map[string]json.RawMessage `json:"inputs"`
		Locked struct {
			Type  string `json:"type"`
			Owner string `json:"owner"`
			Repo  string `json:"repo"`
			Rev   string `json:"rev"`
		} `json:"locked"`
	} `json:"nodes"`
}

func pinnedRef(lock []byte, input string) (string, bool) {
	var lf lockFile
	if json.Unmarshal(lock, &lf) != nil {
		return "", false
	}
	root := lf.Root
	if root == "" {
		root = "root"
	}
	node, ok := lf.resolve(root, input, 0)
	if !ok {
		return "", false
	}
	l := lf.Nodes[node].Locked
	if l.Type != "github" || l.Owner == "" || l.Repo == "" || l.Rev == "" {
		return "", false
	}
	return fmt.Sprintf("github:%s/%s/%s", l.Owner, l.Repo, l.Rev), true
}

// resolve follows an input of node to the node it points at. An input is
// either a node name or, for `follows`, a path of input names from the root.
func (lf *lockFile) resolve(node, input string, depth int) (string, bool) {
	if depth > 10 {
		return "", false
	}
	raw, ok := lf.Nodes[node].Inputs[input]
	if !ok {
		return "", false
	}
	var name string
	if json.Unmarshal(raw, &name) == nil {
		return name, true
	}
	var path []string
	if json.Unmarshal(raw, &path) != nil || len(path) == 0 {
		return "", false
	}
	cur := lf.Root
	if cur == "" {
		cur = "root"
	}
	for _, step := range path {
		next, ok := lf.resolve(cur, step, depth+1)
		if !ok {
			return "", false
		}
		cur = next
	}
	return cur, true
}
