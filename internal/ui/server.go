// Package ui serves the nixer web interface on localhost.
package ui

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/aravindkunnath/nixer/internal/config"
	"github.com/aravindkunnath/nixer/internal/edit"
	"github.com/aravindkunnath/nixer/internal/rebuild"
	"github.com/aravindkunnath/nixer/internal/search"
)

//go:embed web/index.html
var indexHTML string

type server struct {
	proj    *config.Project
	dir     string
	token   string
	port    int
	applyMu sync.Mutex
}

// Run serves the UI for the config in dir until interrupted.
func Run(proj *config.Project, port int, open bool, out io.Writer) error {
	dir := proj.Dir
	tok := make([]byte, 16)
	if _, err := rand.Read(tok); err != nil {
		return err
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return err
	}
	s := &server{proj: proj, dir: dir, token: hex.EncodeToString(tok), port: ln.Addr().(*net.TCPAddr).Port}
	srv := &http.Server{Handler: s.routes()}

	url := fmt.Sprintf("http://127.0.0.1:%d/?t=%s", s.port, s.token)
	fmt.Fprintf(out, "nixer ui for %s\n%s\nPress Ctrl-C to stop.\n", dir, url)
	if open {
		openBrowser(url)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func openBrowser(url string) {
	name := "xdg-open"
	if runtime.GOOS == "darwin" {
		name = "open"
	}
	_ = exec.Command(name, url).Start()
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("GET /api/lists", s.lists)
	mux.HandleFunc("POST /api/add", s.add)
	mux.HandleFunc("POST /api/remove", s.remove)
	mux.HandleFunc("GET /api/search", s.search)
	mux.HandleFunc("POST /api/apply", s.apply)
	return s.guard(mux)
}

// guard rejects requests that did not come from the page we served: the Host
// header must be our loopback address (defeats DNS rebinding), browsers'
// Origin must match it, and the API needs the per-run token.
func (s *server) guard(next http.Handler) http.Handler {
	hosts := map[string]bool{
		fmt.Sprintf("127.0.0.1:%d", s.port): true,
		fmt.Sprintf("localhost:%d", s.port): true,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hosts[r.Host] {
			http.Error(w, "bad host", http.StatusForbidden)
			return
		}
		if o := r.Header.Get("Origin"); o != "" && !hosts[strings.TrimPrefix(o, "http://")] {
			http.Error(w, "bad origin", http.StatusForbidden)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") && r.Header.Get("X-Nixer-Token") != s.token {
			http.Error(w, "bad token", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *server) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("t") != s.token {
		http.Error(w, "open the URL printed by `nixer ui`", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprint(w, strings.ReplaceAll(indexHTML, "{{TOKEN}}", s.token))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func (s *server) lists(w http.ResponseWriter, _ *http.Request) {
	all, err := edit.Lists(s.dir, s.proj.Targets)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	lists := all[:0]
	for _, l := range all {
		if !l.Absent {
			lists = append(lists, l)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"dir": s.dir, "unstable": s.proj.Unstable, "lists": lists})
}

type changeRequest struct {
	Kind     string   `json:"kind"`
	Names    []string `json:"names"`
	Unstable bool     `json:"unstable"`
	Force    bool     `json:"force"`
}

func decode(proj *config.Project, w http.ResponseWriter, r *http.Request) (changeRequest, config.Target, bool) {
	var req changeRequest
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, err)
		return req, config.Target{}, false
	}
	t, ok := proj.ByKind(req.Kind)
	if !ok {
		fail(w, http.StatusBadRequest, fmt.Errorf("unknown kind %q", req.Kind))
		return req, t, false
	}
	var names []string
	for _, n := range req.Names {
		if n = strings.TrimSpace(n); n != "" {
			if strings.ContainsAny(n, "\"\n\\ ") {
				fail(w, http.StatusBadRequest, fmt.Errorf("invalid name %q", n))
				return req, t, false
			}
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		fail(w, http.StatusBadRequest, errors.New("no names given"))
		return req, t, false
	}
	req.Names = names
	return req, t, true
}

func (s *server) add(w http.ResponseWriter, r *http.Request) {
	req, t, ok := decode(s.proj, w, r)
	if !ok {
		return
	}
	res, err := edit.Add(s.dir, t, req.Names, edit.Options{Unstable: req.Unstable, Force: req.Force, UnstableSet: s.proj.Unstable, Nixpkgs: s.proj.PinnedNixpkgs()})
	if err != nil {
		fail(w, http.StatusUnprocessableEntity, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *server) remove(w http.ResponseWriter, r *http.Request) {
	req, t, ok := decode(s.proj, w, r)
	if !ok {
		return
	}
	res, err := edit.Remove(s.dir, []config.Target{t}, req.Names, edit.RemoveOptions{UnstableSet: s.proj.Unstable, Strict: true})
	if err != nil {
		fail(w, http.StatusUnprocessableEntity, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *server) search(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) < 2 {
		fail(w, http.StatusBadRequest, errors.New("type at least 2 characters"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	var (
		res []search.Result
		err error
	)
	switch r.URL.Query().Get("source") {
	case "brew":
		res, err = search.Brew(ctx, q)
	default:
		res, err = search.Nix(ctx, s.proj.PinnedNixpkgs(), q)
	}
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	if res == nil {
		res = []search.Result{}
	}
	writeJSON(w, http.StatusOK, res)
}

// flushWriter flushes after every write so output streams to the browser.
type flushWriter struct {
	w http.ResponseWriter
}

func (f flushWriter) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	if fl, ok := f.w.(http.Flusher); ok {
		fl.Flush()
	}
	return n, err
}

// apply runs darwin-rebuild and streams its output. If sudo needs a password
// (there is no terminal here) it falls back to the macOS admin prompt, whose
// output arrives once the build finishes.
func (s *server) apply(w http.ResponseWriter, r *http.Request) {
	if !s.applyMu.TryLock() {
		fail(w, http.StatusConflict, errors.New("an apply is already running"))
		return
	}
	defer s.applyMu.Unlock()

	host, err := s.proj.ResolveHost("")
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	flake := s.dir + "#" + host
	out := flushWriter{w}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)

	// Build first, without sudo, so a bad edit never reaches activation.
	fmt.Fprintf(out, "$ darwin-rebuild build --flake %s\n", flake)
	if err := rebuild.Build(r.Context(), flake, out, out); err != nil {
		status := 1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			status = ee.ExitCode()
		} else {
			fmt.Fprintln(out, err)
		}
		fmt.Fprintf(out, "\nBuild failed; nothing was activated.\n[exit status %d]\n", status)
		return
	}
	fmt.Fprintln(out, "Build succeeded; activating.")

	var cmd *exec.Cmd
	if exec.Command("sudo", "-n", "true").Run() == nil {
		cmd = exec.CommandContext(r.Context(), "sudo", "-n", "darwin-rebuild", "switch", "--flake", flake)
		fmt.Fprintf(out, "$ sudo darwin-rebuild switch --flake %s\n", flake)
	} else {
		bin, err := exec.LookPath("darwin-rebuild")
		if err != nil || runtime.GOOS != "darwin" || strings.ContainsAny(flake+bin, `'"\`) {
			fmt.Fprintf(out, "sudo needs a password and the admin prompt is unavailable.\nRun in a terminal: nixer apply\n[exit status 1]\n")
			return
		}
		fmt.Fprintf(out, "sudo needs a password; asking macOS for administrator access...\n$ darwin-rebuild switch --flake %s\n", flake)
		// Mimic what sudo sets so root can read the user's git repo (libgit2
		// trusts the owner named by SUDO_UID) and use the user's HOME.
		home, _ := os.UserHomeDir()
		env := fmt.Sprintf("HOME='%s' SUDO_UID=%d SUDO_GID=%d SUDO_USER='%s' PATH='%s:/run/current-system/sw/bin:/nix/var/nix/profiles/default/bin:/usr/bin:/bin:/usr/sbin:/sbin'",
			home, os.Getuid(), os.Getgid(), os.Getenv("USER"), filepath.Dir(bin))
		if strings.ContainsAny(env, `"\`) {
			fmt.Fprintf(out, "unsupported characters in environment.\nRun in a terminal: nixer apply\n[exit status 1]\n")
			return
		}
		script := fmt.Sprintf(`do shell script "/usr/bin/env %s '%s' switch --flake '%s' 2>&1" with administrator privileges`, env, bin, flake)
		cmd = exec.CommandContext(r.Context(), "osascript", "-e", script)
	}
	cmd.Stdout, cmd.Stderr = out, out
	status := 0
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			status = ee.ExitCode()
		} else {
			status = 1
			fmt.Fprintln(out, err)
		}
	}
	fmt.Fprintf(out, "\n[exit status %d]\n", status)
}
