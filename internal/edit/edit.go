// Package edit applies package additions and removals to the config files on
// disk. It is shared by the command line and the web UI.
package edit

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/aravindkunnath/nixer/internal/config"
	"github.com/aravindkunnath/nixer/internal/diff"
	"github.com/aravindkunnath/nixer/internal/nixedit"
)

// FileDiff is the change made to one file.
type FileDiff struct {
	File string `json:"file"`
	Diff string `json:"diff"`
}

// Result describes what an operation did.
type Result struct {
	Notes   []string   `json:"notes"`
	Missing []string   `json:"missing"`
	Diffs   []FileDiff `json:"diffs"`
}

// Changed reports whether any file was modified.
func (r Result) Changed() bool { return len(r.Diffs) > 0 }

// Options tweak Add.
type Options struct {
	Unstable bool // write <UnstableSet>.<name>
	Force    bool // skip the nixpkgs existence check

	// UnstableSet is the attribute set unstable packages come from.
	// Empty means config.DefaultUnstable.
	UnstableSet string

	// Nixpkgs is the flake reference the existence check uses (the pin from
	// flake.lock). Empty means config.Registry.
	Nixpkgs string

	// DryRun computes the diff without writing or backing anything up.
	DryRun bool
}

// List is the current contents of one target.
type List struct {
	Kind  string   `json:"kind"`
	Label string   `json:"label"`
	File  string   `json:"file"`
	Items []string `json:"items"`
	Error string   `json:"error,omitempty"`
	// Absent means this config simply has no such file or list (no Homebrew,
	// no home-manager); Error says what was missing.
	Absent bool `json:"absent,omitempty"`
}

// Lists reads every target in ts. Missing files and lists are reported as
// Absent entries, not errors.
func Lists(dir string, ts []config.Target) ([]List, error) {
	var out []List
	for _, t := range ts {
		l := List{Kind: t.Kind, Label: t.Label, File: t.File, Items: []string{}}
		b, err := os.ReadFile(filepath.Join(dir, t.File))
		if errors.Is(err, fs.ErrNotExist) {
			l.Absent, l.Error = true, t.File+" does not exist"
			out = append(out, l)
			continue
		}
		if err != nil {
			return nil, err
		}
		items, err := nixedit.List(string(b), t.Attr)
		if err != nil {
			l.Error = err.Error()
			l.Absent = errors.Is(err, nixedit.ErrNoList)
		} else if items != nil {
			l.Items = items
		}
		out = append(out, l)
	}
	return out, nil
}

// Add adds names to the list for target t and writes the file.
func Add(dir string, t config.Target, names []string, opt Options) (Result, error) {
	var res Result
	if opt.UnstableSet == "" {
		opt.UnstableSet = config.DefaultUnstable
	}
	path := filepath.Join(dir, t.File)
	b, err := os.ReadFile(path)
	if err != nil {
		return res, err
	}
	old := string(b)
	text := old
	items, err := nixedit.List(text, t.Attr)
	if err != nil {
		return res, fmt.Errorf("%s: %w", t.File, err)
	}
	existing := map[string]bool{}
	for _, i := range items {
		existing[nixedit.Normalize(i, opt.UnstableSet)] = true
	}
	for _, name := range names {
		if existing[name] {
			res.Notes = append(res.Notes, fmt.Sprintf("%s already in %s", name, t.Label))
			continue
		}
		if !t.Quote && !opt.Force && !pkgExists(opt.Nixpkgs, name) {
			return res, fmt.Errorf("%q not found in nixpkgs; check the name or force the add", name)
		}
		entry := name
		switch {
		case t.Quote:
			entry = `"` + name + `"`
		case opt.Unstable:
			entry = opt.UnstableSet + "." + name
		default:
			// Write names the way the list does: pkgs.foo when it has no `with pkgs;`.
			prefix, err := nixedit.Style(text, t.Attr, opt.UnstableSet)
			if err != nil {
				return res, fmt.Errorf("%s: %w", t.File, err)
			}
			entry = prefix + name
		}
		if text, err = nixedit.Add(text, t.Attr, entry); err != nil {
			return res, err
		}
		existing[name] = true
		res.Notes = append(res.Notes, fmt.Sprintf("added %s to %s", name, t.Label))
	}
	if text == old {
		return res, nil
	}
	if !opt.DryRun {
		if err := saveBackup(dir, map[string]string{t.File: old}); err != nil {
			return res, fmt.Errorf("cannot save undo backup: %w", err)
		}
		if err := writeAtomic(path, []byte(text)); err != nil {
			return res, err
		}
	}
	res.Diffs = append(res.Diffs, FileDiff{t.File, diff.Unified(t.File, old, text, 1)})
	return res, nil
}

// RemoveOptions tweak Remove.
type RemoveOptions struct {
	// UnstableSet is the project's unstable attribute set ("" for the default).
	UnstableSet string
	// Strict makes targets whose file or list does not exist an error (the
	// user named that target); otherwise they are skipped (searching all).
	Strict bool
	// DryRun computes the diffs without writing or backing anything up.
	DryRun bool
}

// Remove removes names from every target in ts and writes the changed files.
func Remove(dir string, ts []config.Target, names []string, opt RemoveOptions) (Result, error) {
	unstableSet, strict := opt.UnstableSet, opt.Strict
	type pending struct{ old, text string }
	var res Result
	files := map[string]*pending{}
	gone := map[string]bool{}
	var order []string
	for _, name := range names {
		found := false
		for _, t := range ts {
			if gone[t.File] {
				continue
			}
			p := files[t.File]
			if p == nil {
				b, err := os.ReadFile(filepath.Join(dir, t.File))
				if errors.Is(err, fs.ErrNotExist) && !strict {
					gone[t.File] = true
					continue
				}
				if err != nil {
					return res, err
				}
				p = &pending{string(b), string(b)}
				files[t.File] = p
				order = append(order, t.File)
			}
			out, ok, err := nixedit.Remove(p.text, t.Attr, name, unstableSet)
			if errors.Is(err, nixedit.ErrNoList) && !strict {
				continue
			}
			if err != nil {
				return res, fmt.Errorf("%s: %w", t.File, err)
			}
			if ok {
				p.text = out
				found = true
				res.Notes = append(res.Notes, fmt.Sprintf("removed %s from %s", name, t.Label))
			}
		}
		if !found {
			res.Missing = append(res.Missing, name)
		}
	}
	changed := map[string]string{}
	for _, file := range order {
		if p := files[file]; p.old != p.text {
			changed[file] = p.old
		}
	}
	if len(changed) > 0 && !opt.DryRun {
		if err := saveBackup(dir, changed); err != nil {
			return res, fmt.Errorf("cannot save undo backup: %w", err)
		}
	}
	for _, file := range order {
		p := files[file]
		if p.old == p.text {
			continue
		}
		if !opt.DryRun {
			if err := writeAtomic(filepath.Join(dir, file), []byte(p.text)); err != nil {
				return res, err
			}
		}
		res.Diffs = append(res.Diffs, FileDiff{file, diff.Unified(file, p.old, p.text, 1)})
	}
	return res, nil
}

// pkgExists checks that nixpkgs (the flake reference ref, normally the pin
// from the user's flake.lock) has an attribute with this name. If nix is
// unavailable or slow it errs on the side of allowing the add.
func pkgExists(ref, name string) bool {
	if ref == "" {
		ref = config.Registry
	}
	if _, err := exec.LookPath("nix"); err != nil {
		return true
	}
	done := make(chan bool, 1)
	c := exec.Command("nix", "eval", "--raw", ref+"#"+name+".name")
	go func() { done <- c.Run() == nil }()
	select {
	case ok := <-done:
		return ok
	case <-time.After(2 * time.Minute):
		_ = c.Process.Kill()
		return true
	}
}
