package rebuild

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fake puts a darwin-rebuild script first on PATH.
func fake(t *testing.T, script string) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "darwin-rebuild"), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
}

func TestBuildRunsBuildActionInScratchDir(t *testing.T) {
	here, _ := os.Getwd()
	fake(t, `echo "args: $*"; echo "cwd: $(pwd -P)"; ln -s /dev/null result`)
	var out, errb bytes.Buffer
	if err := Build(context.Background(), "/cfg#host", &out, &errb); err != nil {
		t.Fatalf("%v: %s", err, errb.String())
	}
	if !strings.Contains(out.String(), "args: build --flake /cfg#host") {
		t.Fatalf("wrong args:\n%s", out.String())
	}
	if strings.Contains(out.String(), "cwd: "+here) {
		t.Fatalf("ran in the project dir:\n%s", out.String())
	}
	if _, err := os.Lstat(filepath.Join(here, "result")); err == nil {
		t.Fatal("result link left in working dir")
	}
}

func TestBuildFailureIsAnExitError(t *testing.T) {
	fake(t, `echo "error: boom" >&2; exit 3`)
	var out, errb bytes.Buffer
	err := Build(context.Background(), "x#y", &out, &errb)
	ee, ok := err.(*exec.ExitError)
	if !ok || ee.ExitCode() != 3 || !strings.Contains(errb.String(), "boom") {
		t.Fatalf("err=%v stderr=%q", err, errb.String())
	}
}
