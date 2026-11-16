// Package git is the little nixer needs from git: flakes only see tracked
// files, so it warns about untracked edits and can commit a change.
package git

import (
	"fmt"
	"os/exec"
	"strings"
)

func run(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// InRepo reports whether dir is inside a git work tree (and git exists).
func InRepo(dir string) bool {
	out, err := run(dir, "rev-parse", "--is-inside-work-tree")
	return err == nil && out == "true"
}

// Untracked returns those of files (relative to dir) that git does not
// track. Files that are git-ignored are included: a flake cannot see them
// either.
func Untracked(dir string, files []string) []string {
	var out []string
	for _, f := range files {
		if _, err := run(dir, "ls-files", "--error-unmatch", "--", f); err != nil {
			out = append(out, f)
		}
	}
	return out
}

// Commit stages exactly files and commits them with msg. Other staged or
// modified files are left alone.
func Commit(dir string, files []string, msg string) error {
	if out, err := run(dir, append([]string{"add", "--"}, files...)...); err != nil {
		return fmt.Errorf("git add: %s", out)
	}
	args := append([]string{"commit", "-m", msg, "--"}, files...)
	if out, err := run(dir, args...); err != nil {
		return fmt.Errorf("git commit: %s", out)
	}
	return nil
}
