package diff

import (
	"strings"
	"testing"
)

func TestUnified(t *testing.T) {
	a := "a\nb\nc\nd\ne\n"
	b := "a\nb\nX\nc\nd\ne\n"
	got := Unified("f", a, b, 1)
	want := "--- f\n+++ f\n@@ -2,2 +2,3 @@\n b\n+X\n c\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestEqual(t *testing.T) {
	if Unified("f", "a\n", "a\n", 1) != "" {
		t.Fatal("expected empty diff")
	}
}

func TestRemoval(t *testing.T) {
	got := Unified("f", "a\nb\nc\n", "a\nc\n", 1)
	if !strings.Contains(got, "-b\n") {
		t.Fatalf("got:\n%s", got)
	}
}
