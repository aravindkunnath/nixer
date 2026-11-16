package edit

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aksappy/nixer/internal/config"
)

// A config with a system file only: no home-manager file, no Homebrew lists.
func minimalConfig(t *testing.T) (string, *config.Project) {
	t.Helper()
	dir := t.TempDir()
	body := "{\n  environment.systemPackages = [\n    wget\n  ];\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "configuration.nix"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return dir, p
}

func TestListsMarksMissingTargetsAbsent(t *testing.T) {
	dir, p := minimalConfig(t)
	lists, err := Lists(dir, p.Targets)
	if err != nil {
		t.Fatal(err)
	}
	present := map[string]bool{}
	for _, l := range lists {
		if !l.Absent {
			present[l.Kind] = true
		}
	}
	if len(present) != 1 || !present["system"] {
		t.Fatalf("present=%v, want only system", present)
	}
}

func TestRemoveSkipsMissingTargetsUnlessStrict(t *testing.T) {
	dir, p := minimalConfig(t)
	res, err := Remove(dir, p.Targets, []string{"wget"}, RemoveOptions{})
	if err != nil {
		t.Fatalf("lenient: %v", err)
	}
	if !res.Changed() {
		t.Fatal("wget not removed")
	}
	b, _ := os.ReadFile(filepath.Join(dir, "configuration.nix"))
	if strings.Contains(string(b), "wget") {
		t.Fatalf("still there:\n%s", b)
	}

	home, _ := p.ByKind("home")
	if _, err := Remove(dir, []config.Target{home}, []string{"x"}, RemoveOptions{Strict: true}); err == nil {
		t.Fatal("strict: want error for missing home.nix")
	}
	brew, _ := p.ByKind("brew")
	if _, err := Remove(dir, []config.Target{brew}, []string{"x"}, RemoveOptions{Strict: true}); err == nil {
		t.Fatal("strict: want error for missing brews list")
	}
}

func TestRemoveReportsMissingNameWhenTargetsAbsent(t *testing.T) {
	dir, p := minimalConfig(t)
	res, err := Remove(dir, p.Targets, []string{"nothere"}, RemoveOptions{})
	if err != nil || len(res.Missing) != 1 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestAddMatchesListStyle(t *testing.T) {
	for name, tc := range map[string]struct{ list, want string }{
		"pkgs. prefixed": {"[\n    pkgs.git\n  ]", "    pkgs.git\n    pkgs.ripgrep\n"},
		"with pkgs":      {"with pkgs; [\n    git\n  ]", "    git\n    ripgrep\n"},
		"bare":           {"[\n    git\n  ]", "    git\n    ripgrep\n"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			body := "{\n  environment.systemPackages = " + tc.list + ";\n}\n"
			if err := os.WriteFile(filepath.Join(dir, "configuration.nix"), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			p, _ := config.Load(dir)
			sys, _ := p.ByKind("system")
			if _, err := Add(dir, sys, []string{"ripgrep"}, Options{Force: true}); err != nil {
				t.Fatal(err)
			}
			b, _ := os.ReadFile(filepath.Join(dir, "configuration.nix"))
			if !strings.Contains(string(b), tc.want) {
				t.Fatalf("want %q in:\n%s", tc.want, b)
			}
		})
	}
}

func TestAddRefusesUnsupportedList(t *testing.T) {
	dir := t.TempDir()
	body := "{\n  environment.systemPackages = [ a ] ++ extra;\n}\n"
	os.WriteFile(filepath.Join(dir, "configuration.nix"), []byte(body), 0o644)
	p, _ := config.Load(dir)
	sys, _ := p.ByKind("system")
	_, err := Add(dir, sys, []string{"b"}, Options{Force: true})
	if err == nil || !strings.Contains(err.Error(), "++") {
		t.Fatalf("err=%v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "configuration.nix")); string(b) != body {
		t.Fatalf("file changed:\n%s", b)
	}
}

func sysTarget(t *testing.T) (string, config.Target) {
	t.Helper()
	dir, p := minimalConfig(t)
	sys, _ := p.ByKind("system")
	return dir, sys
}

func read(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestDryRunWritesNothing(t *testing.T) {
	dir, sys := sysTarget(t)
	before := read(t, dir, "configuration.nix")
	res, err := Add(dir, sys, []string{"curl"}, Options{Force: true, DryRun: true})
	if err != nil || !res.Changed() {
		t.Fatalf("add: %+v %v", res, err)
	}
	res, err = Remove(dir, []config.Target{sys}, []string{"wget"}, RemoveOptions{DryRun: true})
	if err != nil || !res.Changed() {
		t.Fatalf("rm: %+v %v", res, err)
	}
	if read(t, dir, "configuration.nix") != before {
		t.Fatal("dry run modified the file")
	}
	if _, err := os.Stat(filepath.Join(dir, StateDir)); err == nil {
		t.Fatal("dry run left a backup")
	}
}

func TestUndoRestoresLastEditOnly(t *testing.T) {
	dir, sys := sysTarget(t)
	orig := read(t, dir, "configuration.nix")
	if _, err := Add(dir, sys, []string{"curl"}, Options{Force: true}); err != nil {
		t.Fatal(err)
	}
	afterFirst := read(t, dir, "configuration.nix")
	if _, err := Add(dir, sys, []string{"git"}, Options{Force: true}); err != nil {
		t.Fatal(err)
	}
	// Dry-run undo changes nothing.
	if _, err := Undo(dir, true); err != nil || !strings.Contains(read(t, dir, "configuration.nix"), "git") {
		t.Fatalf("dry undo: %v", err)
	}
	res, err := Undo(dir, false)
	if err != nil || len(res.Diffs) != 1 {
		t.Fatalf("undo: %+v %v", res, err)
	}
	if got := read(t, dir, "configuration.nix"); got != afterFirst {
		t.Fatalf("undo went to:\n%s\nwant:\n%s", got, afterFirst)
	}
	// One level only.
	if _, err := Undo(dir, false); !errors.Is(err, ErrNothingToUndo) {
		t.Fatalf("second undo: %v", err)
	}
	if got := read(t, dir, "configuration.nix"); got == orig {
		t.Fatal("second undo went back further")
	}
}

func TestUndoAfterRemoveAcrossFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "home.nix"), []byte("{\n  home.packages = [\n    fish\n  ];\n}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "configuration.nix"), []byte("{\n  environment.systemPackages = [\n    fish\n  ];\n}\n"), 0o644)
	p, _ := config.Load(dir)
	if _, err := Remove(dir, p.Targets, []string{"fish"}, RemoveOptions{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(read(t, dir, "home.nix"), "fish") || strings.Contains(read(t, dir, "configuration.nix"), "fish") {
		t.Fatal("not removed")
	}
	if _, err := Undo(dir, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read(t, dir, "home.nix"), "fish") || !strings.Contains(read(t, dir, "configuration.nix"), "fish") {
		t.Fatal("both files should be restored together")
	}
}

func TestWritesKeepModeAndLeaveNoTempFiles(t *testing.T) {
	dir, sys := sysTarget(t)
	path := filepath.Join(dir, "configuration.nix")
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(dir, sys, []string{"curl"}, Options{Force: true}); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(path)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode().Perm())
	}
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".nixer-tmp-") {
			t.Fatalf("temp file left: %s", e.Name())
		}
	}
}

func TestWriteThroughSymlinkKeepsLink(t *testing.T) {
	dir, sys := sysTarget(t)
	real := filepath.Join(t.TempDir(), "real.nix")
	if err := os.Rename(filepath.Join(dir, "configuration.nix"), real); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(dir, "configuration.nix")); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(dir, sys, []string{"curl"}, Options{Force: true}); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Lstat(filepath.Join(dir, "configuration.nix")); st.Mode()&os.ModeSymlink == 0 {
		t.Fatal("symlink replaced by a regular file")
	}
	if b, _ := os.ReadFile(real); !strings.Contains(string(b), "curl") {
		t.Fatal("target not updated")
	}
}
