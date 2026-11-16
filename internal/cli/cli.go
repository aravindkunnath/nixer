// Package cli implements the nixer command line.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/aksappy/nixer/internal/config"
	"github.com/aksappy/nixer/internal/edit"
	"github.com/aksappy/nixer/internal/git"
	"github.com/aksappy/nixer/internal/rebuild"
	"github.com/aksappy/nixer/internal/ui"
)

const usageText = `nixer: add/remove packages in your nix-darwin config without opening the files.

Usage:
  nixer [--dir DIR] <command> [flags] [args]

Commands:
  add <name>...      add packages (-n: show the diff only, -a: apply, -m: apply + git commit)
  rm <name>...       remove packages (searches all lists unless a target is given)
  list               list managed packages
  search <term>...   search nixpkgs as pinned in flake.lock (-b: Homebrew)
  apply              build, then sudo darwin-rebuild switch
  undo               restore the files as they were before the last add/rm
  clean              garbage-collect the Nix store (--old: also old generations, -n: dry run)
  ui                 manage everything in a local web page
  edit [file]        open a config file in $EDITOR
  doctor             show what nixer detected (run this first, and in bug reports)

Targets for add/rm/list (default: home-manager packages):
  -H home.packages   -s environment.systemPackages   -F fonts.packages
  -b homebrew brews  -c homebrew casks               -t homebrew taps

The config directory is --dir, $NIXER_DIR, or the current directory.
`

type exitError int

func (e exitError) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

type app struct {
	out, err io.Writer
}

// Run executes nixer with args and returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	a := &app{out: stdout, err: stderr}
	err := a.run(args)
	var ee exitError
	switch {
	case err == nil, errors.Is(err, flag.ErrHelp):
		return 0
	case errors.As(err, &ee):
		return int(ee)
	default:
		fmt.Fprintln(stderr, "nixer:", err)
		return 1
	}
}

func (a *app) run(args []string) error {
	global := flag.NewFlagSet("nixer", flag.ContinueOnError)
	global.SetOutput(a.err)
	global.Usage = func() { fmt.Fprint(a.err, usageText) }
	dir := global.String("dir", "", "config directory")
	if err := global.Parse(args); err != nil {
		return err
	}
	rest := global.Args()
	if len(rest) == 0 {
		global.Usage()
		return exitError(2)
	}
	cmd, rest := rest[0], rest[1:]

	commands := map[string]func(string, []string) error{
		"add": a.add, "rm": a.rm, "remove": a.rm, "list": a.list, "ls": a.list,
		"search": a.search, "apply": a.apply, "edit": a.edit, "ui": a.ui, "doctor": a.doctor, "undo": a.undo, "clean": a.clean,
	}
	if cmd == "help" {
		global.Usage()
		return nil
	}
	fn, ok := commands[cmd]
	if !ok {
		return fmt.Errorf("unknown command %q (try `nixer help`)", cmd)
	}
	return fn(*dir, rest)
}

// parse is flag parsing that allows flags after positional arguments.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

func (a *app) flagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(a.err)
	return fs
}

type kindFlags struct{ home, system, font, brew, cask, tap bool }

func (k *kindFlags) register(fs *flag.FlagSet) {
	fs.BoolVar(&k.home, "H", false, "home.nix home.packages")
	fs.BoolVar(&k.system, "s", false, "environment.systemPackages")
	fs.BoolVar(&k.font, "F", false, "fonts.packages")
	fs.BoolVar(&k.brew, "b", false, "homebrew brews")
	fs.BoolVar(&k.cask, "c", false, "homebrew casks")
	fs.BoolVar(&k.tap, "t", false, "homebrew taps")
}

func (k *kindFlags) selected(p *config.Project) []config.Target {
	on := map[string]bool{"home": k.home, "system": k.system, "font": k.font,
		"brew": k.brew, "cask": k.cask, "tap": k.tap}
	var ts []config.Target
	for _, t := range p.Targets {
		if on[t.Kind] {
			ts = append(ts, t)
		}
	}
	return ts
}

func (a *app) report(res edit.Result) {
	for _, n := range res.Notes {
		fmt.Fprintln(a.out, n)
	}
	for _, m := range res.Missing {
		fmt.Fprintf(a.err, "%s not found\n", m)
	}
	for _, d := range res.Diffs {
		fmt.Fprint(a.out, d.Diff)
	}
}

func (a *app) add(dirFlag string, args []string) error {
	fs := a.flagSet("add")
	var k kindFlags
	k.register(fs)
	unstable := fs.Bool("u", false, "use pkgs-unstable.<name>")
	force := fs.Bool("f", false, "skip nixpkgs existence check")
	var ef editFlags
	ef.register(fs)
	names, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return errors.New("add: no package names given")
	}
	p, err := a.project(dirFlag)
	if err != nil {
		return err
	}
	ts := k.selected(p)
	switch len(ts) {
	case 0:
		ts = p.Targets[:1]
	case 1:
	default:
		return errors.New("add: pick only one target")
	}
	res, err := edit.Add(p.Dir, ts[0], names, edit.Options{
		Unstable: *unstable, Force: *force, UnstableSet: p.Unstable,
		Nixpkgs: p.PinnedNixpkgs(), DryRun: ef.dry,
	})
	a.report(res)
	if err != nil {
		return err
	}
	return a.afterEdit(p, res, ef, "add "+strings.Join(names, " "))
}

// editFlags are the flags add and rm share for what happens after the edit.
type editFlags struct{ dry, apply, commit bool }

func (e *editFlags) register(fs *flag.FlagSet) {
	fs.BoolVar(&e.dry, "n", false, "dry run: print the diff, write nothing")
	fs.BoolVar(&e.apply, "a", false, "build and switch afterwards")
	fs.BoolVar(&e.commit, "m", false, "after a successful apply, git-commit the change (implies -a)")
}

// afterEdit handles what follows a successful edit: dry-run notice, git
// warnings, apply, commit.
func (a *app) afterEdit(p *config.Project, res edit.Result, ef editFlags, what string) error {
	if !res.Changed() {
		return nil
	}
	if ef.dry {
		fmt.Fprintln(a.out, "\nDry run: no files were written.")
		return nil
	}
	var files []string
	for _, d := range res.Diffs {
		files = append(files, d.File)
	}
	if git.InRepo(p.Dir) {
		if un := git.Untracked(p.Dir, files); len(un) > 0 {
			fmt.Fprintf(a.err, "warning: %s not tracked by git; flakes ignore untracked files, so `git add` %s before applying\n",
				strings.Join(un, ", "), pluralIt(len(un)))
		}
	}
	if ef.commit {
		ef.apply = true
	}
	if !ef.apply {
		fmt.Fprintln(a.out, "\nRun `nixer apply` to activate (`nixer undo` reverts this edit).")
		return nil
	}
	if err := a.rebuild(p, ""); err != nil {
		return err
	}
	if ef.commit {
		if !git.InRepo(p.Dir) {
			fmt.Fprintln(a.err, "warning: not a git repository; nothing to commit")
			return nil
		}
		if err := git.Commit(p.Dir, files, "nixer: "+what); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "committed: nixer: %s\n", what)
	}
	return nil
}

func pluralIt(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}

func (a *app) rm(dirFlag string, args []string) error {
	fs := a.flagSet("rm")
	var k kindFlags
	k.register(fs)
	var ef editFlags
	ef.register(fs)
	names, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return errors.New("rm: no package names given")
	}
	p, err := a.project(dirFlag)
	if err != nil {
		return err
	}
	ts := k.selected(p)
	explicit := len(ts) > 0
	if !explicit {
		ts = p.Targets
	}
	res, err := edit.Remove(p.Dir, ts, names, edit.RemoveOptions{UnstableSet: p.Unstable, Strict: explicit, DryRun: ef.dry})
	a.report(res)
	if err != nil {
		return err
	}
	if err := a.afterEdit(p, res, ef, "rm "+strings.Join(names, " ")); err != nil {
		return err
	}
	if len(res.Missing) > 0 {
		return exitError(1)
	}
	return nil
}

func (a *app) list(dirFlag string, args []string) error {
	fs := a.flagSet("list")
	var k kindFlags
	k.register(fs)
	if _, err := parse(fs, args); err != nil {
		return err
	}
	p, err := a.project(dirFlag)
	if err != nil {
		return err
	}
	ts := k.selected(p)
	explicit := len(ts) > 0
	if !explicit {
		ts = p.Targets
	}
	lists, err := edit.Lists(p.Dir, ts)
	if err != nil {
		return err
	}
	for _, l := range lists {
		if l.Absent && !explicit {
			continue // this config has no such list; only say so when asked
		}
		if l.Error != "" {
			fmt.Fprintf(a.out, "%s (%s): not found\n", l.Label, l.File)
			continue
		}
		fmt.Fprintf(a.out, "%s (%s):\n", l.Label, l.File)
		for _, i := range l.Items {
			fmt.Fprintln(a.out, "  "+i)
		}
	}
	return nil
}

func (a *app) ui(dirFlag string, args []string) error {
	fs := a.flagSet("ui")
	port := fs.Int("port", 0, "port to listen on (default: random)")
	noOpen := fs.Bool("no-open", false, "do not open the browser")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	p, err := a.project(dirFlag)
	if err != nil {
		return err
	}
	return ui.Run(p, *port, !*noOpen, a.out)
}

func (a *app) search(dirFlag string, args []string) error {
	fs := a.flagSet("search")
	brew := fs.Bool("b", false, "search Homebrew instead of nixpkgs")
	terms, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(terms) == 0 {
		return errors.New("search: no search terms given")
	}
	if *brew {
		return a.passthrough("brew", append([]string{"search"}, terms...)...)
	}
	ref := config.Registry
	if p, err := a.project(dirFlag); err == nil {
		ref = p.PinnedNixpkgs()
	}
	if ref == config.Registry {
		fmt.Fprintln(a.err, "note: no pinned nixpkgs found in flake.lock; searching the global registry")
	}
	return a.passthrough("nix", append([]string{"search", ref}, terms...)...)
}

func (a *app) apply(dirFlag string, args []string) error {
	fs := a.flagSet("apply")
	host := fs.String("host", "", "darwinConfigurations name (default: nixer.json, the flake, then LocalHostName)")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	p, err := a.project(dirFlag)
	if err != nil {
		return err
	}
	return a.rebuild(p, *host)
}

func (a *app) rebuild(p *config.Project, host string) error {
	host, err := p.ResolveHost(host)
	if err != nil {
		return fmt.Errorf("%w (use -host or set \"host\" in %s)", err, config.FileName)
	}
	flake := p.Dir + "#" + host
	// Build first, as the user, so a bad edit never reaches activation.
	fmt.Fprintf(a.out, "$ darwin-rebuild build --flake %s\n", flake)
	if err := rebuild.Build(context.Background(), flake, a.out, a.err); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			fmt.Fprintln(a.err, "build failed; nothing was activated (`nixer undo` reverts the last edit)")
			return exitError(ee.ExitCode())
		}
		return err
	}
	return a.passthrough("sudo", "darwin-rebuild", "switch", "--flake", flake)
}

func (a *app) undo(dirFlag string, args []string) error {
	fs := a.flagSet("undo")
	dry := fs.Bool("n", false, "show what would be restored, change nothing")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	dir, err := config.Dir(dirFlag)
	if err != nil {
		return err
	}
	res, err := edit.Undo(dir, *dry)
	if errors.Is(err, edit.ErrNothingToUndo) {
		fmt.Fprintln(a.out, "Nothing to undo.")
		return nil
	}
	a.report(res)
	if err != nil {
		return err
	}
	if *dry {
		fmt.Fprintln(a.out, "\nDry run: no files were written.")
	}
	return nil
}

func (a *app) clean(_ string, args []string) error {
	fs := a.flagSet("clean")
	old := fs.Bool("old", false, "also delete old generations (no rollback to them afterwards)")
	dry := fs.Bool("n", false, "show what would be deleted, delete nothing")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	var cmd []string
	if !*dry {
		cmd = []string{"sudo"}
	}
	cmd = append(cmd, "nix-collect-garbage")
	if *old {
		cmd = append(cmd, "-d")
	}
	if *dry {
		cmd = append(cmd, "--dry-run")
	}
	return a.passthrough(cmd[0], cmd[1:]...)
}

// project resolves the config directory and loads its nixer.json.
func (a *app) project(dirFlag string) (*config.Project, error) {
	dir, err := config.Dir(dirFlag)
	if err != nil {
		return nil, err
	}
	return config.Load(dir)
}

func (a *app) edit(dirFlag string, args []string) error {
	fs := a.flagSet("edit")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	p, err := a.project(dirFlag)
	if err != nil {
		return err
	}
	file := p.Targets[0].File
	if len(pos) > 0 {
		file = pos[0]
	}
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	return a.passthrough(editor, filepath.Join(p.Dir, file))
}

// doctor prints what nixer detected about the config. It reports problems
// instead of stopping at the first one, and exits 1 if any were found.
func (a *app) doctor(dirFlag string, args []string) error {
	fs := a.flagSet("doctor")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	dir, err := config.Dir(dirFlag)
	if err != nil {
		fmt.Fprintf(a.out, "config dir: %v\n", err)
		return exitError(1)
	}
	problems := 0
	fmt.Fprintf(a.out, "config dir: %s\n", dir)
	p, err := config.Load(dir)
	if err != nil {
		fmt.Fprintf(a.out, "%s: %v\n", config.FileName, err)
		return exitError(1)
	}
	if _, err := os.Stat(filepath.Join(dir, config.FileName)); err == nil {
		fmt.Fprintf(a.out, "%s: found\n", config.FileName)
	} else {
		fmt.Fprintf(a.out, "%s: none (using defaults)\n", config.FileName)
	}
	if host, src, err := p.ResolveHostSource(""); err != nil {
		fmt.Fprintf(a.out, "host: %v\n", err)
		problems++
	} else {
		fmt.Fprintf(a.out, "host: %s (from %s)\n", host, src)
	}
	fmt.Fprintf(a.out, "unstable set: %s\n", p.Unstable)
	fmt.Fprintln(a.out, "targets:")
	lists, err := edit.Lists(dir, p.Targets)
	if err != nil {
		return err
	}
	found := 0
	for _, l := range lists {
		switch {
		case l.Absent:
			fmt.Fprintf(a.out, "  %-7s %-18s %-27s not present (skipped)\n", l.Kind, l.File, attrOf(p, l.Kind))
		case l.Error != "":
			fmt.Fprintf(a.out, "  %-7s %-18s %-27s ERROR: %s\n", l.Kind, l.File, attrOf(p, l.Kind), l.Error)
			problems++
		default:
			found++
			fmt.Fprintf(a.out, "  %-7s %-18s %-27s found, %d items\n", l.Kind, l.File, attrOf(p, l.Kind), len(l.Items))
		}
	}
	if found == 0 {
		fmt.Fprintf(a.out, "no editable lists found: point targets at your files in %s (see the README)\n", config.FileName)
		problems++
	}
	for _, tool := range []string{"nix", "darwin-rebuild"} {
		if path, err := exec.LookPath(tool); err == nil {
			fmt.Fprintf(a.out, "%s: %s\n", tool, path)
		} else {
			fmt.Fprintf(a.out, "%s: not found on PATH\n", tool)
		}
	}
	if problems > 0 {
		return exitError(1)
	}
	return nil
}

func attrOf(p *config.Project, kind string) string {
	t, _ := p.ByKind(kind)
	return t.Attr
}

// passthrough runs a command attached to the terminal, echoing it first.
func (a *app) passthrough(name string, args ...string) error {
	fmt.Fprintf(a.out, "$ %s %s\n", name, strings.Join(args, " "))
	c := exec.Command(name, args...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, a.out, a.err
	if err := c.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return exitError(ee.ExitCode())
		}
		return err
	}
	return nil
}
