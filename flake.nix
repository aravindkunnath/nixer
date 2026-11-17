{
  description = "nixer: add and remove packages in your nix-darwin config without editing files";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";

  outputs = { self, nixpkgs }:
    let
      # Single source of truth for the version; the release workflow reads it too.
      version = nixpkgs.lib.removeSuffix "\n" (builtins.readFile ./VERSION);
      # macOS only: nixer drives darwin-rebuild, which does not exist elsewhere.
      systems = [ "aarch64-darwin" "x86_64-darwin" ];
      forAll = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
    in
    {
      packages = forAll (pkgs: rec {
        nixer = pkgs.buildGoModule {
          pname = "nixer";
          inherit version;
          src = ./.;
          ldflags = [ "-X github.com/aravindkunnath/nixer/internal/version.Version=${version}" ];
          vendorHash = null; # standard library only
          subPackages = [ "cmd/nixer" ];
          meta.mainProgram = "nixer";
        };
        default = nixer;
      });

      overlays.default = final: _prev: {
        nixer = self.packages.${final.stdenv.hostPlatform.system}.nixer;
      };

      devShells = forAll (pkgs: {
        default = pkgs.mkShell { packages = [ pkgs.go pkgs.gopls ]; };
      });
    };
}
