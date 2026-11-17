package ui

import (
	"encoding/json"
	"github.com/aravindkunnath/nixer/internal/config"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testServer(t *testing.T) (*server, http.Handler) {
	t.Helper()
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("flake.nix", "{}")
	write("home.nix", "{\n  home.packages = with pkgs; [\n    fish\n  ];\n}\n")
	write("configuration.nix", "{\n  environment.systemPackages = [ ];\n  fonts.packages = [ ];\n  homebrew = {\n    brews = [ ];\n    casks = [ ];\n    taps = [ ];\n  };\n}\n")
	p, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := &server{proj: p, dir: dir, token: "tok", port: 1234}
	return s, s.routes()
}

func do(h http.Handler, method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Host = "127.0.0.1:1234"
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

var auth = map[string]string{"X-Nixer-Token": "tok"}

func TestGuard(t *testing.T) {
	_, h := testServer(t)
	if w := do(h, "GET", "/api/lists", "", nil); w.Code != 403 {
		t.Errorf("no token: %d", w.Code)
	}
	if w := do(h, "GET", "/?t=wrong", "", nil); w.Code != 403 {
		t.Errorf("wrong page token: %d", w.Code)
	}
	r := httptest.NewRequest("GET", "/api/lists", nil)
	r.Host = "evil.example:1234"
	r.Header.Set("X-Nixer-Token", "tok")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Errorf("bad host: %d", w.Code)
	}
	if w := do(h, "POST", "/api/add", `{}`, map[string]string{"X-Nixer-Token": "tok", "Origin": "http://evil.example"}); w.Code != 403 {
		t.Errorf("bad origin: %d", w.Code)
	}
}

func TestIndexInjectsToken(t *testing.T) {
	_, h := testServer(t)
	w := do(h, "GET", "/?t=tok", "", nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), "{{TOKEN}}") || !strings.Contains(w.Body.String(), `"tok"`) {
		t.Fatalf("code=%d", w.Code)
	}
}

func TestAddRemoveRoundTrip(t *testing.T) {
	s, h := testServer(t)
	w := do(h, "POST", "/api/add", `{"kind":"cask","names":["firefox"]}`, auth)
	if w.Code != 200 {
		t.Fatalf("add: %d %s", w.Code, w.Body)
	}
	b, _ := os.ReadFile(filepath.Join(s.dir, "configuration.nix"))
	if !strings.Contains(string(b), `"firefox"`) {
		t.Fatalf("not written:\n%s", b)
	}
	w = do(h, "GET", "/api/lists", "", auth)
	var got struct {
		Lists []struct {
			Kind  string
			Items []string
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, l := range got.Lists {
		if l.Kind == "cask" && len(l.Items) == 1 && l.Items[0] == "firefox" {
			found = true
		}
	}
	if !found {
		t.Fatalf("lists: %s", w.Body)
	}
	if w = do(h, "POST", "/api/remove", `{"kind":"cask","names":["firefox"]}`, auth); w.Code != 200 {
		t.Fatalf("remove: %d %s", w.Code, w.Body)
	}
	b, _ = os.ReadFile(filepath.Join(s.dir, "configuration.nix"))
	if strings.Contains(string(b), "firefox") {
		t.Fatalf("not removed:\n%s", b)
	}
}

func TestRejectsBadNames(t *testing.T) {
	_, h := testServer(t)
	for _, body := range []string{`{"kind":"home","names":["a b"]}`, `{"kind":"home","names":["x\"; evil"]}`, `{"kind":"nope","names":["a"]}`, `{"kind":"home","names":[]}`} {
		if w := do(h, "POST", "/api/add", body, auth); w.Code != 400 {
			t.Errorf("%s: %d", body, w.Code)
		}
	}
}
