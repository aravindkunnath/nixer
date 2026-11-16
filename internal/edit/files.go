package edit

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/aksappy/nixer/internal/diff"
)

// StateDir is where nixer keeps its undo backup, inside the config directory.
const StateDir = ".nixer"

func undoDir(dir string) string { return filepath.Join(dir, StateDir, "undo") }

// writeAtomic replaces path with data through a temp file and a rename, so an
// interrupted write never leaves a half-written config. The file keeps its
// mode, and a symlinked config file is replaced at its target.
func writeAtomic(path string, data []byte) error {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	mode := os.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".nixer-tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // no-op once renamed
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// saveBackup records the previous contents of files (relative to dir) as the
// single undo level, replacing any earlier one.
func saveBackup(dir string, olds map[string]string) error {
	u := undoDir(dir)
	if err := os.RemoveAll(u); err != nil {
		return err
	}
	for rel, old := range olds {
		p := filepath.Join(u, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(old), 0o600); err != nil {
			return err
		}
	}
	// Keep git from picking the backup up.
	return os.WriteFile(filepath.Join(dir, StateDir, ".gitignore"), []byte("*\n"), 0o644)
}

// ErrNothingToUndo is returned by Undo when there is no backup.
var ErrNothingToUndo = errors.New("nothing to undo")

// Undo restores the files as they were before the last add or rm, then
// forgets the backup (one level only). dryRun shows the diffs without
// restoring anything.
func Undo(dir string, dryRun bool) (Result, error) {
	var res Result
	u := undoDir(dir)
	var rels []string
	err := filepath.WalkDir(u, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			rel, _ := filepath.Rel(u, p)
			rels = append(rels, rel)
		}
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) || (err == nil && len(rels) == 0) {
		return res, ErrNothingToUndo
	}
	if err != nil {
		return res, err
	}
	sort.Strings(rels)
	type restore struct{ rel, cur, old string }
	var todo []restore
	for _, rel := range rels {
		old, err := os.ReadFile(filepath.Join(u, rel))
		if err != nil {
			return res, err
		}
		cur, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return res, err
		}
		todo = append(todo, restore{rel, string(cur), string(old)})
	}
	for _, r := range todo {
		if !dryRun {
			if err := writeAtomic(filepath.Join(dir, r.rel), []byte(r.old)); err != nil {
				return res, err
			}
		}
		res.Notes = append(res.Notes, fmt.Sprintf("restored %s", r.rel))
		if d := diff.Unified(r.rel, r.cur, r.old, 1); d != "" {
			res.Diffs = append(res.Diffs, FileDiff{r.rel, d})
		}
	}
	if dryRun {
		return res, nil
	}
	return res, os.RemoveAll(u)
}
