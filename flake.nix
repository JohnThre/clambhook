{
  description = "clambhook local network client";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs }:
    let
      systems = [
        "aarch64-darwin"
        "x86_64-darwin"
      ];
      forAllSystems = nixpkgs.lib.genAttrs systems;
    in {
      packages = forAllSystems (system:
        let
          pkgs = import nixpkgs { inherit system; };
          version = self.shortRev or self.dirtyShortRev or "dev";
        in {
          default = pkgs.buildGoModule {
            pname = "clambhook";
            inherit version;
            src = self;

            vendorHash = null;

            nativeBuildInputs = [ pkgs.pkg-config ];
            buildInputs = [ pkgs.libsodium ];
            preBuild = "make -C clib";
            ldflags = [ "-X" "main.version=${version}" ];
            subPackages = [
              "cmd/clambhook"
              "cmd/clambhook-tui"
            ];

            meta = with pkgs.lib; {
              description = "Local network client daemon and terminal dashboard";
              homepage = "https://github.com/JohnThre/clambhook";
              license = {
                shortName = "Clambhook-Proprietary-View-Only";
                fullName = "Clambhook Proprietary Source-Available License";
                url = "https://github.com/JohnThre/clambhook/blob/main/LICENSE";
                free = false;
                redistributable = false;
              };
              mainProgram = "clambhook";
              platforms = platforms.darwin;
            };
          };
        });

      apps = forAllSystems (system: {
        default = {
          type = "app";
          program = "${self.packages.${system}.default}/bin/clambhook";
        };
        tui = {
          type = "app";
          program = "${self.packages.${system}.default}/bin/clambhook-tui";
        };
      });

      devShells = forAllSystems (system:
        let pkgs = import nixpkgs { inherit system; };
        in {
          default = pkgs.mkShell {
            nativeBuildInputs = [
              pkgs.go
              pkgs.gnumake
              pkgs.pkg-config
            ];
            buildInputs = [ pkgs.libsodium ];
          };
        });
    };
}
