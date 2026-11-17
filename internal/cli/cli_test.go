package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func run(args ...string) (int, string) {
	var out, errb bytes.Buffer
	code := Run(args, &out, &errb)
	return code, out.String() + errb.String()
}

func TestDoctorReportsTargets(t *testing.T) {
	dir := fixture(t, map[string]string{
		"flake.nix":         "{}",
		"nixer.json":        `{"host":"mymac","unstable":"unstable"}`,
		"configuration.nix": "{\n  environment.systemPackages = [\n    wget\n    curl\n  ];\n}\n",
	})
	code, out := run("--dir", dir, "doctor")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	for _, want := range []string{
		"config dir: " + dir,
		"nixer.json: found",
		"host: mymac (from nixer.json)",
		"unstable set: unstable",
		"found, 2 items",
		"not present (skipped)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestDoctorFlagsProblems(t *testing.T) {
	dir := fixture(t, map[string]string{
		"flake.nix":  "{}",
		"nixer.json": `{"host":"mymac"}`,
		// Present but not a literal list: found by name, unparseable.
		"configuration.nix": "{\n  environment.systemPackages = [\n    wget\n  ;\n}\n",
	})
	code, out := run("--dir", dir, "doctor")
	if code != 1 || !strings.Contains(out, "ERROR") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
}

func TestDoctorNoFlake(t *testing.T) {
	code, out := run("--dir", t.TempDir(), "doctor")
	if code != 1 || !strings.Contains(out, "no flake.nix") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
}

func TestDoctorBadNixerJSON(t *testing.T) {
	dir := fixture(t, map[string]string{"flake.nix": "{}", "nixer.json": `{"nope":1}`})
	code, out := run("--dir", dir, "doctor")
	if code != 1 || !strings.Contains(out, "unknown field") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
}

func TestDoctorFailsWhenNothingFound(t *testing.T) {
	dir := fixture(t, map[string]string{"flake.nix": "{}", "nixer.json": `{"host":"mymac"}`})
	code, out := run("--dir", dir, "doctor")
	if code != 1 || !strings.Contains(out, "no editable lists found") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
}

func TestAddDryRunThenRealThenUndo(t *testing.T) {
	body := "{\n  environment.systemPackages = [\n    wget\n  ];\n}\n"
	dir := fixture(t, map[string]string{"flake.nix": "{}", "nixer.json": `{"host":"h"}`, "configuration.nix": body})
	path := filepath.Join(dir, "configuration.nix")
	current := func() string { b, _ := os.ReadFile(path); return string(b) }

	code, out := run("--dir", dir, "add", "-s", "-f", "-n", "curl")
	if code != 0 || !strings.Contains(out, "+    curl") || !strings.Contains(out, "Dry run") || current() != body {
		t.Fatalf("dry run: exit %d, file changed=%v\n%s", code, current() != body, out)
	}
	if code, out = run("--dir", dir, "add", "-s", "-f", "curl"); code != 0 || !strings.Contains(current(), "curl") {
		t.Fatalf("add: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "nixer undo") {
		t.Errorf("no undo hint:\n%s", out)
	}
	if code, out = run("--dir", dir, "undo"); code != 0 || current() != body {
		t.Fatalf("undo: exit %d\n%s\n%s", code, out, current())
	}
	if _, out = run("--dir", dir, "undo"); !strings.Contains(out, "Nothing to undo") {
		t.Fatalf("second undo:\n%s", out)
	}
}

func TestRmDryRun(t *testing.T) {
	body := "{\n  environment.systemPackages = [\n    wget\n  ];\n}\n"
	dir := fixture(t, map[string]string{"flake.nix": "{}", "configuration.nix": body})
	code, out := run("--dir", dir, "rm", "-n", "wget")
	b, _ := os.ReadFile(filepath.Join(dir, "configuration.nix"))
	if code != 0 || !strings.Contains(out, "-    wget") || string(b) != body {
		t.Fatalf("exit %d, unchanged=%v\n%s", code, string(b) == body, out)
	}
}

func TestAddWarnsAboutUntrackedFile(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := fixture(t, map[string]string{"flake.nix": "{}", "configuration.nix": "{\n  environment.systemPackages = [\n    wget\n  ];\n}\n"})
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("%s", out)
	}
	code, out := run("--dir", dir, "add", "-s", "-f", "curl")
	if code != 0 || !strings.Contains(out, "warning: configuration.nix not tracked by git") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	exec.Command("git", "-C", dir, "add", ".").Run()
	if _, out = run("--dir", dir, "add", "-s", "-f", "git"); strings.Contains(out, "warning") {
		t.Fatalf("tracked file still warns:\n%s", out)
	}
}

func TestVersion(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"--version"}} {
		code, out := run(args...)
		if code != 0 || !strings.HasPrefix(out, "nixer ") {
			t.Errorf("%v: exit %d %q", args, code, out)
		}
	}
}

// Layouts from the roadmap's section 2, read through the CLI.
func TestListLayouts(t *testing.T) {
	dir := fixture(t, map[string]string{
		"flake.nix": "{}",
		"home.nix":  "{ pkgs, ... }: {\n  home = {\n    packages = with pkgs; [\n      fish\n    ];\n  };\n}\n",
		"configuration.nix": "{ pkgs, ... }: {\n  environment.systemPackages = [ pkgs.git pkgs.curl ];\n" +
			"  homebrew = {\n    enable = true;\n    casks = [ \"ghostty\" \"firefox\" ];\n  };\n" +
			"  fonts.packages = [ a ] ++ extra;\n}\n",
	})
	code, out := run("--dir", dir, "list")
	if code != 1 {
		t.Fatalf("exit %d (the ++ list should fail the run):\n%s", code, out)
	}
	for _, want := range []string{"  fish", "  pkgs.git", "  pkgs.curl", "  ghostty", "  firefox"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	code, out = run("--dir", dir, "list", "-F")
	if code != 1 || !strings.Contains(out, "++") {
		t.Errorf("unsupported list should be explained, got exit %d:\n%s", code, out)
	}
}
