{
  description = "Withings smart-scale weight bridge to Garmin Connect";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs }:
    let
      systems = [ "x86_64-linux" "aarch64-linux" "x86_64-darwin" "aarch64-darwin" ];
      forAllSystems = nixpkgs.lib.genAttrs systems;
      revision = if self ? shortRev then self.shortRev else "dirty";
    in {
      packages = forAllSystems (system:
        let
          pkgs = import nixpkgs { inherit system; };
          go = pkgs.go_1_25;
        in {
          default = (pkgs.buildGoModule.override { inherit go; }) {
            pname = "withings2garmin";
            version = "0.1.0";
            src = pkgs.lib.cleanSourceWith {
              src = ./.;
              filter = path: type:
                let baseName = baseNameOf path;
                in baseName != ".git" && baseName != ".direnv" && baseName != "result";
            };
            vendorHash = "sha256-e6idC1E4jUWVnaymBPKvg2Z1iMJVhHe14BlQPujphoA=";
            env.CGO_ENABLED = "0";
            ldflags = [
              "-s" "-w"
              "-X main.version=0.1.0"
              "-X main.revision=${revision}"
              "-X main.buildDate=1970-01-01T00:00:00Z"
            ];
            meta.mainProgram = "withings2garmin";
          };
        });

      devShells = forAllSystems (system:
        let
          pkgs = import nixpkgs { inherit system; };
          go = pkgs.go_1_25;
        in {
          default = pkgs.mkShell {
            packages = with pkgs; [ go gopls gotools golangci-lint govulncheck delve alejandra ];
          };
        });

      nixosModules.default = import ./nix/module.nix self;

      checks = forAllSystems (system: {
        package = self.packages.${system}.default;
      });
    };
}
