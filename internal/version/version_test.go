package version

import "testing"

func TestStringPrefersLinkerVersion(t *testing.T) {
	old := Version
	defer func() { Version = old }()
	Version = "1.2.3"
	if got := String(); got != "1.2.3" {
		t.Fatalf("got %q", got)
	}
	Version = ""
	if got := String(); got == "" {
		t.Fatal("empty fallback version")
	}
}
