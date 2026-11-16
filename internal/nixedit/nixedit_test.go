package nixedit

import (
	"errors"
	"strings"
	"testing"
)

const sample = `{ pkgs, ... }: {
  home.packages = with pkgs; [
    # shell
    fish
    pkgs-unstable.neovim # editor
    # trailing comment
  ];
  homebrew.casks = [
    "ghostty"
  ];
  environment.systemPackages =
    [ pkgs.vim
    ];
  empty = [ ];
}
`

func TestList(t *testing.T) {
	got, err := List(sample, "home.packages")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "fish,pkgs-unstable.neovim" {
		t.Fatalf("got %v", got)
	}
}

func TestListMissing(t *testing.T) {
	if _, err := List(sample, "nope"); err == nil {
		t.Fatal("expected error")
	}
}

func TestAddKeepsCommentsAndIndent(t *testing.T) {
	out, err := Add(sample, "home.packages", "ripgrep")
	if err != nil {
		t.Fatal(err)
	}
	want := "    pkgs-unstable.neovim # editor\n    ripgrep\n    # trailing comment\n"
	if !strings.Contains(out, want) {
		t.Fatalf("missing %q in:\n%s", want, out)
	}
}

func TestAddQuoted(t *testing.T) {
	out, _ := Add(sample, "homebrew.casks", `"firefox"`)
	if !strings.Contains(out, "    \"ghostty\"\n    \"firefox\"\n  ];") {
		t.Fatalf("unexpected:\n%s", out)
	}
}

func TestAddInlineFirstItem(t *testing.T) {
	out, err := Add(sample, "environment.systemPackages", "git")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "    [\n      pkgs.vim\n      git\n    ];") {
		t.Fatalf("unexpected:\n%s", out)
	}
}

func TestAddToEmpty(t *testing.T) {
	out, err := Add(sample, "empty", "x")
	if err != nil {
		t.Fatal(err)
	}
	items, _ := List(out, "empty")
	if len(items) != 1 || items[0] != "x" {
		t.Fatalf("got %v in:\n%s", items, out)
	}
}

func TestRemove(t *testing.T) {
	out, ok, err := Remove(sample, "home.packages", "neovim")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if strings.Contains(out, "neovim") || !strings.Contains(out, "# trailing comment") {
		t.Fatalf("unexpected:\n%s", out)
	}
	if _, ok, _ := Remove(sample, "home.packages", "nothere"); ok {
		t.Fatal("removed nonexistent")
	}
}

func TestRemoveQuoted(t *testing.T) {
	out, ok, _ := Remove(sample, "homebrew.casks", "ghostty")
	if !ok || strings.Contains(out, "ghostty") {
		t.Fatalf("unexpected:\n%s", out)
	}
}

func mustList(t *testing.T, text, attr string) string {
	t.Helper()
	items, err := List(text, attr)
	if err != nil {
		t.Fatalf("List(%s): %v", attr, err)
	}
	return strings.Join(items, ",")
}

func TestSeveralItemsOnOneLine(t *testing.T) {
	text := "{\n  home.packages = [ git curl wget ];\n}\n"
	if got := mustList(t, text, "home.packages"); got != "git,curl,wget" {
		t.Fatalf("got %q", got)
	}
	out, ok, err := Remove(text, "home.packages", "curl")
	if err != nil || !ok || out != "{\n  home.packages = [ git wget ];\n}\n" {
		t.Fatalf("remove middle: ok=%v err=%v\n%s", ok, err, out)
	}
	out, _, _ = Remove(text, "home.packages", "wget")
	if out != "{\n  home.packages = [ git curl ];\n}\n" {
		t.Fatalf("remove last:\n%s", out)
	}
	out, _, _ = Remove(text, "home.packages", "git")
	if out != "{\n  home.packages = [ curl wget ];\n}\n" {
		t.Fatalf("remove first:\n%s", out)
	}
}

func TestAddToSingleLineList(t *testing.T) {
	out, err := Add("{\n  home.packages = [ git curl ];\n}\n", "home.packages", "wget")
	if err != nil || out != "{\n  home.packages = [ git curl wget ];\n}\n" {
		t.Fatalf("err=%v\n%s", err, out)
	}
	out, _ = Add("{\n  x = [ ];\n}\n", "x", "a")
	if out != "{\n  x = [ a ];\n}\n" {
		t.Fatalf("empty:\n%s", out)
	}
}

func TestRemoveOneOfSeveralOnMultilineLine(t *testing.T) {
	text := "{\n  x = [\n    a b\n    c\n  ];\n}\n"
	out, ok, _ := Remove(text, "x", "b")
	if !ok || out != "{\n  x = [\n    a\n    c\n  ];\n}\n" {
		t.Fatalf("got:\n%s", out)
	}
}

func TestQuotedItemsWithSpaces(t *testing.T) {
	text := "{\n  homebrew.casks = [ \"google chrome\" \"firefox\" ];\n}\n"
	if got := mustList(t, text, "homebrew.casks"); got != "google chrome,firefox" {
		t.Fatalf("got %q", got)
	}
}

func TestBothAttributeSpellings(t *testing.T) {
	nested := "{\n  home = {\n    username = \"a\";\n    packages = with pkgs; [\n      fish\n    ];\n  };\n}\n"
	if got := mustList(t, nested, "home.packages"); got != "fish" {
		t.Fatalf("nested list: %q", got)
	}
	out, err := Add(nested, "home.packages", "ripgrep")
	if err != nil || !strings.Contains(out, "      fish\n      ripgrep\n    ];") {
		t.Fatalf("nested add: %v\n%s", err, out)
	}
	out, ok, _ := Remove(out, "home.packages", "fish")
	if !ok || mustList(t, out, "home.packages") != "ripgrep" {
		t.Fatalf("nested remove:\n%s", out)
	}
	hb := "{\n  homebrew = {\n    enable = true;\n    onActivation = { cleanup = \"zap\"; };\n    casks = [\n      \"ghostty\"\n    ];\n  };\n}\n"
	if got := mustList(t, hb, "homebrew.casks"); got != "ghostty" {
		t.Fatalf("homebrew nested: %q", got)
	}
	if _, err := List(hb, "homebrew.brews"); !errors.Is(err, ErrNoList) {
		t.Fatalf("want ErrNoList, got %v", err)
	}
	// Dotted form still works.
	if got := mustList(t, sample, "homebrew.casks"); got != "ghostty" {
		t.Fatalf("dotted: %q", got)
	}
}

func TestNestedListMustBeDirectChild(t *testing.T) {
	text := "{\n  home = {\n    other = { packages = [ wrong ]; };\n  };\n}\n"
	if _, err := List(text, "home.packages"); !errors.Is(err, ErrNoList) {
		t.Fatalf("want ErrNoList, got %v", err)
	}
}

func TestCommentsAndStringsDoNotConfuseBrackets(t *testing.T) {
	text := "{\n  x = [\n    a\n    /* ] fake close */\n    \"b]\"\n    # ] another\n    c\n  ];\n  y = [ z ];\n}\n"
	if got := mustList(t, text, "x"); got != "a,b],c" {
		t.Fatalf("got %q", got)
	}
	out, err := Add(text, "x", "d")
	if err != nil || !strings.Contains(out, "    c\n    d\n  ];") {
		t.Fatalf("add: %v\n%s", err, out)
	}
}

func TestAttrInsideCommentOrStringIsIgnored(t *testing.T) {
	text := "{\n  /* x = [ fake ]; */\n  desc = ''\n    x = [ alsofake ];\n  '';\n  x = [ real ];\n}\n"
	if got := mustList(t, text, "x"); got != "real" {
		t.Fatalf("got %q", got)
	}
	text = "{\n  s = ''\n    ] ''${x} '''\n  '';\n  x = [ a ];\n}\n"
	if got := mustList(t, text, "x"); got != "a" {
		t.Fatalf("got %q", got)
	}
}

func TestRefusesWhatItCannotParse(t *testing.T) {
	for name, text := range map[string]string{
		"concat after":  "{\n  x = [ a ] ++ lib.optionals true [ b ];\n}\n",
		"concat before": "{\n  x = base ++ [ a ];\n}\n",
		"optionals":     "{\n  x = lib.optionals stdenv.isDarwin [ a ];\n}\n",
		"override":      "{\n  x = [\n    a\n    (b.override { c = 1; })\n  ];\n}\n",
		"nested list":   "{\n  x = [ a [ b ] ];\n}\n",
		"interpolation": "{\n  x = [ \"a${b}\" ];\n}\n",
		"path":          "{\n  x = [ ./pkg.nix ];\n}\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := List(text, "x"); !errors.Is(err, ErrUnsupported) {
				t.Errorf("List: want ErrUnsupported, got %v", err)
			}
			if _, err := Add(text, "x", "n"); !errors.Is(err, ErrUnsupported) {
				t.Errorf("Add: want ErrUnsupported, got %v", err)
			}
			if _, _, err := Remove(text, "x", "a"); !errors.Is(err, ErrUnsupported) {
				t.Errorf("Remove: want ErrUnsupported, got %v", err)
			}
		})
	}
	_, err := List("{\n  x = [\n    a\n    (b.override { })\n  ];\n}\n", "x")
	if err == nil || !strings.Contains(err.Error(), "line 4") {
		t.Fatalf("error should name the line: %v", err)
	}
}

func TestStyle(t *testing.T) {
	for name, tc := range map[string]struct{ text, want string }{
		"with pkgs":        {"{ x = with pkgs; [ a b ]; }", ""},
		"pkgs prefixed":    {"{ x = [ pkgs.a pkgs.b ]; }", "pkgs."},
		"mixed, majority":  {"{ x = [ pkgs.a pkgs.b c ]; }", "pkgs."},
		"bare items":       {"{ x = [ a b ]; }", ""},
		"unstable ignored": {"{ x = [ pkgs-unstable.a pkgs.b ]; }", "pkgs."},
		"empty":            {"{ x = [ ]; }", ""},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := Style(tc.text, "x", "pkgs-unstable")
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}
