# nixer

Add and remove packages in your nix-darwin / home-manager config without opening the files.
It edits the relevant `[ ... ]` list in place, keeps your formatting and comments, and shows a diff.

```sh
nixer add ripgrep fd          # home.nix  home.packages (default)
nixer add -s wget             # configuration.nix  environment.systemPackages
nixer add -u neovim           # as pkgs-unstable.neovim
nixer add -F nerd-fonts.fira-code   # fonts.packages
nixer add -c firefox          # homebrew cask (-b brew, -t tap)
nixer rm fd
nixer add -n ripgrep          # dry run: print the diff, write nothing
nixer undo                    # restore the files from before the last add/rm
nixer clean                   # sudo nix-collect-garbage (--old: also old generations, -n: dry run)
nixer list
nixer search ripgrep          # -b to search Homebrew
nixer apply                   # sudo darwin-rebuild switch --flake <dir>#<host>
nixer ui                      # manage everything in a local web page
nixer doctor                  # show what nixer detected (include this in bug reports)
```

`add` and `rm` take `-a` to run `apply` straight afterwards, `-n` to only print the diff, and `-m` to
`git commit` the changed files after a successful apply (implies `-a`).

`apply` first runs `darwin-rebuild build` (no sudo) and only switches if it succeeds, so a bad edit never
reaches activation. Edits are written atomically (temp file and rename, file mode kept), and the files as
they were before the last `add`/`rm` are saved under `.nixer/` so `nixer undo` can restore them (one level).

`add` checks the package exists in the nixpkgs revision pinned in your `flake.lock` (the `nixpkgs` input, or
the one named by `"nixpkgs"` in `nixer.json`), not the global registry, and refuses typos unless you pass
`-f`. `search` uses the same pin. If your config is in a git repository, nixer warns when an edited file is
untracked, because flakes cannot see untracked files.

## Web UI

`nixer ui` serves a page on `127.0.0.1` (random port, `-port N` to pick one, `-no-open` to skip the
browser). It lists everything in your config with remove buttons, searches nixpkgs and Homebrew,
adds packages in one click, shows the diff of every change, and has an Apply button that streams
`darwin-rebuild` output. If `sudo` needs a password, Apply asks macOS for administrator access instead.

Because the server edits files and can run `darwin-rebuild`, it only accepts requests that carry a
random per-run token (in the printed URL), come from its own loopback address, and have a matching
`Origin`. Stop it with Ctrl-C.

## Config location

`--dir`, then the nearest directory from the current one upwards that contains `flake.nix` (so it works from
any subfolder of a config), then `$NIXER_DIR`. `nixer doctor` prints which directory it picked.
Expected layout: `home.nix` (with `home.packages`) and `configuration.nix`
(`environment.systemPackages`, `fonts.packages`, and `homebrew.{brews,casks,taps}`).

Each list may be written dotted (`homebrew.casks = [ ... ];`) or nested (`homebrew = { casks = [ ... ]; };`),
over several lines or on one (`[ git curl ]`), with or without `with pkgs;`. New names follow the list's
style (`pkgs.git` if that is how the list is written). Comments and strings, including `''` strings, are
skipped when matching brackets. Lists built with `++`, `lib.optionals`, or entries such as
`(pkg.override { })` are refused with an error naming the line, rather than edited by guesswork.

### nixer.json

If your config is laid out differently, put an optional `nixer.json` next to `flake.nix`. Every
field is optional; anything you leave out keeps the default above.

```json
{
  "host": "work-mac",
  "unstable": "unstable",
  "targets": {
    "home": { "file": "modules/home.nix" },
    "cask": { "file": "darwin.nix", "attr": "homebrew.casks" }
  }
}
```

- `host`: the `darwinConfigurations` name `apply` uses. Order: `-host`, this field, the flake's only entry (or the one matching `LocalHostName`), then `LocalHostName`. With several entries and no match, nixer asks you to pick one.
- `nixpkgs`: the flake input whose pin is used for `search` and the existence check (default `nixpkgs`).
- `unstable`: the attribute set `add -u` writes, e.g. `unstable.neovim` (default `pkgs-unstable`).
- `targets`: per kind (`home`, `system`, `font`, `brew`, `cask`, `tap`), the `file` (inside the config
  directory) and `attr` to edit. Unknown keys and kinds are rejected.

## Install

```sh
nix run github:aksappy/nixer -- list      # try it
nix profile install github:aksappy/nixer  # or add the flake as an input
```

Prebuilt macOS binaries (arm64 and amd64, with `SHA256SUMS`) are attached to each
[GitHub release](../../releases). Every push to `main` that passes CI publishes a release for the version in
`VERSION` if it has none yet, so to cut a release bump `VERSION` and merge.

From source: `go build -o bin/nixer ./cmd/nixer`.

## Layout

```
cmd/nixer/       entry point
internal/cli/        commands and flag parsing
internal/nixedit/    in-place Nix list editing (tested)
internal/config/     config dir resolution, nixer.json, and the list each kind maps to
internal/diff/       unified diffs for showing edits
internal/edit/       add/remove on disk, shared by the CLI and the UI
internal/search/     nixpkgs and Homebrew search
internal/rebuild/    the no-sudo `darwin-rebuild build` run before switching
internal/git/        untracked-file detection and committing edits
internal/ui/         web server and embedded page (web/index.html)
```
