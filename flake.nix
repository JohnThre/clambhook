# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

{
  description = "ClambHook C17 runtime and command-line clients";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs }:
    let
      systems = [ "aarch64-darwin" "aarch64-linux" "x86_64-darwin" "x86_64-linux" ];
      forAllSystems = nixpkgs.lib.genAttrs systems;
    in {
      packages = forAllSystems (system:
        let
          pkgs = import nixpkgs { inherit system; };
          version = self.shortRev or self.dirtyShortRev or "dev";
        in {
          default = pkgs.stdenv.mkDerivation {
            pname = "clambhook";
            inherit version;
            src = self;

            nativeBuildInputs = with pkgs; [ cmake ninja pkg-config ];
            buildInputs = with pkgs; [ curl libuv libsodium openssl ];
            cmakeFlags = [
              "-DCLAMBHOOK_BUILD_TESTS=ON"
              "-DCLAMBHOOK_WARNINGS_AS_ERRORS=ON"
            ];
            doCheck = true;

            meta = with pkgs.lib; {
              description = "ClambHook C17 daemon, terminal UI, and license helper";
              homepage = "https://github.com/JohnThre/clambhook";
              license = licenses.gpl3Only;
              mainProgram = "clambhook";
              platforms = platforms.unix;
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
            nativeBuildInputs = with pkgs; [ cmake ninja pkg-config maven jdk17 ];
            buildInputs = with pkgs; [ curl libuv libsodium openssl ];
          };
        });
    };
}
