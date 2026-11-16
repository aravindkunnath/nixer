// Package rebuild runs `darwin-rebuild build`, the no-sudo dry run that
// proves a config evaluates and builds before anything is activated.
package rebuild

import (
	"context"
	"os"
	"os/exec"
)

// Build runs `darwin-rebuild build --flake flake` as the current user. The
// build leaves a ./result symlink in its working directory, so it runs in a
// scratch directory that is removed afterwards. Output goes to out; stdin is
// left unconnected.
func Build(ctx context.Context, flake string, out, errw interface{ Write([]byte) (int, error) }) error {
	scratch, err := os.MkdirTemp("", "nixer-build-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)
	cmd := exec.CommandContext(ctx, "darwin-rebuild", "build", "--flake", flake)
	cmd.Dir = scratch
	cmd.Stdout, cmd.Stderr = out, errw
	return cmd.Run()
}
