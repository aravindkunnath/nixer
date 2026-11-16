# nixer roadmap

Scope: macOS only. nixer manages flake-based nix-darwin configs (with
home-manager and nix-homebrew lists inside them).

Goal: anyone with such a config can point nixer at it and add, remove, and
apply packages safely, without first reshaping their config to match the
author's.

Today nixer assumes one machine's layout: `home.nix` + `configuration.nix`,
fixed attribute spellings, and the name `pkgs-unstable`. The list below is the
smallest set of changes that removes those assumptions.

## 1. Works on configs other than the author's

- [x] **Project config file** (`nixer.json` in the config dir, optional).
      Declares targets (`kind → file + attribute`), the unstable prefix, and
      the host name. The current hard-coded table becomes the default when the
      file is absent.
- [x] **Host detection.** Pick the `darwinConfigurations` entry from the flake
      when there is exactly one; fall back to LocalHostName, then `-host`.
      Many configs are not keyed by the machine's hostname.
- [x] **Optional targets.** A config with no Homebrew or no home-manager
      should list and edit the targets it has without errors about the rest.
- [x] **`nixer doctor`.** Prints what was detected: config dir, host, each
      target with its file and whether the list was found. This is the first
      thing a new user runs and the first thing asked for in bug reports.

## 2. Edits that do not break the config

- [x] **Match the list's own style.** If existing items are written
      `pkgs.ripgrep` (no `with pkgs;`), add `pkgs.<name>`, not a bare name.
- [x] **Several items on one line.** `[ git curl wget ]` is currently read as a
      single item called `git curl wget`; split on whitespace outside strings
      and parentheses.
- [x] **Both attribute spellings.** Find `home.packages = [` and
      `home = { packages = [`; likewise `homebrew.casks = [` and the nested
      form. Today each target supports only one spelling.
- [x] **Skip `/* */` comments and `''` strings** when matching brackets.
- [x] **Refuse what it cannot parse.** Lists built with `++`, `lib.optionals`,
      or `(pkg.override { })` entries produce a clear error, not a best guess.

## 3. Safe to run

- [x] **`--dry-run` / `-n`** on `add` and `rm`: print the diff, write nothing.
- [x] **Build before switch.** `apply` runs `darwin-rebuild build` first
      (no sudo) and stops on failure, so a bad edit never reaches activation.
- [x] **`nixer undo`.** Restores the files from before the last edit (one
      level; a backup under the config dir's `.nixer/`).
- [x] **Atomic writes** (temp file + rename) that keep the file's mode.
- [x] **Check and search against the pinned nixpkgs.** Existence checks and
      `search` use the nixpkgs input locked in the user's `flake.lock`, not the
      global registry, so "exists" means "exists in what you will build".
- [x] **`nixer clean`** (added on request). `sudo nix-collect-garbage`; `--old` also
      deletes old generations, `-n` is a dry run.
- [x] **Git awareness.** Flakes ignore untracked files; warn when an edited
      file is untracked, and offer `-m` to commit the change after a
      successful apply.

## 4. Project basics

- [ ] Put the repo under git, tag releases, add `nixer version`.
- [ ] CI: `go vet`, `go test`, `nix build` on macOS.
- [ ] Drop the Linux systems from `flake.nix`; the binary cannot apply there.
- [ ] Tests for `edit`, `config`, and `cli` against fixture configs covering
      the layouts in section 2 (only `nixedit`, `diff`, and `ui` are tested now).
- [ ] README: a "supported layouts" section and a `nixer.json` example.

## Not now

Kept out deliberately to stay minimal: NixOS and standalone home-manager
support, flake input updates, generations /
rollback (the native tools already do these in one command; `nixer clean`
is only a thin wrapper), editing options other than lists (`programs.*.enable`, services),
multi-host management, and a full Nix parser. Revisit a real parser only if
section 2 keeps growing.
