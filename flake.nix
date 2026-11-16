{
  description = "nixer: add and remove packages in your nix-darwin config without editing files";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";

  outputs = { self, nixpkgs }:
    let
      systems = [ "aarch64-darwin" "x86_64-darwin" "aarch64-linux" "x86_64-linux" ];
      forAll = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
    in
    {
      packages = forAll (pkgs: rec {
        nixer = pkgs.buildGoModule {
          pname = "nixer";
          version = "0.1.0";
          src = ./.;
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
