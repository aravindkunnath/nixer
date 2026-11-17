// Package version reports which nixer this is.
package version

import "runtime/debug"

// Version is set at build time (-ldflags "-X .../version.Version=1.2.3") by
// the flake. Builds made another way fall back to the module version or the
// git revision recorded by the Go toolchain.
var Version string

// String returns the version to show to users.
func String() string {
	if Version != "" {
		return Version
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	if v := bi.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	rev, dirty := "", false
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return "dev"
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	if dirty {
		rev += "-dirty"
	}
	return "dev-" + rev
}
