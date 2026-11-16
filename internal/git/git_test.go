package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func repo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "t@example.com"}, {"config", "user.name", "t"}, {"config", "commit.gpgsign", "false"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", args, out)
		}
	}
	return dir
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestInRepo(t *testing.T) {
	if InRepo(t.TempDir()) {
		t.Error("plain dir reported as repo")
	}
	if !InRepo(repo(t)) {
		t.Error("repo not detected")
	}
}

func TestUntrackedAndCommit(t *testing.T) {
	dir := repo(t)
	write(t, dir, "a.nix", "1")
	write(t, dir, "b.nix", "1")
	if got := Untracked(dir, []string{"a.nix", "b.nix"}); len(got) != 2 {
		t.Fatalf("untracked: %v", got)
	}
	if err := Commit(dir, []string{"a.nix"}, "nixer: test"); err != nil {
		t.Fatal(err)
	}
	if got := Untracked(dir, []string{"a.nix", "b.nix"}); len(got) != 1 || got[0] != "b.nix" {
		t.Fatalf("after commit: %v", got)
	}
	// Only a.nix was committed.
	out, _ := run(dir, "show", "--stat", "--format=%s", "HEAD")
	if !strings.Contains(out, "nixer: test") || strings.Contains(out, "b.nix") {
		t.Fatalf("commit:\n%s", out)
	}
}
